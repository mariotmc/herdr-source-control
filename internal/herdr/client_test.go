package herdr

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type runnerFunc func(context.Context, string, ...string) ([]byte, error)

func (f runnerFunc) Run(ctx context.Context, binary string, args ...string) ([]byte, error) {
	return f(ctx, binary, args...)
}

func TestOpenPaneArgvAndJSON(t *testing.T) {
	var binary string
	var args []string
	client := &Client{Binary: "/custom/herdr", Runner: runnerFunc(func(_ context.Context, gotBinary string, gotArgs ...string) ([]byte, error) {
		binary = gotBinary
		args = append([]string(nil), gotArgs...)
		return []byte(`{"result":{"pane_id":"p1","tab_id":"t1"}}`), nil
	})}

	opened, err := client.OpenPane(context.Background(), OpenPaneRequest{WorkspaceID: "w1", CWD: "/repo path", Identity: "abc"})
	if err != nil {
		t.Fatal(err)
	}
	if opened.PaneID != "p1" || opened.TabID != "t1" || binary != "/custom/herdr" {
		t.Fatalf("OpenPane() = %#v via %q", opened, binary)
	}
	want := "plugin|pane|open|--plugin|herdr-source-control|--entrypoint|source-control|--placement|tab|--workspace|w1|--cwd|/repo path|--env|HERDR_SOURCE_CONTROL_ROOT=/repo path|--env|HERDR_SOURCE_CONTROL_ID=abc|--focus"
	if got := strings.Join(args, "|"); got != want {
		t.Fatalf("argv = %q, want %q", got, want)
	}
}

func TestOpenPaneAcceptsNestedPluginPaneResponse(t *testing.T) {
	client := &Client{Runner: runnerFunc(func(context.Context, string, ...string) ([]byte, error) {
		return []byte(`{"result":{"plugin_pane":{"entrypoint":"source-control","pane":{"pane_id":"p2","tab_id":"t2"},"plugin_id":"herdr-source-control"}}}`), nil
	})}

	opened, err := client.OpenPane(context.Background(), OpenPaneRequest{WorkspaceID: "w1", CWD: "/repo", Identity: "abc"})
	if err != nil {
		t.Fatal(err)
	}
	if opened.PaneID != "p2" || opened.TabID != "t2" {
		t.Fatalf("OpenPane() = %#v", opened)
	}
}

func TestClientJSONAndCommandErrors(t *testing.T) {
	t.Run("malformed JSON", func(t *testing.T) {
		client := &Client{Runner: runnerFunc(func(context.Context, string, ...string) ([]byte, error) {
			return []byte("not json"), nil
		})}
		if _, err := client.ListPanes(context.Background(), "w1"); err == nil || !strings.Contains(err.Error(), "decode herdr") {
			t.Fatalf("ListPanes() error = %v", err)
		}
	})

	t.Run("command error", func(t *testing.T) {
		failure := errors.New("failed")
		client := &Client{Runner: runnerFunc(func(context.Context, string, ...string) ([]byte, error) {
			return nil, failure
		})}
		if err := client.FocusPane(context.Background(), "p1"); !errors.Is(err, failure) {
			t.Fatalf("FocusPane() error = %v", err)
		}
	})
}

func TestExecRunnerCapturesStderr(t *testing.T) {
	path := filepath.Join(t.TempDir(), "herdr")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nprintf 'bad response' >&2\nexit 9\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	client := NewClient(path)
	err := client.FocusPane(context.Background(), "p1")
	var commandError *CommandError
	if !errors.As(err, &commandError) || commandError.Output != "bad response" {
		t.Fatalf("FocusPane() error = %#v", err)
	}
}

func TestClassifyLiveness(t *testing.T) {
	tests := []struct {
		name string
		info ProcessInfo
		want Liveness
	}{
		{"live", ProcessInfo{ForegroundProcesses: []Process{{Argv: []string{"/plugin/herdr-source-control", "tui"}}}}, LivenessLive},
		{"plugin wrong mode", ProcessInfo{ForegroundProcesses: []Process{{Argv: []string{"herdr-source-control", "open"}}}}, LivenessExited},
		{"replacement shell", ProcessInfo{ForegroundProcesses: []Process{{Name: "bash", Argv: []string{"/usr/bin/bash"}}}}, LivenessExited},
		{"empty", ProcessInfo{}, LivenessIndeterminate},
		{"missing fields", ProcessInfo{ForegroundProcesses: []Process{{}}}, LivenessIndeterminate},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := ClassifyLiveness(test.info); got != test.want {
				t.Fatalf("ClassifyLiveness() = %v, want %v", got, test.want)
			}
		})
	}
}
