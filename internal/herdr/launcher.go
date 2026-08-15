package herdr

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"syscall"
)

type Launcher struct {
	Client         *Client
	StateDir       string
	PWD            string
	ContextJSON    string
	RepositoryRoot RepositoryRootFunc
}

type OpenResult struct {
	Target Target
	PaneID string
	TabID  string
	Opened bool
}

func NewLauncherFromEnv() *Launcher {
	return &Launcher{
		Client:         NewClient(os.Getenv("HERDR_BIN_PATH")),
		StateDir:       os.Getenv("HERDR_PLUGIN_STATE_DIR"),
		PWD:            os.Getenv("PWD"),
		ContextJSON:    os.Getenv("HERDR_PLUGIN_CONTEXT_JSON"),
		RepositoryRoot: SystemRepositoryRoot,
	}
}

func (l *Launcher) Open(ctx context.Context) (OpenResult, error) {
	pluginContext, err := ParseContext(l.ContextJSON)
	if err != nil {
		return OpenResult{}, err
	}
	if pluginContext.WorkspaceID == "" {
		return OpenResult{}, errors.New("Herdr plugin context is missing workspace_id; invoke Open Source Control from a workspace")
	}
	startDir := pluginContext.StartDir(l.PWD)
	if startDir == "" {
		return OpenResult{}, errors.New("PWD is empty and no invoking pane directory is available")
	}
	resolver := l.RepositoryRoot
	if resolver == nil {
		resolver = SystemRepositoryRoot
	}
	target, err := ResolveTarget(ctx, startDir, resolver)
	if err != nil {
		return OpenResult{}, err
	}
	if l.StateDir == "" {
		return OpenResult{}, errors.New("HERDR_PLUGIN_STATE_DIR is empty")
	}
	if l.Client == nil {
		l.Client = NewClient("")
	}

	lock, err := acquireLock(filepath.Join(l.StateDir, "open.lock"))
	if err != nil {
		return OpenResult{}, err
	}
	defer lock.Close()

	panes, err := l.Client.ListPanes(ctx, pluginContext.WorkspaceID)
	if err != nil {
		return OpenResult{}, err
	}
	for _, pane := range panes {
		if pane.Tokens[PluginID] != target.Identity {
			continue
		}
		info, inspectErr := l.Client.ProcessInfo(ctx, pane.ID)
		liveness := LivenessIndeterminate
		if inspectErr == nil {
			liveness = ClassifyLiveness(info)
		}
		if liveness != LivenessExited {
			if err := l.Client.FocusPane(ctx, pane.ID); err != nil {
				return OpenResult{}, err
			}
			l.closeRestoredDuplicates(ctx, panes, target)
			return OpenResult{Target: target, PaneID: pane.ID, TabID: pane.TabID}, nil
		}
		if err := l.Client.ClosePane(ctx, pane.ID); err != nil {
			return OpenResult{}, err
		}
		if _, err := l.Client.ListPanes(ctx, pluginContext.WorkspaceID); err != nil {
			return OpenResult{}, err
		}
		break
	}

	reclaimed, ok, err := l.reclaim(ctx, panes, target)
	if err != nil {
		return OpenResult{}, err
	}
	if ok {
		l.closeRestoredDuplicates(ctx, panes, target, reclaimed.PaneID)
		return reclaimed, nil
	}

	opened, err := l.Client.OpenPane(ctx, OpenPaneRequest{
		WorkspaceID: pluginContext.WorkspaceID,
		CWD:         target.Root,
		Identity:    target.Identity,
	})
	if err != nil {
		return OpenResult{}, err
	}
	if err := l.Client.ReportIdentity(ctx, opened.PaneID, target.Identity); err != nil {
		closeErr := l.Client.ClosePane(ctx, opened.PaneID)
		if closeErr != nil {
			return OpenResult{}, fmt.Errorf("report pane identity: %w; close unidentifiable pane: %v", err, closeErr)
		}
		return OpenResult{}, fmt.Errorf("report pane identity: %w", err)
	}
	if err := l.Client.RenameTab(ctx, opened.TabID); err != nil {
		return OpenResult{}, err
	}
	return OpenResult{Target: target, PaneID: opened.PaneID, TabID: opened.TabID, Opened: true}, nil
}

// reclaim restarts the plugin inside a pane that Herdr's snapshot restore brought back as a
// bare shell. Herdr persists only a pane's label and cwd, never our identity token, so a
// restored pane is unrecognisable by token and would otherwise cause a duplicate tab.
func (l *Launcher) reclaim(ctx context.Context, panes []Pane, target Target) (OpenResult, bool, error) {
	binary, err := pluginBinary()
	if err != nil {
		return OpenResult{}, false, nil
	}
	for _, pane := range panes {
		if !l.isRestoredPane(ctx, pane, target) {
			continue
		}
		argv := []string{"env",
			"HERDR_SOURCE_CONTROL_ROOT=" + target.Root,
			"HERDR_SOURCE_CONTROL_ID=" + target.Identity,
			"HERDR_PANE_ID=" + pane.ID,
			binary, "tui"}
		if err := l.Client.RunInPane(ctx, pane.ID, argv); err != nil {
			return OpenResult{}, false, err
		}
		if err := l.Client.ReportIdentity(ctx, pane.ID, target.Identity); err != nil {
			return OpenResult{}, false, fmt.Errorf("report pane identity: %w", err)
		}
		if err := l.Client.RenameTab(ctx, pane.TabID); err != nil {
			return OpenResult{}, false, err
		}
		if err := l.Client.FocusTab(ctx, pane.TabID); err != nil {
			return OpenResult{}, false, err
		}
		return OpenResult{Target: target, PaneID: pane.ID, TabID: pane.TabID}, true, nil
	}
	return OpenResult{}, false, nil
}

// closeRestoredDuplicates removes leftover restored panes for this repository once a live one
// is in use, which is what leaves a user with one working and one blank Source Control tab.
// Best effort: failing to tidy up must never fail the open.
func (l *Launcher) closeRestoredDuplicates(ctx context.Context, panes []Pane, target Target, keep ...string) {
	for _, pane := range panes {
		if slices.Contains(keep, pane.ID) || !l.isRestoredPane(ctx, pane, target) {
			continue
		}
		_ = l.Client.ClosePlainPane(ctx, pane.ID)
	}
}

// isRestoredPane reports whether a pane is one of ours that Herdr's snapshot restore left as a
// bare shell. It never matches a pane running anything the user could be using.
func (l *Launcher) isRestoredPane(ctx context.Context, pane Pane, target Target) bool {
	if pane.Tokens[PluginID] != "" || pane.Label != TabName || !isTargetRoot(pane.CWD, target) {
		return false
	}
	info, err := l.Client.ProcessInfo(ctx, pane.ID)
	return err == nil && IsIdleShell(info)
}

// isTargetRoot also accepts the canonical root so a repository reached through a symlink,
// where Herdr may persist either spelling of the path, still matches.
func isTargetRoot(cwd string, target Target) bool {
	clean := filepath.Clean(cwd)
	return clean == target.Root || (target.CanonicalRoot != "" && clean == target.CanonicalRoot)
}

func pluginBinary() (string, error) {
	if root := os.Getenv("HERDR_PLUGIN_ROOT"); root != "" {
		return filepath.Join(root, "bin", PluginID), nil
	}
	return os.Executable()
}

func acquireLock(path string) (*os.File, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create Herdr plugin state directory: %w", err)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open Herdr launcher lock: %w", err)
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX); err != nil {
		file.Close()
		return nil, fmt.Errorf("lock Herdr launcher: %w", err)
	}
	return file, nil
}
