package git

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/mariotmc/herdr-source-control/internal/domain"
)

type Repository struct {
	runner   runner
	startDir string

	branchMu         sync.Mutex
	branchGeneration uint64
	branches         map[string]domain.LocalBranch
}

var _ domain.Repository = (*Repository)(nil)

func New(ctx context.Context, startDir string) (*Repository, error) {
	r, err := newRunner()
	if err != nil {
		return nil, err
	}
	if err := r.checkVersion(ctx, startDir); err != nil {
		return nil, err
	}
	return &Repository{runner: r, startDir: startDir}, nil
}

func (r *Repository) Snapshot(ctx context.Context) (domain.Snapshot, error) {
	discovery, err := r.runner.discover(ctx, r.startDir)
	if err != nil {
		return domain.Snapshot{}, err
	}
	result := r.runner.cancellable(ctx, readTimeout, discovery.Root, true, "status", "--porcelain=v2", "--branch", "-z", "--untracked-files=all", "--renames", "--ahead-behind")
	if result.err != nil {
		return domain.Snapshot{}, commandError("status", "", result)
	}
	parsed, err := parsePorcelain(result.stdout)
	if err != nil {
		return domain.Snapshot{}, parseError(err)
	}
	if err := r.completeBranch(ctx, discovery.Root, &parsed); err != nil {
		return domain.Snapshot{}, err
	}
	return domain.Snapshot{
		Root: discovery.Root, CanonicalRoot: discovery.CanonicalRoot, GitDir: discovery.GitDir, CommonDir: discovery.CommonDir,
		Branch: parsed.branch, Operation: operationState(discovery.GitDir), Changes: parsed.changes, CapturedAt: time.Now(),
	}, nil
}

func (r *Repository) completeBranch(ctx context.Context, root string, parsed *statusResult) error {
	if parsed.branch.State == domain.HeadDetached {
		return r.verifyHead(ctx, root, parsed.branch)
	}
	if parsed.branch.Name == "" {
		return parseError(errors.New("attached head has no branch name"))
	}
	first, err := r.exactUpstream(ctx, root, parsed.branch.Name)
	if err != nil {
		return err
	}
	if first.short != parsed.statusUpstream {
		return parseError(errors.New("status and exact upstream differ"))
	}
	parsed.branch.Upstream, parsed.branch.UpstreamRef = first.short, first.full
	parsed.branch.RemoteName, parsed.branch.RemoteRef = first.remote, first.remoteRef
	if first.full != "" && parsed.branch.State != domain.HeadUnborn {
		counts := r.runner.cancellable(ctx, readTimeout, root, true, "rev-list", "--left-right", "--count", "HEAD..."+first.full)
		if counts.err == nil {
			parsed.branch.Ahead, parsed.branch.Behind, err = parseCount(counts.stdout)
			if err != nil {
				return parseError(err)
			}
			parsed.branch.CountsKnown = true
		} else if counts.exitCode != 128 {
			return commandError("count upstream", "", counts)
		}
	}
	second, err := r.exactUpstream(ctx, root, parsed.branch.Name)
	if err != nil {
		return err
	}
	if first != second {
		return parseError(errors.New("upstream changed during snapshot"))
	}
	return r.verifyHead(ctx, root, parsed.branch)
}

func (r *Repository) verifyHead(ctx context.Context, root string, expected domain.BranchState) error {
	if expected.State != domain.HeadDetached {
		symbolic := r.runner.cancellable(ctx, readTimeout, root, true, "symbolic-ref", "--quiet", "--short", "HEAD")
		if symbolic.err != nil || string(removeOneLineEnding(symbolic.stdout)) != expected.Name {
			return parseError(errors.New("head branch changed during snapshot"))
		}
	}
	oid := r.runner.cancellable(ctx, readTimeout, root, true, "rev-parse", "--verify", "HEAD")
	if expected.State == domain.HeadUnborn {
		if oid.err == nil {
			return parseError(errors.New("unborn head gained a commit during snapshot"))
		}
		return nil
	}
	if oid.err != nil || string(removeOneLineEnding(oid.stdout)) != expected.OID {
		return parseError(errors.New("head commit changed during snapshot"))
	}
	return nil
}

func operationState(gitDir string) domain.OperationState {
	exists := func(name string) bool {
		_, err := os.Stat(name)
		return err == nil
	}
	return domain.OperationState{
		Merge: exists(gitDir + "/MERGE_HEAD"), Rebase: exists(gitDir+"/rebase-merge") || exists(gitDir+"/rebase-apply"),
		CherryPick: exists(gitDir + "/CHERRY_PICK_HEAD"), Revert: exists(gitDir + "/REVERT_HEAD"),
	}
}

func (r *Repository) ValidateBranch(ctx context.Context, name string) error {
	if name == "" {
		return &OperationError{Kind: ErrorValidation, Operation: "validate branch", ExitCode: -1, Stderr: "Enter a branch name."}
	}
	discovery, err := r.runner.discover(ctx, r.startDir)
	if err != nil {
		return err
	}
	check := r.runner.cancellable(ctx, readTimeout, discovery.Root, true, "check-ref-format", "--branch", name)
	if check.err != nil {
		return &OperationError{Kind: ErrorValidation, Operation: "validate branch", ExitCode: check.exitCode, Stderr: "Invalid branch name.", Err: check.err}
	}
	exists := r.runner.cancellable(ctx, readTimeout, discovery.Root, true, "show-ref", "--verify", "--quiet", "refs/heads/"+name)
	if exists.err == nil {
		return &OperationError{Kind: ErrorValidation, Operation: "validate branch", ExitCode: 0, Stderr: `Branch "` + sanitizeDetail([]byte(name)) + `" already exists.`}
	}
	if exists.exitCode != 1 {
		return commandError("validate branch", "existence", exists)
	}
	return nil
}

func (r *Repository) Checkout(ctx context.Context, name string) error {
	r.branchMu.Lock()
	_, wasListed := r.branches[name]
	r.branchMu.Unlock()
	if !wasListed {
		return &OperationError{Kind: ErrorValidation, Operation: "checkout", ExitCode: -1, Stderr: "Select a branch from the current branch list."}
	}
	branches, err := r.LocalBranches(ctx)
	if err != nil {
		return err
	}
	for _, branch := range branches {
		if branch.Name != name {
			continue
		}
		if branch.Current {
			return &OperationError{Kind: ErrorValidation, Operation: "checkout", ExitCode: -1, Stderr: `Already on branch "` + sanitizeDetail([]byte(name)) + `".`}
		}
		if branch.WorktreePath != "" {
			return &OperationError{Kind: ErrorWorktreeConflict, Operation: "checkout", ExitCode: -1, Stderr: sanitizeDetail([]byte(branch.WorktreePath))}
		}
		discovery, err := r.runner.discover(ctx, r.startDir)
		if err != nil {
			return err
		}
		return r.runner.mutation(ctx, discovery.Root, "checkout", "switch", "--no-guess", "--no-recurse-submodules", "--", name)
	}
	return &OperationError{Kind: ErrorValidation, Operation: "checkout", ExitCode: -1, Stderr: "Branch no longer exists."}
}

func (r *Repository) CreateBranch(ctx context.Context, name string) error {
	if err := r.ValidateBranch(ctx, name); err != nil {
		return err
	}
	discovery, err := r.runner.discover(ctx, r.startDir)
	if err != nil {
		return err
	}
	head := r.runner.cancellable(ctx, readTimeout, discovery.Root, true, "rev-parse", "--verify", "HEAD")
	if head.err != nil {
		return &OperationError{Kind: ErrorValidation, Operation: "create branch", ExitCode: head.exitCode, Stderr: "Create the first commit before creating another branch.", Err: head.err}
	}
	return r.runner.mutation(ctx, discovery.Root, "create branch", "switch", "--no-recurse-submodules", "-c", name, "--")
}

func branchTuple(branch domain.BranchState) upstreamTuple {
	return upstreamTuple{full: branch.UpstreamRef, short: branch.Upstream, remote: branch.RemoteName, remoteRef: branch.RemoteRef}
}

func validateBranchSync(branch domain.BranchState) error {
	if branch.State != domain.HeadAttached || branch.Name == "" || branch.OID == "" || !branch.CountsKnown {
		return &OperationError{Kind: ErrorNoUpstream, Operation: "sync", ExitCode: -1}
	}
	return validateRemoteTuple(branchTuple(branch))
}

func isTrue(value []byte) bool { return strings.EqualFold(string(removeOneLineEnding(value)), "true") }
