package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/mariotmc/herdr-source-control/internal/app"
	"github.com/mariotmc/herdr-source-control/internal/domain"
	gitrepo "github.com/mariotmc/herdr-source-control/internal/git"
	"github.com/mariotmc/herdr-source-control/internal/herdr"
	"github.com/mariotmc/herdr-source-control/internal/state"
)

const (
	fetchThrottle    = state.FetchThrottle
	fetchTimeout     = 5 * time.Minute
	fetchStaleAfter  = 15 * time.Minute
	sidebarTokenName = "sc"
)

var (
	version = "0.2.1"
	commit  = "unknown"
)

func main() {
	os.Exit(run(context.Background(), os.Args[1:], os.Stderr))
}

func run(ctx context.Context, args []string, stderr io.Writer) int {
	if len(args) != 1 {
		usage(stderr)
		return 2
	}
	if args[0] != "open" && args[0] != "tui" && args[0] != "fetch" {
		usage(stderr)
		return 2
	}
	stateDir := os.Getenv("HERDR_PLUGIN_STATE_DIR")
	logger, warning, closeLog := newLogger(stateDir)
	defer closeLog()
	if warning != nil && args[0] != "fetch" {
		fmt.Fprintf(stderr, "warning: %v\n", warning)
	}

	switch args[0] {
	case "open":
		logger.Info("starting", "version", version, "commit", commit, "mode", "open")
		if _, err := herdr.NewLauncherFromEnv().Open(ctx); err != nil {
			logger.Error("Herdr launcher failed", "error", err)
			fmt.Fprintln(stderr, err)
			return 1
		}
		return 0
	case "tui":
		return runTUI(ctx, logger, stateDir, stderr)
	case "fetch":
		runFetch(ctx, logger, stateDir)
		return 0
	}
	return 2
}

// runFetch is invoked from Herdr event hooks on every workspace and pane focus.
// It stays silent and always succeeds: a focus event must never surface plugin
// noise or a non-zero exit.
func runFetch(ctx context.Context, logger *slog.Logger, stateDir string) {
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, fetchTimeout)
	defer cancel()

	startDir := os.Getenv("PWD")
	if pluginContext, err := herdr.ParseContext(os.Getenv("HERDR_PLUGIN_CONTEXT_JSON")); err == nil {
		startDir = pluginContext.StartDir(startDir)
	}
	if startDir == "" {
		return
	}
	// Discovery alone answers the throttle, so a hook firing on every pane focus costs one
	// cheap Git call instead of a full working-tree status.
	discovery, err := gitrepo.Discover(ctx, startDir)
	if err != nil {
		logger.Debug("background fetch skipped", "reason", "repository unavailable", "error", err)
		return
	}
	store := state.Store{Dir: stateDir}
	key := state.Key(discovery.Root)
	record, _ := store.Load(key)
	if record.LastAttemptUnix != 0 && time.Since(time.Unix(record.LastAttemptUnix, 0)) < fetchThrottle {
		// Re-assert the badge even when throttled: it is set per workspace but tracked per
		// repository, so without this a flag raised by one transient failure can outlive the
		// success that should have cleared it.
		reportSidebarToken(ctx, record.Stale(fetchStaleAfter))
		return
	}

	repository, err := gitrepo.New(ctx, startDir)
	if err != nil {
		logger.Debug("background fetch skipped", "reason", "repository unavailable", "error", err)
		return
	}
	snapshot, err := repository.Snapshot(ctx)
	if err != nil {
		logger.Debug("background fetch skipped", "reason", "snapshot failed", "error", err)
		return
	}
	record.LastAttemptUnix = time.Now().Unix()
	_ = store.Save(key, record)

	branch := snapshot.Branch
	if branch.State != domain.HeadAttached || branch.Upstream == "" || branch.UpstreamRef == "" ||
		branch.RemoteName == "" || branch.RemoteRef == "" || !branch.CountsKnown {
		return
	}
	fetchErr := repository.Fetch(ctx, branch)
	if fetchErr != nil {
		// A deleted upstream fails on every retry, so warning about it would be permanent noise
		// on every merged branch rather than something the user can act on.
		record.LastError = fetchErr.Error()
		record.LastErrorPermanent = gitrepo.IsKind(fetchErr, gitrepo.ErrorMissingUpstreamRef)
	} else {
		record.LastSuccessUnix, record.LastError, record.LastErrorPermanent = time.Now().Unix(), "", false
	}
	if err := store.Save(key, record); err != nil {
		logger.Warn("record background fetch", "error", err)
	}
	logger.Debug("background fetch", "root", snapshot.Root, "error", fetchErr)
	reportSidebarToken(ctx, record.Stale(fetchStaleAfter))
}

// reportSidebarToken tells Herdr whether the ahead/behind counts it renders from
// local remote-tracking refs can still be trusted.
func reportSidebarToken(ctx context.Context, stale bool) {
	workspace := os.Getenv("HERDR_WORKSPACE_ID")
	if workspace == "" {
		return
	}
	args := []string{"workspace", "report-metadata", workspace, "--source", herdr.PluginID, "--clear-token", sidebarTokenName}
	if stale {
		args = []string{"workspace", "report-metadata", workspace, "--source", herdr.PluginID, "--token", sidebarTokenName + "=stale"}
	}
	binary := os.Getenv("HERDR_BIN_PATH")
	if binary == "" {
		binary = "herdr"
	}
	command := exec.CommandContext(ctx, binary, args...)
	command.Stdin, command.Stdout, command.Stderr = nil, nil, nil
	_ = command.Run()
}

func runTUI(ctx context.Context, logger *slog.Logger, stateDir string, stderr io.Writer) int {
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	root := os.Getenv("HERDR_SOURCE_CONTROL_ROOT")
	if root == "" {
		root = os.Getenv("PWD")
	}
	if root == "" {
		var err error
		root, err = os.Getwd()
		if err != nil {
			fmt.Fprintf(stderr, "determine repository root: %v\n", err)
			return 1
		}
	}
	logger.Info("starting", "version", version, "commit", commit, "mode", "tui", "root", root)
	if err := herdr.RefreshMetadataFromEnv(ctx); err != nil {
		logger.Warn("refresh Herdr metadata", "error", err)
	}
	factory := func(ctx context.Context, startDir string) (domain.Repository, error) {
		return gitrepo.New(ctx, startDir)
	}
	model := app.New(app.Config{Context: ctx, RepositoryFactory: factory, StartRoot: root, StateDir: stateDir, Logger: logger})
	_, err := tea.NewProgram(model, tea.WithContext(ctx)).Run()
	model.Close()
	if err != nil {
		logger.Error("TUI failed", "error", err)
		fmt.Fprintf(stderr, "source control: %v\n", err)
		return 1
	}
	return 0
}

func usage(writer io.Writer) {
	fmt.Fprintln(writer, "usage: herdr-source-control <open|tui|fetch>")
}

func newLogger(stateDir string) (*slog.Logger, error, func()) {
	if stateDir == "" {
		return discardLogger(), fmt.Errorf("HERDR_PLUGIN_STATE_DIR is empty; file logging disabled"), func() {}
	}
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return discardLogger(), fmt.Errorf("create log directory: %w", err), func() {}
	}
	if err := os.Chmod(stateDir, 0o700); err != nil {
		return discardLogger(), fmt.Errorf("secure log directory: %w", err), func() {}
	}
	file, err := os.OpenFile(filepath.Join(stateDir, "herdr-source-control.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return discardLogger(), fmt.Errorf("open log file: %w", err), func() {}
	}
	if err := file.Chmod(0o600); err != nil {
		file.Close()
		return discardLogger(), fmt.Errorf("secure log file: %w", err), func() {}
	}
	return slog.New(slog.NewTextHandler(file, &slog.HandlerOptions{Level: slog.LevelDebug})), nil, func() { _ = file.Close() }
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
