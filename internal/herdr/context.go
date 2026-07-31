package herdr

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const discoveryTimeout = 5 * time.Second

type Context struct {
	WorkspaceID    string  `json:"workspace_id"`
	FocusedPaneCWD *string `json:"focused_pane_cwd"`
	WorkspaceCWD   *string `json:"workspace_cwd"`
}

func ParseContext(value string) (Context, error) {
	var context Context
	if value == "" {
		return context, errors.New("HERDR_PLUGIN_CONTEXT_JSON is empty")
	}
	if err := json.Unmarshal([]byte(value), &context); err != nil {
		return context, fmt.Errorf("parse HERDR_PLUGIN_CONTEXT_JSON: %w", err)
	}
	return context, nil
}

func (c Context) StartDir(pwd string) string {
	if c.FocusedPaneCWD != nil && *c.FocusedPaneCWD != "" {
		return *c.FocusedPaneCWD
	}
	if c.WorkspaceCWD != nil && *c.WorkspaceCWD != "" {
		return *c.WorkspaceCWD
	}
	return pwd
}

type Target struct {
	Root          string
	CanonicalRoot string
	Identity      string
	Repository    bool
}

type RepositoryRootFunc func(context.Context, string) (string, bool, error)

func ResolveTarget(ctx context.Context, startDir string, repositoryRoot RepositoryRootFunc) (Target, error) {
	absolute, err := filepath.Abs(startDir)
	if err != nil {
		return Target{}, fmt.Errorf("resolve invoking directory: %w", err)
	}
	absolute = filepath.Clean(absolute)

	root := absolute
	repository := false
	if repositoryRoot != nil {
		resolved, found, err := repositoryRoot(ctx, absolute)
		if err != nil {
			return Target{}, err
		}
		if found {
			root, err = filepath.Abs(resolved)
			if err != nil {
				return Target{}, fmt.Errorf("resolve repository root: %w", err)
			}
			root = filepath.Clean(root)
			repository = true
		}
	}

	canonical := root
	if evaluated, err := filepath.EvalSymlinks(root); err == nil {
		canonical = evaluated
	}
	sum := sha256.Sum256([]byte(canonical))
	return Target{
		Root:          root,
		CanonicalRoot: canonical,
		Identity:      hex.EncodeToString(sum[:]),
		Repository:    repository,
	}, nil
}

func SystemRepositoryRoot(ctx context.Context, startDir string) (string, bool, error) {
	ctx, cancel := context.WithTimeout(ctx, discoveryTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "git", "rev-parse", "--path-format=absolute", "--show-toplevel")
	cmd.Dir = startDir
	cmd.Stdin = nil
	cmd.Env = append(os.Environ(), "LC_ALL=C", "LANG=C", "LANGUAGE=C", "GIT_OPTIONAL_LOCKS=0")
	output, err := cmd.Output()
	if err != nil {
		var exitError *exec.ExitError
		if errors.As(err, &exitError) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("discover repository: %w", err)
	}
	root := strings.TrimSuffix(strings.TrimSuffix(string(output), "\n"), "\r")
	if root == "" {
		return "", false, errors.New("discover repository: Git returned an empty root")
	}
	return root, true, nil
}
