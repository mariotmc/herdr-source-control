package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	tea "charm.land/bubbletea/v2"
	"github.com/mariotmc/herdr-source-control/internal/app"
	"github.com/mariotmc/herdr-source-control/internal/domain"
	gitrepo "github.com/mariotmc/herdr-source-control/internal/git"
	"github.com/mariotmc/herdr-source-control/internal/herdr"
)

var (
	version = "0.1.0"
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
	if args[0] != "open" && args[0] != "tui" {
		usage(stderr)
		return 2
	}
	logger, warning, closeLog := newLogger(os.Getenv("HERDR_PLUGIN_STATE_DIR"))
	defer closeLog()
	if warning != nil {
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
		return runTUI(ctx, logger, stderr)
	}
	return 2
}

func runTUI(ctx context.Context, logger *slog.Logger, stderr io.Writer) int {
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
	model := app.New(app.Config{Context: ctx, RepositoryFactory: factory, StartRoot: root, Logger: logger})
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
	fmt.Fprintln(writer, "usage: herdr-source-control <open|tui>")
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
