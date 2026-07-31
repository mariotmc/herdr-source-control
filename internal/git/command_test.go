package git

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCommandEnvironmentOverridesPromptsAndScopesOptionalLocks(t *testing.T) {
	t.Setenv("GIT_ASKPASS", "/bad")
	t.Setenv("SSH_ASKPASS", "/bad")
	t.Setenv("LC_ALL", "bad")
	t.Setenv("GIT_OPTIONAL_LOCKS", "inherited")
	readEnv := strings.Join(commandEnvironment(true), "\n")
	for _, expected := range []string{"GIT_ASKPASS=/bin/false", "SSH_ASKPASS=/bin/false", "SSH_ASKPASS_REQUIRE=never", "GIT_TERMINAL_PROMPT=0", "GCM_INTERACTIVE=Never", "LC_ALL=C", "GIT_OPTIONAL_LOCKS=0"} {
		if !strings.Contains(readEnv, expected) {
			t.Errorf("read environment missing %q", expected)
		}
	}
	mutationEnv := strings.Join(commandEnvironment(false), "\n")
	if !strings.Contains(mutationEnv, "GIT_OPTIONAL_LOCKS=inherited") || strings.Contains(mutationEnv, "GIT_OPTIONAL_LOCKS=0") {
		t.Fatalf("mutation optional locks environment = %s", mutationEnv)
	}
}

func TestCancellableCommandTerminatesProcessSession(t *testing.T) {
	script := filepath.Join(t.TempDir(), "fake-git")
	if err := os.WriteFile(script, []byte("#!/bin/sh\ntrap 'exit 0' TERM\nsleep 30 &\nwait\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	started := time.Now()
	result := (runner{path: script}).cancellable(ctx, time.Minute, t.TempDir(), true, "status")
	if !errors.Is(result.err, errCancelled) {
		t.Fatalf("error = %v", result.err)
	}
	if time.Since(started) > 3*time.Second {
		t.Fatalf("cancellation took %v", time.Since(started))
	}
}

func TestMutationIgnoresCancellationAfterSpawn(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "fake-git")
	marker := filepath.Join(dir, "done")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nsleep 0.1\nprintf done > \"$MARKER\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MARKER", marker)
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()
	if err := (runner{path: script}).mutation(ctx, dir, "push", "push"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("mutation did not finish: %v", err)
	}
}

func TestSanitizeDetailCapsEscapesAndRedacts(t *testing.T) {
	input := append([]byte("fatal: https://user:secret@example.com/\n\x1b[31m"), 0xff)
	got := sanitizeDetail(input)
	if strings.Contains(got, "secret") || strings.ContainsRune(got, '\x1b') || !strings.Contains(got, `<redacted>@example.com`) || !strings.Contains(got, `\xFF`) {
		t.Fatalf("sanitized detail = %q", got)
	}
	long := sanitizeDetail([]byte(strings.Repeat("x", stderrLimit+100)))
	if len(long) != stderrLimit {
		t.Fatalf("sanitized length = %d", len(long))
	}
}

func TestParseVersion(t *testing.T) {
	for _, tt := range []struct {
		output       string
		major, minor int
	}{
		{"git version 2.31.0\n", 2, 31},
		{"git version 2.43.0.windows.1\r\n", 2, 43},
		{"git version 3.0.0.rc1\n", 3, 0},
	} {
		major, minor, err := parseVersion([]byte(tt.output))
		if err != nil || major != tt.major || minor != tt.minor {
			t.Errorf("parseVersion(%q) = %d, %d, %v", tt.output, major, minor, err)
		}
	}
	if _, _, err := parseVersion([]byte("not git")); err == nil {
		t.Fatal("invalid version parsed")
	}
}
