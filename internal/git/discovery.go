package git

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
)

type Discovery struct {
	Root          string
	CanonicalRoot string
	GitDir        string
	CommonDir     string
}

func Discover(ctx context.Context, startDir string) (Discovery, error) {
	r, err := newRunner()
	if err != nil {
		return Discovery{}, err
	}
	if err := r.checkVersion(ctx, startDir); err != nil {
		return Discovery{}, err
	}
	return r.discover(ctx, startDir)
}

func (r runner) checkVersion(ctx context.Context, dir string) error {
	result := r.cancellable(ctx, versionDiscoveryTimeout, dir, true, "--version")
	if result.err != nil {
		return commandError("version", "", result)
	}
	major, minor, err := parseVersion(result.stdout)
	if err != nil {
		return parseError(err)
	}
	if major < 2 || major == 2 && minor < 31 {
		return &OperationError{Kind: ErrorGitTooOld, Operation: "version", ExitCode: 0}
	}
	return nil
}

func (r runner) discover(ctx context.Context, startDir string) (Discovery, error) {
	abs, err := filepath.Abs(startDir)
	if err != nil {
		return Discovery{}, &OperationError{Kind: ErrorNotRepository, Operation: "discovery", ExitCode: -1, Err: err}
	}
	bare := r.cancellable(ctx, versionDiscoveryTimeout, abs, true, "rev-parse", "--is-bare-repository")
	if bare.err == nil && string(removeOneLineEnding(bare.stdout)) == "true" {
		return Discovery{}, &OperationError{Kind: ErrorBareRepository, Operation: "discovery", ExitCode: 0}
	}
	inside := r.cancellable(ctx, versionDiscoveryTimeout, abs, true, "rev-parse", "--is-inside-work-tree")
	if inside.err != nil || string(removeOneLineEnding(inside.stdout)) != "true" {
		if errors.Is(inside.err, errTimedOut) || errors.Is(inside.err, errCancelled) {
			return Discovery{}, commandError("discovery", "", inside)
		}
		detail := sanitizeDetail(inside.stderr)
		if strings.Contains(strings.ToLower(detail), "dubious ownership") || strings.Contains(detail, "safe.directory") {
			return Discovery{}, &OperationError{Kind: ErrorCommand, Operation: "discovery", ExitCode: inside.exitCode, Stderr: detail, Err: inside.err}
		}
		return Discovery{}, &OperationError{Kind: ErrorNotRepository, Operation: "discovery", ExitCode: inside.exitCode, Stderr: detail, Err: inside.err}
	}

	query := func(arg string) (string, error) {
		result := r.cancellable(ctx, versionDiscoveryTimeout, abs, true, "rev-parse", "--path-format=absolute", arg)
		if result.err != nil {
			return "", commandError("discovery", arg, result)
		}
		return string(removeOneLineEnding(result.stdout)), nil
	}
	root, err := query("--show-toplevel")
	if err != nil {
		return Discovery{}, err
	}
	gitDir, err := query("--git-dir")
	if err != nil {
		return Discovery{}, err
	}
	commonDir, err := query("--git-common-dir")
	if err != nil {
		return Discovery{}, err
	}
	canonical := root
	if evaluated, evalErr := filepath.EvalSymlinks(root); evalErr == nil {
		canonical = evaluated
	}
	return Discovery{Root: root, CanonicalRoot: canonical, GitDir: gitDir, CommonDir: commonDir}, nil
}

func removeOneLineEnding(value []byte) []byte {
	if len(value) >= 2 && value[len(value)-2] == '\r' && value[len(value)-1] == '\n' {
		return value[:len(value)-2]
	}
	if len(value) >= 1 && value[len(value)-1] == '\n' {
		return value[:len(value)-1]
	}
	return value
}
