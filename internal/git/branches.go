package git

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/mariotmc/herdr-source-control/internal/domain"
)

const branchFormat = "%(refname:strip=2)%00%(HEAD)%00%(worktreepath)%00%(objectname)%00%(authorname)%00%(committerdate:unix)%00%(subject)%00%00"
const upstreamFormat = "%(upstream)%00%(upstream:short)%00%(upstream:remotename)%00%(upstream:remoteref)%00%00"

type upstreamTuple struct {
	full, short, remote, remoteRef string
}

func parseFramedRecords(data []byte, fieldCount int) ([][][]byte, error) {
	if len(data) == 0 {
		return nil, nil
	}
	var records [][][]byte
	for len(data) > 0 {
		end := bytes.Index(data, []byte{0, 0, '\n'})
		if end < 0 {
			return nil, errors.New("unterminated ref record")
		}
		fields := bytes.Split(data[:end], []byte{0})
		if len(fields) != fieldCount {
			return nil, fmt.Errorf("ref record has %d fields, want %d", len(fields), fieldCount)
		}
		record := make([][]byte, len(fields))
		for i := range fields {
			record[i] = bytes.Clone(fields[i])
		}
		records = append(records, record)
		data = data[end+3:]
	}
	return records, nil
}

func parseBranches(data []byte) ([]domain.LocalBranch, error) {
	records, err := parseFramedRecords(data, 7)
	if err != nil {
		return nil, err
	}
	branches := make([]domain.LocalBranch, 0, len(records))
	for _, fields := range records {
		head := string(fields[1])
		if len(fields[0]) == 0 || len(fields[3]) == 0 || head != "" && head != " " && head != "*" {
			return nil, errors.New("invalid branch metadata")
		}
		seconds, err := strconv.ParseInt(string(fields[5]), 10, 64)
		if err != nil {
			return nil, fmt.Errorf("branch commit time: %w", err)
		}
		branches = append(branches, domain.LocalBranch{
			Name: string(fields[0]), Current: head == "*", WorktreePath: string(fields[2]),
			OID: string(fields[3]), Author: sanitizeDetail(fields[4]), CommitTime: time.Unix(seconds, 0), Subject: sanitizeDetail(fields[6]),
		})
	}
	for i := range branches {
		if branches[i].Current && i != 0 {
			current := branches[i]
			copy(branches[1:i+1], branches[0:i])
			branches[0] = current
			break
		}
	}
	return branches, nil
}

func parseUpstream(data []byte) (upstreamTuple, error) {
	records, err := parseFramedRecords(data, 4)
	if err != nil {
		return upstreamTuple{}, err
	}
	if len(records) == 0 {
		return upstreamTuple{}, nil
	}
	if len(records) != 1 {
		return upstreamTuple{}, fmt.Errorf("upstream query returned %d records", len(records))
	}
	return upstreamTuple{full: string(records[0][0]), short: string(records[0][1]), remote: string(records[0][2]), remoteRef: string(records[0][3])}, nil
}

func (r *Repository) LocalBranches(ctx context.Context) ([]domain.LocalBranch, error) {
	discovery, err := r.runner.discover(ctx, r.startDir)
	if err != nil {
		return nil, err
	}
	result := r.runner.cancellable(ctx, readTimeout, discovery.Root, true, "for-each-ref", "--format="+branchFormat, "--sort=refname", "--sort=-committerdate", "refs/heads/")
	if result.err != nil {
		return nil, commandError("list branches", "", result)
	}
	branches, err := parseBranches(result.stdout)
	if err != nil {
		return nil, parseError(err)
	}
	r.branchMu.Lock()
	r.branchGeneration++
	r.branches = make(map[string]domain.LocalBranch, len(branches))
	for _, branch := range branches {
		r.branches[branch.Name] = branch
	}
	r.branchMu.Unlock()
	return branches, nil
}

func (r *Repository) exactUpstream(ctx context.Context, root, branch string) (upstreamTuple, error) {
	result := r.runner.cancellable(ctx, readTimeout, root, true, "for-each-ref", "--format="+upstreamFormat, "refs/heads/"+branch)
	if result.err != nil {
		return upstreamTuple{}, commandError("resolve upstream", "", result)
	}
	tuple, err := parseUpstream(result.stdout)
	if err != nil {
		return upstreamTuple{}, parseError(err)
	}
	return tuple, nil
}

func validateRemoteTuple(tuple upstreamTuple) error {
	if tuple.full == "" || tuple.short == "" || tuple.remote == "" || tuple.remoteRef == "" || tuple.remote == "." || !strings.HasPrefix(tuple.full, "refs/remotes/") || !strings.HasPrefix(tuple.remoteRef, "refs/heads/") {
		return &OperationError{Kind: ErrorNoUpstream, Operation: "sync", ExitCode: -1}
	}
	if strings.HasPrefix(tuple.remote, "-") || strings.ContainsRune(tuple.remote, 0) {
		return &OperationError{Kind: ErrorNoUpstream, Operation: "sync", ExitCode: -1}
	}
	return nil
}
