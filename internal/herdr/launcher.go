package herdr

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
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
