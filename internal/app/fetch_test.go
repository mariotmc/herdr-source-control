package app

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/mariotmc/herdr-source-control/internal/domain"
	"github.com/mariotmc/herdr-source-control/internal/state"
	"github.com/mariotmc/herdr-source-control/internal/ui"
)

func fetchableSnapshot() domain.Snapshot {
	snapshot := attachedSnapshot(0, 1)
	snapshot.CommonDir = "/repo/.git"
	snapshot.Root = "/repo"
	return snapshot
}

func TestPollIsSkippedWhileBlurredAndResumesOnFocus(t *testing.T) {
	repository := &fakeRepository{snapshots: []domain.Snapshot{fetchableSnapshot()}}
	model := New(Config{Repository: repository, StartRoot: "/repo"})
	update(t, model, tea.BlurMsg{})
	if command := update(t, model, pollTickMsg(time.Now())); command == nil {
		t.Fatal("blurred poll did not schedule the next tick")
	}
	if model.refreshBusy || model.queuedRefreshReasons != 0 {
		t.Fatalf("blurred poll refreshed: busy=%v queued=%v", model.refreshBusy, model.queuedRefreshReasons)
	}
	command := update(t, model, tea.FocusMsg{})
	if command == nil || !model.refreshBusy {
		t.Fatal("focus did not request a refresh")
	}
	update(t, model, execute(t, command))
	if repository.snapshotCalls != 1 {
		t.Fatalf("snapshot calls = %d, want 1", repository.snapshotCalls)
	}
}

func TestFocusDefaultsToVisibleSoPollingRunsWithoutFocusEvents(t *testing.T) {
	repository := &fakeRepository{snapshots: []domain.Snapshot{fetchableSnapshot()}}
	model := New(Config{Repository: repository, StartRoot: "/repo"})
	if !model.focused {
		t.Fatal("focused did not default to true")
	}
	if command := update(t, model, pollTickMsg(time.Now())); command == nil {
		t.Fatal("poll did not schedule the next tick")
	}
	if !model.refreshBusy {
		t.Fatal("poll did not refresh without a preceding focus event")
	}
}

func TestAutoFetchTickStartsFetchAndReschedules(t *testing.T) {
	repository := &fakeRepository{snapshots: []domain.Snapshot{fetchableSnapshot()}}
	model := New(Config{Repository: repository, StartRoot: "/repo", StateDir: t.TempDir()})
	model.publishSnapshot(fetchableSnapshot())
	if command := update(t, model, autoFetchTickMsg(time.Now())); command == nil {
		t.Fatal("auto-fetch tick did not schedule the next tick")
	}
	if !model.fetchBusy || model.mutation != OperationNone {
		t.Fatalf("tick state: busy=%v mutation=%v", model.fetchBusy, model.mutation)
	}
	// The tick command is batched with the three-minute timer; run the fetch on its own.
	model.fetchBusy = false
	message, ok := execute(t, model.startAutoFetch(false)).(autoFetchFinishedMsg)
	if !ok || message.Skipped || repository.fetchCalls != 1 {
		t.Fatalf("fetch did not run: %#v calls=%d", message, repository.fetchCalls)
	}
	command := update(t, model, message)
	if model.fetchBusy || model.mutation != OperationNone || model.lastFetchSuccess.IsZero() {
		t.Fatalf("completion state: busy=%v mutation=%v success=%v", model.fetchBusy, model.mutation, model.lastFetchSuccess)
	}
	if command == nil || !model.refreshBusy {
		t.Fatal("completed fetch did not request a refresh")
	}
}

func TestAutoFetchSkipsMutationSyncAndUnusableUpstream(t *testing.T) {
	repository := &fakeRepository{}
	model := New(Config{Repository: repository, StartRoot: "/repo", StateDir: t.TempDir()})
	model.publishSnapshot(fetchableSnapshot())

	model.mutation = OperationCheckout
	if command := update(t, model, autoFetchTickMsg(time.Now())); command == nil {
		t.Fatal("tick did not reschedule during a mutation")
	}
	if model.fetchBusy {
		t.Fatal("auto-fetch started during a mutation")
	}
	model.mutation = OperationNone

	model.syncState = &SyncState{}
	update(t, model, autoFetchTickMsg(time.Now()))
	if model.fetchBusy {
		t.Fatal("auto-fetch started during a user sync")
	}
	model.syncState = nil

	detached := fetchableSnapshot()
	detached.Branch = domain.BranchState{State: domain.HeadDetached, OID: strings.Repeat("a", 40)}
	model.publishSnapshot(detached)
	update(t, model, autoFetchTickMsg(time.Now()))
	if model.fetchBusy || repository.fetchCalls != 0 {
		t.Fatalf("auto-fetch ran without a usable upstream: busy=%v calls=%d", model.fetchBusy, repository.fetchCalls)
	}

	noCounts := fetchableSnapshot()
	noCounts.Branch.CountsKnown = false
	model.publishSnapshot(noCounts)
	update(t, model, autoFetchTickMsg(time.Now()))
	if model.fetchBusy || repository.fetchCalls != 0 {
		t.Fatalf("auto-fetch ran with unknown counts: busy=%v calls=%d", model.fetchBusy, repository.fetchCalls)
	}
}

func TestAutoFetchThrottleIsSharedThroughStateStore(t *testing.T) {
	directory := t.TempDir()
	store := state.Store{Dir: directory}
	key := state.Key("/repo")
	attempt := time.Now().Add(-time.Minute)
	if err := store.Save(key, state.Record{LastAttemptUnix: attempt.Unix()}); err != nil {
		t.Fatal(err)
	}
	repository := &fakeRepository{}
	model := New(Config{Repository: repository, StartRoot: "/repo", StateDir: directory})
	model.publishSnapshot(fetchableSnapshot())

	message, ok := execute(t, model.startAutoFetch(false)).(autoFetchFinishedMsg)
	if !ok || !message.Skipped || repository.fetchCalls != 0 {
		t.Fatalf("another process's attempt did not throttle this one: %#v calls=%d", message, repository.fetchCalls)
	}
	update(t, model, message)
	if model.lastFetchAttempt.Unix() != attempt.Unix() {
		t.Fatalf("skipped fetch did not adopt the shared attempt time: %v", model.lastFetchAttempt)
	}

	forced, ok := execute(t, model.startAutoFetch(true)).(autoFetchFinishedMsg)
	if !ok || forced.Skipped || repository.fetchCalls != 1 {
		t.Fatalf("forced fetch was throttled: %#v calls=%d", forced, repository.fetchCalls)
	}
	record, err := store.Load(key)
	if err != nil {
		t.Fatal(err)
	}
	if record.LastSuccessUnix == 0 || record.LastAttemptUnix == attempt.Unix() {
		t.Fatalf("fetch was not recorded for other processes: %#v", record)
	}
}

func TestSuccessfulCheckoutFetchesImmediately(t *testing.T) {
	directory := t.TempDir()
	store := state.Store{Dir: directory}
	key := state.Key("/repo")
	if err := store.Save(key, state.Record{LastAttemptUnix: time.Now().Unix()}); err != nil {
		t.Fatal(err)
	}
	switched := fetchableSnapshot()
	switched.Branch.Name = "topic"
	repository := &fakeRepository{snapshots: []domain.Snapshot{switched}}
	model := New(Config{Repository: repository, StartRoot: "/repo", StateDir: directory})
	model.publishSnapshot(fetchableSnapshot())
	model.pendingFetch = false

	command := model.startMutation(OperationCheckout, "topic")
	command = update(t, model, execute(t, command))
	if !model.pendingFetch || !model.pendingFetchForce {
		t.Fatal("checkout did not arm an immediate fetch")
	}
	command = update(t, model, execute(t, command))
	if model.pendingFetch || !model.fetchBusy {
		t.Fatalf("pending fetch was not started: pending=%v busy=%v", model.pendingFetch, model.fetchBusy)
	}
	message, ok := execute(t, command).(autoFetchFinishedMsg)
	if !ok || message.Skipped || repository.fetchCalls != 1 {
		t.Fatalf("checkout fetch did not bypass the throttle: %#v calls=%d", message, repository.fetchCalls)
	}
}

func TestStatusFooterReportsFetchStaleness(t *testing.T) {
	model := New(Config{StartRoot: "/repo"})
	model.width, model.height = 120, 35
	model.publishSnapshot(attachedSnapshot(0, 0))
	cases := []struct {
		prepare func()
		want    string
	}{
		{func() {}, "Ready · not checked yet"},
		{func() { model.lastFetchSuccess = time.Now().Add(-2 * time.Minute) }, "Ready · checked 2m ago"},
		{func() {
			model.lastFetchFailed = true
			model.lastFetchAttempt = time.Now().Add(-12 * time.Minute)
		}, "Ready · check failed 12m ago"},
	}
	for _, test := range cases {
		test.prepare()
		if got := ansi.Strip(model.statusFooter(ui.SizeWide, 120)); !strings.Contains(got, test.want) {
			t.Errorf("footer = %q, want %q", got, test.want)
		}
	}
	if got := ansi.Strip(model.statusFooter(ui.SizeSmall, 50)); strings.Contains(got, "check failed") {
		t.Errorf("small footer kept the fetch suffix: %q", got)
	}
	model.setError("Git fetch failed.")
	if got := ansi.Strip(model.statusFooter(ui.SizeWide, 120)); !strings.Contains(got, "Git fetch failed.") || strings.Contains(got, "check failed") {
		t.Errorf("error status lost priority over fetch staleness: %q", got)
	}
}
