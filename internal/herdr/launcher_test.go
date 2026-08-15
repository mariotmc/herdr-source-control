package herdr

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

const fakeHerdrScript = `#!/bin/sh
set -eu
printf '%s\n' "$*" >> "$HERDR_FAKE_DIR/calls"
case "$1 $2 $3" in
  "pane list --workspace")
    if [ -f "$HERDR_FAKE_DIR/restored" ]; then
      label='Source Control'
      [ ! -f "$HERDR_FAKE_DIR/restored-label" ] || label=$(cat "$HERDR_FAKE_DIR/restored-label")
      cwd=$(cat "$HERDR_FAKE_DIR/restored")
      if [ -f "$HERDR_FAKE_DIR/restored-extra" ]; then
        printf '{"id":"fake","result":{"panes":[{"pane_id":"pG","tab_id":"tD","workspace_id":"w1","label":"%s","cwd":"%s"},{"pane_id":"pH","tab_id":"tE","workspace_id":"w1","label":"%s","cwd":"%s"}]}}\n' "$label" "$cwd" "$label" "$cwd"
      else
        printf '{"id":"fake","result":{"panes":[{"pane_id":"pG","tab_id":"tD","workspace_id":"w1","label":"%s","cwd":"%s"}]}}\n' "$label" "$cwd"
      fi
    elif [ -f "$HERDR_FAKE_DIR/identity" ]; then
      identity=$(cat "$HERDR_FAKE_DIR/identity")
      printf '{"id":"fake","result":{"panes":[{"pane_id":"p1","tab_id":"t1","metadata":{"sources":[{"tokens":{"herdr-source-control":"%s"}}]}}]}}\n' "$identity"
    else
      printf '{"id":"fake","result":{"panes":[]}}\n'
    fi
    ;;
  "pane process-info --pane")
    mode=live
    [ ! -f "$HERDR_FAKE_DIR/process" ] || mode=$(cat "$HERDR_FAKE_DIR/process")
    case "$mode" in
      fail) printf 'inspection unavailable\n' >&2; exit 1 ;;
      empty) printf '{"result":{"process_info":{"foreground_processes":[]}}}\n' ;;
      stale) printf '{"result":{"process_info":{"foreground_processes":[{"name":"bash","argv":["/usr/bin/bash"]}]}}}\n' ;;
      *) printf '{"result":{"process_info":{"foreground_processes":[{"name":"herdr-source-control","argv":["/plugin/herdr-source-control","tui"]}]}}}\n' ;;
    esac
    ;;
  "plugin pane open")
    [ ! -f "$HERDR_FAKE_DIR/open-delay" ] || sleep 0.1
    printf '{"id":"fake","result":{"pane_id":"p1","tab_id":"t1"}}\n'
    ;;
  "pane report-metadata "*)
    if [ -f "$HERDR_FAKE_DIR/metadata-fail" ]; then
      printf 'metadata failed\n' >&2
      exit 1
    fi
    token=
    previous=
    for argument in "$@"; do
      if [ "$previous" = "--token" ]; then token=$argument; fi
      previous=$argument
    done
    printf '%s' "${token#*=}" > "$HERDR_FAKE_DIR/identity"
    ;;
esac
`

func newFakeLauncher(t *testing.T) (*Launcher, string) {
	t.Helper()
	directory := t.TempDir()
	binary := filepath.Join(directory, "herdr")
	if err := os.WriteFile(binary, []byte(fakeHerdrScript), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HERDR_FAKE_DIR", directory)
	root := filepath.Join(directory, "repo")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	launcher := &Launcher{
		Client:      NewClient(binary),
		StateDir:    filepath.Join(directory, "state"),
		PWD:         root,
		ContextJSON: `{"workspace_id":"w1"}`,
		RepositoryRoot: func(context.Context, string) (string, bool, error) {
			return root, true, nil
		},
	}
	return launcher, directory
}

func calls(t *testing.T, directory string) []string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(directory, "calls"))
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSpace(string(data)), "\n")
}

func TestLauncherOpensReportsRenamesAndFocusesExistingPane(t *testing.T) {
	launcher, directory := newFakeLauncher(t)
	first, err := launcher.Open(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !first.Opened || first.PaneID != "p1" || first.TabID != "t1" {
		t.Fatalf("first Open() = %#v", first)
	}
	got := calls(t, directory)
	if len(got) != 4 {
		t.Fatalf("calls = %#v", got)
	}
	if !strings.Contains(got[1], "--placement tab --workspace w1 --cwd "+first.Target.Root) ||
		!strings.Contains(got[1], "--env HERDR_SOURCE_CONTROL_ROOT="+first.Target.Root) ||
		!strings.Contains(got[1], "--env HERDR_SOURCE_CONTROL_ID="+first.Target.Identity) ||
		!strings.HasSuffix(got[1], "--focus") {
		t.Fatalf("open argv = %q", got[1])
	}
	if got[2] != "pane report-metadata p1 --source herdr-source-control --token herdr-source-control="+first.Target.Identity {
		t.Fatalf("metadata argv = %q", got[2])
	}
	if got[3] != "tab rename t1 Source Control" {
		t.Fatalf("rename argv = %q", got[3])
	}

	second, err := launcher.Open(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if second.Opened {
		t.Fatalf("second Open() = %#v", second)
	}
	got = calls(t, directory)
	if got[len(got)-2] != "pane process-info --pane p1" || got[len(got)-1] != "plugin pane focus p1" {
		t.Fatalf("existing pane calls = %#v", got)
	}
}

func TestLauncherClosesStalePaneAndReplacesIt(t *testing.T) {
	launcher, directory := newFakeLauncher(t)
	target, err := ResolveTarget(context.Background(), launcher.PWD, launcher.RepositoryRoot)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "identity"), []byte(target.Identity), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "process"), []byte("stale"), 0o600); err != nil {
		t.Fatal(err)
	}

	result, err := launcher.Open(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !result.Opened {
		t.Fatalf("Open() = %#v", result)
	}
	joined := strings.Join(calls(t, directory), "\n")
	want := "pane process-info --pane p1\nplugin pane close p1\npane list --workspace w1\nplugin pane open"
	if !strings.Contains(joined, want) {
		t.Fatalf("calls did not contain stale replacement sequence:\n%s", joined)
	}
}

func TestLauncherIndeterminateProcessInfoOnlyFocuses(t *testing.T) {
	for _, mode := range []string{"fail", "empty"} {
		t.Run(mode, func(t *testing.T) {
			launcher, directory := newFakeLauncher(t)
			target, err := ResolveTarget(context.Background(), launcher.PWD, launcher.RepositoryRoot)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(directory, "identity"), []byte(target.Identity), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(directory, "process"), []byte(mode), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := launcher.Open(context.Background()); err != nil {
				t.Fatal(err)
			}
			joined := strings.Join(calls(t, directory), "\n")
			if strings.Contains(joined, "plugin pane close") || !strings.Contains(joined, "plugin pane focus p1") {
				t.Fatalf("calls =\n%s", joined)
			}
		})
	}
}

func TestLauncherMetadataFailureClosesNewPane(t *testing.T) {
	launcher, directory := newFakeLauncher(t)
	if err := os.WriteFile(filepath.Join(directory, "metadata-fail"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := launcher.Open(context.Background()); err == nil || !strings.Contains(err.Error(), "report pane identity") {
		t.Fatalf("Open() error = %v", err)
	}
	joined := strings.Join(calls(t, directory), "\n")
	if !strings.Contains(joined, "pane report-metadata") || !strings.HasSuffix(joined, "plugin pane close p1") {
		t.Fatalf("calls =\n%s", joined)
	}
}

func TestConcurrentLauncherOpensAreSerialized(t *testing.T) {
	launcher, directory := newFakeLauncher(t)
	if err := os.WriteFile(filepath.Join(directory, "open-delay"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	second := *launcher
	results := make(chan OpenResult, 2)
	errors := make(chan error, 2)
	var start sync.WaitGroup
	start.Add(2)
	for _, current := range []*Launcher{launcher, &second} {
		go func(current *Launcher) {
			start.Done()
			start.Wait()
			result, err := current.Open(context.Background())
			results <- result
			errors <- err
		}(current)
	}
	for range 2 {
		if err := <-errors; err != nil {
			t.Fatal(err)
		}
	}
	opened := 0
	for range 2 {
		if (<-results).Opened {
			opened++
		}
	}
	if opened != 1 {
		t.Fatalf("opened count = %d, want 1", opened)
	}
	allCalls := calls(t, directory)
	openCalls := 0
	for _, call := range allCalls {
		if strings.HasPrefix(call, "plugin pane open ") {
			openCalls++
		}
	}
	if openCalls != 1 {
		t.Fatalf("open calls = %d; calls = %s", openCalls, fmt.Sprint(allCalls))
	}
}

// restore makes the fake herdr report a single snapshot-restored pane: labelled like our tab,
// sitting in cwd, and carrying no identity token.
func restore(t *testing.T, directory, cwd, process string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(directory, "restored"), []byte(cwd), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "process"), []byte(process), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestLauncherClosesLeftoverRestoredDuplicate(t *testing.T) {
	launcher, directory := newFakeLauncher(t)
	t.Setenv("HERDR_PLUGIN_ROOT", filepath.Join(directory, "plugin"))
	restore(t, directory, launcher.PWD, "stale")
	if err := os.WriteFile(filepath.Join(directory, "restored-extra"), nil, 0o600); err != nil {
		t.Fatal(err)
	}

	result, err := launcher.Open(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.PaneID != "pG" {
		t.Fatalf("Open() reclaimed %#v, want the first restored pane", result)
	}
	joined := strings.Join(calls(t, directory), "\n")
	if !strings.Contains(joined, "pane close pH") {
		t.Fatalf("leftover restored duplicate was not closed:\n%s", joined)
	}
	if strings.Contains(joined, "pane close pG") {
		t.Fatalf("reclaimed pane must not be closed:\n%s", joined)
	}
}

func TestLauncherReclaimsRestoredPane(t *testing.T) {
	launcher, directory := newFakeLauncher(t)
	pluginRoot := filepath.Join(directory, "plugin")
	t.Setenv("HERDR_PLUGIN_ROOT", pluginRoot)
	restore(t, directory, launcher.PWD, "stale")

	result, err := launcher.Open(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.Opened || result.PaneID != "pG" || result.TabID != "tD" {
		t.Fatalf("Open() = %#v", result)
	}
	got := calls(t, directory)
	if len(got) != 6 {
		t.Fatalf("calls = %#v", got)
	}
	if got[1] != "pane process-info --pane pG" {
		t.Fatalf("process-info argv = %q", got[1])
	}
	binary := filepath.Join(pluginRoot, "bin", "herdr-source-control")
	want := "pane run pG env HERDR_SOURCE_CONTROL_ROOT=" + result.Target.Root +
		" HERDR_SOURCE_CONTROL_ID=" + result.Target.Identity +
		" HERDR_PANE_ID=pG " + binary + " tui"
	if got[2] != want {
		t.Fatalf("run argv = %q, want %q", got[2], want)
	}
	if got[3] != "pane report-metadata pG --source herdr-source-control --token herdr-source-control="+result.Target.Identity {
		t.Fatalf("metadata argv = %q", got[3])
	}
	if got[4] != "tab rename tD Source Control" || got[5] != "tab focus tD" {
		t.Fatalf("rename/focus argv = %#v", got[4:])
	}
	if strings.Contains(strings.Join(got, "\n"), "plugin pane open") {
		t.Fatalf("reclaim still opened a second tab:\n%s", strings.Join(got, "\n"))
	}
}

func TestLauncherReclaimFallsBackToExecutableWithoutPluginRoot(t *testing.T) {
	launcher, directory := newFakeLauncher(t)
	t.Setenv("HERDR_PLUGIN_ROOT", "")
	restore(t, directory, launcher.PWD, "stale")

	if _, err := launcher.Open(context.Background()); err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if got := calls(t, directory)[2]; !strings.HasSuffix(got, " "+executable+" tui") {
		t.Fatalf("run argv = %q, want binary %q", got, executable)
	}
}

func TestLauncherDoesNotReclaimPaneRunningOurBinary(t *testing.T) {
	t.Run("restored shape", func(t *testing.T) {
		launcher, directory := newFakeLauncher(t)
		restore(t, directory, launcher.PWD, "live")

		result, err := launcher.Open(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if !result.Opened {
			t.Fatalf("Open() = %#v", result)
		}
		joined := strings.Join(calls(t, directory), "\n")
		if strings.Contains(joined, "pane run") {
			t.Fatalf("clobbered a pane running our binary:\n%s", joined)
		}
	})

	t.Run("identified pane", func(t *testing.T) {
		launcher, directory := newFakeLauncher(t)
		target, err := ResolveTarget(context.Background(), launcher.PWD, launcher.RepositoryRoot)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(directory, "identity"), []byte(target.Identity), 0o600); err != nil {
			t.Fatal(err)
		}

		result, err := launcher.Open(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if result.Opened {
			t.Fatalf("Open() = %#v", result)
		}
		joined := strings.Join(calls(t, directory), "\n")
		if strings.Contains(joined, "pane run") || !strings.Contains(joined, "plugin pane focus p1") {
			t.Fatalf("calls =\n%s", joined)
		}
	})
}

func TestLauncherDoesNotReclaimPaneInAnotherDirectory(t *testing.T) {
	launcher, directory := newFakeLauncher(t)
	restore(t, directory, filepath.Join(directory, "other-repo"), "stale")

	result, err := launcher.Open(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !result.Opened {
		t.Fatalf("Open() = %#v", result)
	}
	joined := strings.Join(calls(t, directory), "\n")
	if strings.Contains(joined, "pane run") || strings.Contains(joined, "pane process-info") {
		t.Fatalf("inspected or reclaimed an unrelated tab:\n%s", joined)
	}
}

func TestLauncherDoesNotReclaimWhenProcessInfoIsIndeterminate(t *testing.T) {
	for _, mode := range []string{"fail", "empty"} {
		t.Run(mode, func(t *testing.T) {
			launcher, directory := newFakeLauncher(t)
			restore(t, directory, launcher.PWD, mode)

			result, err := launcher.Open(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if !result.Opened {
				t.Fatalf("Open() = %#v", result)
			}
			joined := strings.Join(calls(t, directory), "\n")
			if strings.Contains(joined, "pane run") {
				t.Fatalf("reclaimed a pane with indeterminate process info:\n%s", joined)
			}
		})
	}
}

func TestLauncherOpensNewPaneWhenNoReclaimCandidate(t *testing.T) {
	launcher, directory := newFakeLauncher(t)
	restore(t, directory, launcher.PWD, "stale")
	if err := os.WriteFile(filepath.Join(directory, "restored-label"), []byte("Notes"), 0o600); err != nil {
		t.Fatal(err)
	}

	result, err := launcher.Open(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !result.Opened || result.PaneID != "p1" {
		t.Fatalf("Open() = %#v", result)
	}
	joined := strings.Join(calls(t, directory), "\n")
	if strings.Contains(joined, "pane run") || !strings.Contains(joined, "plugin pane open") {
		t.Fatalf("calls =\n%s", joined)
	}
}

func TestLauncherRequiresWorkspaceID(t *testing.T) {
	launcher, _ := newFakeLauncher(t)
	launcher.ContextJSON = `{}`
	if _, err := launcher.Open(context.Background()); err == nil || !strings.Contains(err.Error(), "workspace_id") {
		t.Fatalf("Open() error = %v", err)
	}
}

func TestMetadataRefreshBestEffortIgnoresFailure(t *testing.T) {
	client := &Client{Runner: runnerFunc(func(context.Context, string, ...string) ([]byte, error) {
		return nil, fmt.Errorf("unavailable")
	})}
	RefreshMetadataBestEffort(context.Background(), client, "p1", "identity")
}
