package git

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const (
	versionDiscoveryTimeout = 5 * time.Second
	readTimeout             = 30 * time.Second
	fetchTimeout            = 5 * time.Minute
	terminationGrace        = 2 * time.Second
	stderrLimit             = 8 * 1024
)

var (
	errTimedOut  = errors.New("git command timed out")
	errCancelled = errors.New("git command cancelled")
)

type commandResult struct {
	stdout   []byte
	stderr   []byte
	err      error
	exitCode int
}

type runner struct {
	path string
}

func newRunner() (runner, error) {
	path, err := exec.LookPath("git")
	if err != nil {
		return runner{}, &OperationError{Kind: ErrorGitUnavailable, Operation: "version", ExitCode: -1, Err: err}
	}
	return runner{path: path}, nil
}

func commandEnvironment(readOnly bool) []string {
	overrides := map[string]string{
		"LC_ALL": "C", "LANG": "C", "LANGUAGE": "C", "GIT_PAGER": "cat",
		"GIT_TERMINAL_PROMPT": "0", "GCM_INTERACTIVE": "Never", "GIT_ASKPASS": "/bin/false",
		"SSH_ASKPASS": "/bin/false", "SSH_ASKPASS_REQUIRE": "never",
	}
	if readOnly {
		overrides["GIT_OPTIONAL_LOCKS"] = "0"
	}
	env := make([]string, 0, len(os.Environ())+len(overrides))
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if _, replaced := overrides[key]; !replaced {
			env = append(env, entry)
		}
	}
	for key, value := range overrides {
		env = append(env, key+"="+value)
	}
	return env
}

func (r runner) cancellable(ctx context.Context, timeout time.Duration, dir string, readOnly bool, args ...string) commandResult {
	if err := ctx.Err(); err != nil {
		resultErr := errCancelled
		if errors.Is(err, context.DeadlineExceeded) {
			resultErr = errTimedOut
		}
		return commandResult{err: resultErr, exitCode: -1}
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.Command(r.path, args...)
	return runCommand(ctx, cmd, dir, readOnly, true)
}

func (r runner) mutation(ctx context.Context, dir string, operation string, args ...string) error {
	if err := ctx.Err(); err != nil {
		return &OperationError{Kind: ErrorCancelled, Operation: operation, ExitCode: -1, Err: err}
	}
	result := runCommand(context.Background(), exec.Command(r.path, args...), dir, false, false)
	if result.err != nil {
		return commandError(operation, "", result)
	}
	return nil
}

func runCommand(ctx context.Context, cmd *exec.Cmd, dir string, readOnly, cancellable bool) commandResult {
	cmd.Dir = dir
	cmd.Env = commandEnvironment(readOnly)
	cmd.Stdin = nil
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return commandResult{stderr: stderr.Bytes(), err: err, exitCode: -1}
	}

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	if !cancellable {
		err := <-done
		return resultFromWait(stdout.Bytes(), stderr.Bytes(), err)
	}

	select {
	case err := <-done:
		return resultFromWait(stdout.Bytes(), stderr.Bytes(), err)
	case <-ctx.Done():
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
		select {
		case <-done:
		case <-time.After(terminationGrace):
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			<-done
		}
		err := errCancelled
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			err = errTimedOut
		}
		return commandResult{stdout: stdout.Bytes(), stderr: stderr.Bytes(), err: err, exitCode: -1}
	}
}

func resultFromWait(stdout, stderr []byte, err error) commandResult {
	exitCode := 0
	if err != nil {
		exitCode = -1
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			exitCode = exitErr.ExitCode()
		}
	}
	return commandResult{stdout: stdout, stderr: stderr, err: err, exitCode: exitCode}
}

func parseVersion(output []byte) (int, int, error) {
	line := string(removeOneLineEnding(output))
	const prefix = "git version "
	if !strings.HasPrefix(line, prefix) {
		return 0, 0, errors.New("unexpected git version output")
	}
	parts := strings.Split(strings.TrimPrefix(line, prefix), ".")
	if len(parts) < 2 {
		return 0, 0, errors.New("incomplete git version")
	}
	major, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, 0, err
	}
	minorDigits := strings.TrimLeftFunc(parts[1], func(r rune) bool { return r < '0' || r > '9' })
	end := 0
	for end < len(minorDigits) && minorDigits[end] >= '0' && minorDigits[end] <= '9' {
		end++
	}
	if end == 0 {
		return 0, 0, errors.New("invalid git minor version")
	}
	minor, err := strconv.Atoi(minorDigits[:end])
	return major, minor, err
}
