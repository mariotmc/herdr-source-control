package domain

import (
	"bytes"
	"context"
	"sort"
	"time"
)

type Status uint8

const (
	StatusUnmodified Status = iota
	StatusModified
	StatusAdded
	StatusDeleted
	StatusRenamed
	StatusCopied
	StatusTypeChanged
	StatusUnmerged
	StatusUnknown
)

func (s Status) Code() string {
	switch s {
	case StatusModified:
		return "M"
	case StatusAdded:
		return "A"
	case StatusDeleted:
		return "D"
	case StatusRenamed:
		return "R"
	case StatusCopied:
		return "C"
	case StatusTypeChanged:
		return "T"
	case StatusUnmerged:
		return "!"
	case StatusUnknown:
		return "?"
	default:
		return ""
	}
}

func (s Status) Label() string {
	switch s {
	case StatusModified:
		return "Modified"
	case StatusAdded:
		return "Added"
	case StatusDeleted:
		return "Deleted"
	case StatusRenamed:
		return "Renamed"
	case StatusCopied:
		return "Copied"
	case StatusTypeChanged:
		return "Type changed"
	case StatusUnmerged:
		return "Conflict"
	case StatusUnknown:
		return "Unknown"
	default:
		return ""
	}
}

type Change struct {
	Path           []byte
	OriginalPath   []byte
	IndexStatus    Status
	WorktreeStatus Status
	RawXY          [2]byte
	Submodule      string
	Score          int
	Untracked      bool
	Conflicted     bool
}

type HeadState uint8

const (
	HeadAttached HeadState = iota
	HeadDetached
	HeadUnborn
)

type BranchState struct {
	State       HeadState
	Name        string
	OID         string
	Upstream    string
	UpstreamRef string
	RemoteName  string
	RemoteRef   string
	Ahead       uint64
	Behind      uint64
	CountsKnown bool
}

type OperationState struct {
	Merge      bool
	Rebase     bool
	CherryPick bool
	Revert     bool
}

func (s OperationState) Active() bool {
	return s.Merge || s.Rebase || s.CherryPick || s.Revert
}

type Snapshot struct {
	Root          string
	CanonicalRoot string
	GitDir        string
	CommonDir     string
	Branch        BranchState
	Operation     OperationState
	Changes       []Change
	CapturedAt    time.Time
}

type LocalBranch struct {
	Name         string
	OID          string
	Subject      string
	Author       string
	CommitTime   time.Time
	Current      bool
	WorktreePath string
}

type Group uint8

const (
	GroupMerge Group = iota
	GroupStaged
	GroupChanges
)

func (g Group) Label() string {
	switch g {
	case GroupMerge:
		return "Merge Changes"
	case GroupStaged:
		return "Staged Changes"
	default:
		return "Changes"
	}
}

type Resource struct {
	Group        Group
	Status       Status
	RawStatus    byte
	Path         []byte
	OriginalPath []byte
	Untracked    bool
}

func ProjectChanges(changes []Change) map[Group][]Resource {
	groups := map[Group][]Resource{
		GroupMerge:   {},
		GroupStaged:  {},
		GroupChanges: {},
	}

	for _, change := range changes {
		switch {
		case change.Conflicted:
			groups[GroupMerge] = append(groups[GroupMerge], Resource{
				Group: GroupMerge, Status: StatusUnmerged, RawStatus: change.RawXY[0], Path: change.Path,
				OriginalPath: change.OriginalPath,
			})
		case change.Untracked:
			groups[GroupChanges] = append(groups[GroupChanges], Resource{
				Group: GroupChanges, Status: StatusAdded, RawStatus: change.RawXY[0], Path: change.Path, Untracked: true,
			})
		default:
			if change.IndexStatus != StatusUnmodified {
				groups[GroupStaged] = append(groups[GroupStaged], Resource{
					Group: GroupStaged, Status: change.IndexStatus, RawStatus: change.RawXY[0], Path: change.Path,
					OriginalPath: change.OriginalPath,
				})
			}
			if change.WorktreeStatus != StatusUnmodified {
				groups[GroupChanges] = append(groups[GroupChanges], Resource{
					Group: GroupChanges, Status: change.WorktreeStatus, RawStatus: change.RawXY[1], Path: change.Path,
					OriginalPath: change.OriginalPath,
				})
			}
		}
	}

	for _, resources := range groups {
		sort.SliceStable(resources, func(i, j int) bool {
			if cmp := bytes.Compare(resources[i].Path, resources[j].Path); cmp != 0 {
				return cmp < 0
			}
			return bytes.Compare(resources[i].OriginalPath, resources[j].OriginalPath) < 0
		})
	}

	return groups
}

type Repository interface {
	Snapshot(context.Context) (Snapshot, error)
	LocalBranches(context.Context) ([]LocalBranch, error)
	ValidateBranch(context.Context, string) error
	Checkout(context.Context, string) error
	CreateBranch(context.Context, string) error
	Fetch(context.Context, BranchState) error
	FastForward(context.Context, BranchState) error
	Push(context.Context, BranchState, string) error
}
