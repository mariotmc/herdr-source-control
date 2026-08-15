package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestRunRejectsMissingAndUnknownCommands(t *testing.T) {
	for _, args := range [][]string{nil, {"unknown"}, {"open", "extra"}} {
		var stderr bytes.Buffer
		if code := run(context.Background(), args, &stderr); code != 2 {
			t.Fatalf("run(%q) = %d, want 2", args, code)
		}
		if stderr.String() != "usage: herdr-source-control <open|tui|fetch>\n" {
			t.Fatalf("unexpected usage: %q", stderr.String())
		}
	}
}

func TestFetchExitsZeroAndSilentlyOutsideRepository(t *testing.T) {
	directory := t.TempDir()
	t.Setenv("PWD", directory)
	t.Setenv("HERDR_PLUGIN_CONTEXT_JSON", "")
	t.Setenv("HERDR_PLUGIN_STATE_DIR", filepath.Join(directory, "state"))
	t.Setenv("HERDR_WORKSPACE_ID", "")
	var stderr bytes.Buffer
	if code := run(context.Background(), []string{"fetch"}, &stderr); code != 0 {
		t.Fatalf("run(fetch) = %d, want 0", code)
	}
	if stderr.Len() != 0 {
		t.Fatalf("fetch wrote to stderr: %q", stderr.String())
	}
}

func TestNewLoggerUsesPrivatePermissions(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "state")
	logger, warning, closeLog := newLogger(directory)
	if warning != nil {
		t.Fatal(warning)
	}
	logger.Info("test")
	closeLog()
	for path, want := range map[string]os.FileMode{
		directory: 0o700,
		filepath.Join(directory, "herdr-source-control.log"): 0o600,
	} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != want {
			t.Errorf("%s mode = %o, want %o", path, got, want)
		}
	}
}
