package herdr

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
)

const (
	PluginID     = "herdr-source-control"
	EntrypointID = "source-control"
	TabName      = "Source Control"
)

type CommandError struct {
	Args   []string
	Output string
	Err    error
}

func (e *CommandError) Error() string {
	if e.Output == "" {
		return fmt.Sprintf("herdr %s: %v", strings.Join(e.Args, " "), e.Err)
	}
	return fmt.Sprintf("herdr %s: %s", strings.Join(e.Args, " "), e.Output)
}

func (e *CommandError) Unwrap() error { return e.Err }

type Runner interface {
	Run(context.Context, string, ...string) ([]byte, error)
}

type ExecRunner struct{}

func (ExecRunner) Run(ctx context.Context, binary string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, binary, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, &CommandError{Args: append([]string(nil), args...), Output: strings.TrimSpace(stderr.String()), Err: err}
	}
	return stdout.Bytes(), nil
}

type Client struct {
	Binary string
	Runner Runner
}

func NewClient(binary string) *Client {
	if binary == "" {
		binary = "herdr"
	}
	return &Client{Binary: binary, Runner: ExecRunner{}}
}

type responseEnvelope struct {
	Result json.RawMessage `json:"result"`
}

func (c *Client) runJSON(ctx context.Context, result any, args ...string) error {
	output, err := c.runner().Run(ctx, c.binary(), args...)
	if err != nil {
		return err
	}
	var envelope responseEnvelope
	if err := json.Unmarshal(output, &envelope); err != nil {
		return fmt.Errorf("decode herdr %s response: %w", strings.Join(args, " "), err)
	}
	if len(envelope.Result) == 0 || bytes.Equal(envelope.Result, []byte("null")) {
		return fmt.Errorf("decode herdr %s response: missing result", strings.Join(args, " "))
	}
	if err := json.Unmarshal(envelope.Result, result); err != nil {
		return fmt.Errorf("decode herdr %s result: %w", strings.Join(args, " "), err)
	}
	return nil
}

func (c *Client) run(ctx context.Context, args ...string) error {
	_, err := c.runner().Run(ctx, c.binary(), args...)
	return err
}

func (c *Client) binary() string {
	if c.Binary == "" {
		return "herdr"
	}
	return c.Binary
}

func (c *Client) runner() Runner {
	if c.Runner == nil {
		return ExecRunner{}
	}
	return c.Runner
}

type Pane struct {
	ID       string
	TabID    string
	Label    string
	CWD      string
	Tokens   map[string]string
	metadata json.RawMessage
}

func (p *Pane) UnmarshalJSON(data []byte) error {
	var raw struct {
		PaneID         string            `json:"pane_id"`
		TabID          string            `json:"tab_id"`
		Label          string            `json:"label"`
		CWD            string            `json:"cwd"`
		Tokens         map[string]string `json:"tokens"`
		MetadataTokens map[string]string `json:"metadata_tokens"`
		Metadata       json.RawMessage   `json:"metadata"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	p.ID, p.TabID = raw.PaneID, raw.TabID
	p.Label, p.CWD = raw.Label, raw.CWD
	p.Tokens = raw.Tokens
	if p.Tokens == nil {
		p.Tokens = raw.MetadataTokens
	}
	p.metadata = raw.Metadata
	if p.Tokens == nil {
		p.Tokens = findStringMap(data, "tokens")
	}
	return nil
}

func findStringMap(data []byte, key string) map[string]string {
	var value any
	if json.Unmarshal(data, &value) != nil {
		return nil
	}
	var visit func(any) map[string]string
	visit = func(current any) map[string]string {
		switch current := current.(type) {
		case map[string]any:
			if candidate, ok := current[key].(map[string]any); ok {
				result := make(map[string]string, len(candidate))
				for name, raw := range candidate {
					if text, ok := raw.(string); ok {
						result[name] = text
					}
				}
				return result
			}
			for _, child := range current {
				if found := visit(child); found != nil {
					return found
				}
			}
		case []any:
			for _, child := range current {
				if found := visit(child); found != nil {
					return found
				}
			}
		}
		return nil
	}
	return visit(value)
}

func (c *Client) ListPanes(ctx context.Context, workspaceID string) ([]Pane, error) {
	var result struct {
		Panes []Pane `json:"panes"`
	}
	if err := c.runJSON(ctx, &result, "pane", "list", "--workspace", workspaceID); err != nil {
		return nil, err
	}
	for _, pane := range result.Panes {
		if pane.ID == "" {
			return nil, errors.New("decode herdr pane list result: pane is missing pane_id")
		}
	}
	return result.Panes, nil
}

type Process struct {
	Name    string   `json:"name"`
	Argv    []string `json:"argv"`
	Cmdline string   `json:"cmdline"`
}

type ProcessInfo struct {
	ForegroundProcesses []Process `json:"foreground_processes"`
}

func (c *Client) ProcessInfo(ctx context.Context, paneID string) (ProcessInfo, error) {
	var result struct {
		ProcessInfo *ProcessInfo `json:"process_info"`
	}
	if err := c.runJSON(ctx, &result, "pane", "process-info", "--pane", paneID); err != nil {
		return ProcessInfo{}, err
	}
	if result.ProcessInfo == nil {
		return ProcessInfo{}, errors.New("decode herdr process-info result: missing process_info")
	}
	return *result.ProcessInfo, nil
}

type Liveness uint8

const (
	LivenessIndeterminate Liveness = iota
	LivenessLive
	LivenessExited
)

func ClassifyLiveness(info ProcessInfo) Liveness {
	if len(info.ForegroundProcesses) == 0 {
		return LivenessIndeterminate
	}
	allIdentifiable := true
	for _, process := range info.ForegroundProcesses {
		executable := processExecutable(process)
		if executable == "" {
			allIdentifiable = false
			continue
		}
		if filepath.Base(executable) != PluginID {
			continue
		}
		if len(process.Argv) == 0 {
			return LivenessLive
		}
		for _, arg := range process.Argv[1:] {
			if arg == "tui" {
				return LivenessLive
			}
		}
	}
	if allIdentifiable {
		return LivenessExited
	}
	return LivenessIndeterminate
}

func processExecutable(process Process) string {
	executable := process.Name
	if len(process.Argv) != 0 {
		executable = process.Argv[0]
	} else if executable == "" {
		if fields := strings.Fields(process.Cmdline); len(fields) != 0 {
			executable = fields[0]
		}
	}
	return executable
}

var idleShells = map[string]bool{
	"bash": true, "csh": true, "dash": true, "fish": true,
	"ksh": true, "sh": true, "tcsh": true, "zsh": true,
}

// IsIdleShell reports whether every foreground process is a bare shell, which is what
// Herdr's snapshot restore leaves in place of a plugin pane it could not restart.
// Anything it cannot positively identify as a shell counts as in use by the user.
func IsIdleShell(info ProcessInfo) bool {
	if len(info.ForegroundProcesses) == 0 {
		return false
	}
	for _, process := range info.ForegroundProcesses {
		executable := processExecutable(process)
		if executable == "" {
			return false
		}
		if !idleShells[strings.TrimPrefix(filepath.Base(executable), "-")] {
			return false
		}
	}
	return true
}

type OpenPaneRequest struct {
	WorkspaceID string
	CWD         string
	Identity    string
}

type OpenedPane struct {
	PaneID string `json:"pane_id"`
	TabID  string `json:"tab_id"`
}

func (c *Client) OpenPane(ctx context.Context, request OpenPaneRequest) (OpenedPane, error) {
	var result struct {
		PaneID     string      `json:"pane_id"`
		TabID      string      `json:"tab_id"`
		Pane       *OpenedPane `json:"pane"`
		PluginPane *struct {
			Pane *OpenedPane `json:"pane"`
		} `json:"plugin_pane"`
	}
	args := []string{"plugin", "pane", "open", "--plugin", PluginID, "--entrypoint", EntrypointID,
		"--placement", "tab", "--workspace", request.WorkspaceID, "--cwd", request.CWD,
		"--env", "HERDR_SOURCE_CONTROL_ROOT=" + request.CWD,
		"--env", "HERDR_SOURCE_CONTROL_ID=" + request.Identity, "--focus"}
	if err := c.runJSON(ctx, &result, args...); err != nil {
		return OpenedPane{}, err
	}
	opened := OpenedPane{PaneID: result.PaneID, TabID: result.TabID}
	if result.Pane != nil {
		opened = *result.Pane
	}
	if result.PluginPane != nil && result.PluginPane.Pane != nil {
		opened = *result.PluginPane.Pane
	}
	if opened.PaneID == "" || opened.TabID == "" {
		return OpenedPane{}, errors.New("decode herdr pane open result: missing pane_id or tab_id")
	}
	return opened, nil
}

func (c *Client) FocusPane(ctx context.Context, paneID string) error {
	return c.run(ctx, "plugin", "pane", "focus", paneID)
}

func (c *Client) ClosePane(ctx context.Context, paneID string) error {
	return c.run(ctx, "plugin", "pane", "close", paneID)
}

// ClosePlainPane closes a pane Herdr no longer owns as a plugin pane, which is what a
// snapshot-restored Source Control pane becomes.
func (c *Client) ClosePlainPane(ctx context.Context, paneID string) error {
	return c.run(ctx, "pane", "close", paneID)
}

func (c *Client) ReportIdentity(ctx context.Context, paneID, identity string) error {
	return c.run(ctx, "pane", "report-metadata", paneID, "--source", PluginID, "--token", PluginID+"="+identity)
}

func (c *Client) RenameTab(ctx context.Context, tabID string) error {
	return c.run(ctx, "tab", "rename", tabID, TabName)
}

func (c *Client) RunInPane(ctx context.Context, paneID string, argv []string) error {
	return c.run(ctx, append([]string{"pane", "run", paneID}, argv...)...)
}

// FocusTab focuses a pane's tab. A reclaimed pane is an ordinary pane rather than a
// plugin-owned one, so `plugin pane focus` does not apply to it.
func (c *Client) FocusTab(ctx context.Context, tabID string) error {
	return c.run(ctx, "tab", "focus", tabID)
}
