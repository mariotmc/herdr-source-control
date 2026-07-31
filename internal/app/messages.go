package app

import (
	"time"

	"github.com/mariotmc/herdr-source-control/internal/domain"
	"github.com/mariotmc/herdr-source-control/internal/ui"
)

type pollTickMsg time.Time
type noticeExpiredMsg struct{ ID uint64 }

type snapshotLoadedMsg struct {
	RequestID  uint64
	Epoch      uint64
	Snapshot   domain.Snapshot
	Repository domain.Repository
	Err        error
}

type branchesLoadedMsg struct {
	RequestID uint64
	Branches  []domain.LocalBranch
	Err       error
}

type branchValidatedMsg struct {
	RequestID uint64
	Name      string
	Err       error
}

type mutationFinishedMsg struct {
	ID        uint64
	Operation Operation
	Detail    any
	Err       error
}

type activateMsg FocusTarget

type mouseAction uint8

const (
	mouseActivate mouseAction = iota
	mouseSelectChange
	mouseSelectBranch
	mouseCancelModal
	mouseScrollChanges
	mouseOpenCreate
	mouseSubmitCreate
)

type mouseActionMsg struct {
	Action   mouseAction
	Target   FocusTarget
	Identity ui.ChangeIdentity
	Index    int
	Delta    int
	When     time.Time
}
