package app

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/mariotmc/herdr-source-control/internal/domain"
	"github.com/mariotmc/herdr-source-control/internal/ui"
)

type fakeRepository struct {
	snapshots        []domain.Snapshot
	snapshotErrors   []error
	branches         []domain.LocalBranch
	branchError      error
	validateError    error
	checkoutError    error
	createError      error
	fetchError       error
	fastForwardError error
	pushError        error

	snapshotCalls, fetchCalls, fastForwardCalls, pushCalls int
	checkoutNames, createNames                             []string
	pushedOID                                              string
}

func (f *fakeRepository) Snapshot(context.Context) (domain.Snapshot, error) {
	index := f.snapshotCalls
	f.snapshotCalls++
	var snapshot domain.Snapshot
	if index < len(f.snapshots) {
		snapshot = f.snapshots[index]
	} else if len(f.snapshots) > 0 {
		snapshot = f.snapshots[len(f.snapshots)-1]
	}
	if index < len(f.snapshotErrors) && f.snapshotErrors[index] != nil {
		return domain.Snapshot{}, f.snapshotErrors[index]
	}
	return snapshot, nil
}
func (f *fakeRepository) LocalBranches(context.Context) ([]domain.LocalBranch, error) {
	return f.branches, f.branchError
}
func (f *fakeRepository) ValidateBranch(context.Context, string) error { return f.validateError }
func (f *fakeRepository) Checkout(_ context.Context, name string) error {
	f.checkoutNames = append(f.checkoutNames, name)
	return f.checkoutError
}
func (f *fakeRepository) CreateBranch(_ context.Context, name string) error {
	f.createNames = append(f.createNames, name)
	return f.createError
}
func (f *fakeRepository) Fetch(context.Context, domain.BranchState) error {
	f.fetchCalls++
	return f.fetchError
}
func (f *fakeRepository) FastForward(context.Context, domain.BranchState) error {
	f.fastForwardCalls++
	return f.fastForwardError
}
func (f *fakeRepository) Push(_ context.Context, _ domain.BranchState, oid string) error {
	f.pushCalls++
	f.pushedOID = oid
	return f.pushError
}

func execute(t *testing.T, command tea.Cmd) tea.Msg {
	t.Helper()
	if command == nil {
		t.Fatal("expected command")
	}
	return command()
}

func update(t *testing.T, model *Model, message tea.Msg) tea.Cmd {
	t.Helper()
	_, command := model.Update(message)
	return command
}

func attachedSnapshot(ahead, behind uint64) domain.Snapshot {
	return domain.Snapshot{Root: "/repo", Branch: domain.BranchState{
		State: domain.HeadAttached, Name: "main", OID: strings.Repeat("a", 40),
		Upstream: "origin/main", UpstreamRef: "refs/remotes/origin/main",
		RemoteName: "origin", RemoteRef: "refs/heads/main",
		Ahead: ahead, Behind: behind, CountsKnown: true,
	}}
}

func TestRefreshCoalescesManualAndDropsPoll(t *testing.T) {
	repository := &fakeRepository{snapshots: []domain.Snapshot{{Root: "/repo"}, {Root: "/repo"}}}
	model := New(Config{Repository: repository, StartRoot: "/repo"})
	first := model.requestRefresh(RefreshStartup)
	if model.requestRefresh(RefreshPoll) != nil {
		t.Fatal("poll queued while refresh was active")
	}
	if model.requestRefresh(RefreshManual) != nil || model.queuedRefreshReasons != RefreshManual {
		t.Fatalf("manual reason was not coalesced: %v", model.queuedRefreshReasons)
	}
	second := update(t, model, execute(t, first))
	if second == nil || !model.refreshBusy || model.queuedRefreshReasons != 0 {
		t.Fatal("queued refresh was not started after completion")
	}
	update(t, model, execute(t, second))
	if repository.snapshotCalls != 2 || model.refreshBusy {
		t.Fatalf("snapshot calls=%d busy=%v", repository.snapshotCalls, model.refreshBusy)
	}
}

func TestStaleRefreshCannotClearNewerBookkeeping(t *testing.T) {
	model := New(Config{Repository: &fakeRepository{}, StartRoot: "/repo"})
	model.refreshID, model.refreshBusy, model.repositoryEpoch = 9, true, 2
	model.stale = true
	model.queuedRefreshReasons = RefreshManual
	model.setError("newer error")
	update(t, model, snapshotLoadedMsg{RequestID: 8, Epoch: 1, Snapshot: domain.Snapshot{Root: "/old"}})
	if !model.refreshBusy || !model.stale || model.status.Text != "newer error" || model.queuedRefreshReasons != RefreshManual {
		t.Fatalf("stale result changed state: %#v", model)
	}
}

func TestSelectionUsesGroupAndRawPathAndFallsBackward(t *testing.T) {
	model := New(Config{StartRoot: "/repo"})
	first := domain.Snapshot{Root: "/repo", Changes: []domain.Change{
		{Path: []byte("a"), WorktreeStatus: domain.StatusModified},
		{Path: []byte("b"), IndexStatus: domain.StatusAdded, WorktreeStatus: domain.StatusModified},
		{Path: []byte("c"), WorktreeStatus: domain.StatusModified},
	}}
	model.publishSnapshot(first)
	model.selected = ui.ChangeIdentity{Group: domain.GroupChanges, Path: "b"}
	second := domain.Snapshot{Root: "/repo", Changes: []domain.Change{
		{Path: []byte("a"), WorktreeStatus: domain.StatusModified},
		{Path: []byte("b"), IndexStatus: domain.StatusAdded},
		{Path: []byte("c"), WorktreeStatus: domain.StatusModified},
	}}
	model.publishSnapshot(second)
	if model.selected != (ui.ChangeIdentity{Group: domain.GroupChanges, Path: "a"}) {
		t.Fatalf("selection = %#v, want previous selectable identity", model.selected)
	}
}

func TestSyncFetchFastForwardPushesCapturedOID(t *testing.T) {
	postFetch := attachedSnapshot(0, 1)
	postFastForward := attachedSnapshot(2, 0)
	postFastForward.Branch.OID = strings.Repeat("b", 40)
	final := attachedSnapshot(0, 0)
	repository := &fakeRepository{snapshots: []domain.Snapshot{postFetch, postFastForward, final}}
	model := New(Config{Repository: repository, StartRoot: "/repo"})
	initial := attachedSnapshot(0, 0)
	model.publishSnapshot(initial)

	command := model.startSync()
	command = update(t, model, execute(t, command))
	command = update(t, model, execute(t, command))
	if model.mutation != OperationSyncFastForward {
		t.Fatalf("phase = %v, want fast-forward", model.mutation)
	}
	command = update(t, model, execute(t, command))
	command = update(t, model, execute(t, command))
	if model.mutation != OperationSyncPush {
		t.Fatalf("phase = %v, want push", model.mutation)
	}
	command = update(t, model, execute(t, command))
	command = update(t, model, execute(t, command))
	if command != nil || model.mutation != OperationNone || model.status.Text != "Sync complete." {
		t.Fatalf("sync did not finish: mutation=%v status=%q", model.mutation, model.status.Text)
	}
	if repository.fetchCalls != 1 || repository.fastForwardCalls != 1 || repository.pushCalls != 1 {
		t.Fatalf("calls fetch=%d ff=%d push=%d", repository.fetchCalls, repository.fastForwardCalls, repository.pushCalls)
	}
	if repository.pushedOID != postFastForward.Branch.OID {
		t.Fatalf("pushed %q, want captured %q", repository.pushedOID, postFastForward.Branch.OID)
	}
}

func TestSyncStopsOnIdentityChange(t *testing.T) {
	changed := attachedSnapshot(1, 0)
	changed.Branch.Name = "other"
	repository := &fakeRepository{snapshots: []domain.Snapshot{changed}}
	model := New(Config{Repository: repository, StartRoot: "/repo"})
	model.publishSnapshot(attachedSnapshot(0, 0))
	command := model.startSync()
	command = update(t, model, execute(t, command))
	update(t, model, execute(t, command))
	if repository.fastForwardCalls != 0 || repository.pushCalls != 0 || !model.status.Error {
		t.Fatalf("identity change continued sync: ff=%d push=%d status=%#v", repository.fastForwardCalls, repository.pushCalls, model.status)
	}
}

func TestSyncStopsOnDivergenceWithoutMutation(t *testing.T) {
	repository := &fakeRepository{snapshots: []domain.Snapshot{attachedSnapshot(1, 1)}}
	model := New(Config{Repository: repository, StartRoot: "/repo"})
	model.publishSnapshot(attachedSnapshot(0, 0))
	command := model.startSync()
	command = update(t, model, execute(t, command))
	update(t, model, execute(t, command))
	if repository.fastForwardCalls != 0 || repository.pushCalls != 0 || !strings.Contains(model.status.Text, "diverged") {
		t.Fatalf("divergence was not refused: ff=%d push=%d status=%q", repository.fastForwardCalls, repository.pushCalls, model.status.Text)
	}
}

func TestSyncStopsWhenConflictOperationAppears(t *testing.T) {
	postFetch := attachedSnapshot(0, 1)
	postFetch.Operation.Merge = true
	repository := &fakeRepository{snapshots: []domain.Snapshot{postFetch}}
	model := New(Config{Repository: repository, StartRoot: "/repo"})
	model.publishSnapshot(attachedSnapshot(0, 0))
	command := model.startSync()
	command = update(t, model, execute(t, command))
	update(t, model, execute(t, command))
	if repository.fastForwardCalls != 0 || repository.pushCalls != 0 || !strings.Contains(model.status.Text, "merge") {
		t.Fatalf("conflict operation continued sync: ff=%d push=%d status=%q", repository.fastForwardCalls, repository.pushCalls, model.status.Text)
	}
}

func TestCheckoutFailureKeepsBranchModalOpen(t *testing.T) {
	repository := &fakeRepository{checkoutError: errors.New("sanitized checkout failure")}
	model := New(Config{Repository: repository, StartRoot: "/repo"})
	model.publishSnapshot(attachedSnapshot(0, 0))
	model.mode = ModeBranches
	command := model.startMutation(OperationCheckout, "topic")
	update(t, model, execute(t, command))
	if model.mode != ModeBranches || !model.status.Error {
		t.Fatalf("modal=%v status=%#v", model.mode, model.status)
	}
}

func TestCheckoutModalCannotCloseWhileMutationRuns(t *testing.T) {
	model := New(Config{})
	model.mode, model.mutation = ModeBranches, OperationCheckout
	model.branchKey(tea.KeyPressMsg(tea.Key{Code: tea.KeyEscape}))
	if model.mode != ModeBranches {
		t.Fatal("checkout modal closed during non-cancellable mutation")
	}
}

func TestBranchPickerFiltersAndCurrentBranchDoesNotMutate(t *testing.T) {
	repository := &fakeRepository{branches: []domain.LocalBranch{{Name: "main", Current: true}, {Name: "feature/auth"}, {Name: "fix"}}}
	model := New(Config{Repository: repository, StartRoot: "/repo"})
	model.width = 80
	model.publishSnapshot(attachedSnapshot(0, 0))
	command := model.openBranches()
	loaded := repository.branches
	model.branchLoadID = 10
	update(t, model, branchesLoadedMsg{RequestID: 10, Branches: loaded})
	model.input.SetValue("fa")
	model.filterBranches()
	if len(model.filtered) != 1 || model.filtered[0].Name != "feature/auth" {
		t.Fatalf("filtered branches: %#v", model.filtered)
	}
	model.input.SetValue("")
	model.filterBranches()
	model.branchIndex = 1
	if got := model.activateBranchSelection(); got != nil || model.mutation != OperationNone {
		t.Fatal("current branch started checkout")
	}
	_ = command
}

func TestCreateValidationFailurePreservesModalAndInput(t *testing.T) {
	repository := &fakeRepository{validateError: errors.New("Invalid branch name.")}
	model := New(Config{Repository: repository, StartRoot: "/repo"})
	model.publishSnapshot(attachedSnapshot(0, 0))
	model.mode = ModeCreateBranch
	model.input.SetValue("bad name")
	command := model.validateBranch(model.input.Value())
	update(t, model, execute(t, command))
	if model.mode != ModeCreateBranch || model.input.Value() != "bad name" || !model.status.Error {
		t.Fatalf("create state changed after validation failure: mode=%v input=%q status=%#v", model.mode, model.input.Value(), model.status)
	}
}

func TestStaleBranchValidationCannotCreateOldInput(t *testing.T) {
	repository := &fakeRepository{}
	model := New(Config{Repository: repository, StartRoot: "/repo"})
	model.publishSnapshot(attachedSnapshot(0, 0))
	model.mode = ModeCreateBranch
	model.input.SetValue("branch-a")
	command := model.validateBranch("branch-a")
	model.input.SetValue("branch-b")
	if next := update(t, model, execute(t, command)); next != nil || model.mutation != OperationNone {
		t.Fatal("stale validation started a mutation")
	}
}

func TestQuitIsDisabledDuringNonCancellableMutation(t *testing.T) {
	model := New(Config{})
	model.mutation = OperationSyncPush
	command := model.keyPress(tea.KeyPressMsg(tea.Key{Text: "q", Code: 'q'}))
	if command != nil || model.status.Text != "Wait for the Git operation to finish." {
		t.Fatalf("quit was not disabled: command=%v status=%q", command != nil, model.status.Text)
	}
}

type blockingRepository struct {
	fakeRepository
	started chan struct{}
}

func (r *blockingRepository) Snapshot(ctx context.Context) (domain.Snapshot, error) {
	close(r.started)
	<-ctx.Done()
	return domain.Snapshot{}, ctx.Err()
}

func TestCloseCancelsAndWaitsForCancellableCommands(t *testing.T) {
	repository := &blockingRepository{started: make(chan struct{})}
	model := New(Config{Repository: repository, StartRoot: "/repo"})
	command := model.requestRefresh(RefreshStartup)
	commandDone := make(chan struct{})
	go func() {
		command()
		close(commandDone)
	}()
	<-repository.started
	model.Close()
	select {
	case <-commandDone:
	case <-time.After(time.Second):
		t.Fatal("cancellable command survived model close")
	}
}
