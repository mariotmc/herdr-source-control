package git

import (
	"context"
	"encoding/hex"
	"strings"

	"github.com/mariotmc/herdr-source-control/internal/domain"
)

func (r *Repository) checkSync(ctx context.Context, branch domain.BranchState) (Discovery, error) {
	if err := validateBranchSync(branch); err != nil {
		return Discovery{}, err
	}
	discovery, err := r.runner.discover(ctx, r.startDir)
	if err != nil {
		return Discovery{}, err
	}
	remoteCheck := r.runner.cancellable(ctx, readTimeout, discovery.Root, true, "check-ref-format", "refs/remotes/"+branch.RemoteName+"/probe")
	if remoteCheck.err != nil {
		return Discovery{}, &OperationError{Kind: ErrorNoUpstream, Operation: "sync", ExitCode: remoteCheck.exitCode, Err: remoteCheck.err}
	}
	mirror := r.runner.cancellable(ctx, readTimeout, discovery.Root, true, "config", "--bool", "--get", "remote."+branch.RemoteName+".mirror")
	if mirror.err == nil && isTrue(mirror.stdout) {
		return Discovery{}, &OperationError{Kind: ErrorValidation, Operation: "sync", ExitCode: 0, Stderr: "Sync is unavailable for mirror remotes."}
	}
	if mirror.err != nil && mirror.exitCode != 1 {
		return Discovery{}, commandError("sync", "check mirror remote", mirror)
	}
	return discovery, nil
}

func (r *Repository) Fetch(ctx context.Context, branch domain.BranchState) error {
	discovery, err := r.checkSync(ctx, branch)
	if err != nil {
		return err
	}
	refspec := "+" + branch.RemoteRef + ":" + branch.UpstreamRef
	result := r.runner.cancellable(ctx, fetchTimeout, discovery.Root, false, "fetch", "--no-tags", "--no-prune", "--no-prune-tags", "--no-recurse-submodules", "--", branch.RemoteName, refspec)
	if result.err != nil {
		return commandError("fetch", "network", result)
	}
	return nil
}

func (r *Repository) FastForward(ctx context.Context, branch domain.BranchState) error {
	discovery, err := r.checkSync(ctx, branch)
	if err != nil {
		return err
	}
	return r.runner.mutation(ctx, discovery.Root, "fast-forward", "merge", "--ff-only", "--no-autostash", "--", branch.UpstreamRef)
}

func (r *Repository) Push(ctx context.Context, branch domain.BranchState, oid string) error {
	if oid != branch.OID || (len(oid) != 40 && len(oid) != 64) || strings.ToLower(oid) != oid {
		return &OperationError{Kind: ErrorValidation, Operation: "push", ExitCode: -1, Stderr: "Push requires a verified full commit object ID."}
	}
	if _, err := hex.DecodeString(oid); err != nil {
		return &OperationError{Kind: ErrorValidation, Operation: "push", ExitCode: -1, Stderr: "Push requires a verified full commit object ID.", Err: err}
	}
	discovery, err := r.checkSync(ctx, branch)
	if err != nil {
		return err
	}
	return r.runner.mutation(ctx, discovery.Root, "push", "push", "--porcelain", "--no-follow-tags", "--recurse-submodules=no", "--", branch.RemoteName, oid+":"+branch.RemoteRef)
}
