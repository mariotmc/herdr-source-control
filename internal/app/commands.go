package app

import (
	"context"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/mariotmc/herdr-source-control/internal/domain"
	"github.com/mariotmc/herdr-source-control/internal/state"
)

func pollCmd() tea.Cmd {
	return tea.Tick(pollInterval, func(now time.Time) tea.Msg { return pollTickMsg(now) })
}

func autoFetchCmd() tea.Cmd {
	return tea.Tick(autoFetchInterval, func(now time.Time) tea.Msg { return autoFetchTickMsg(now) })
}

func batchCmd(commands ...tea.Cmd) tea.Cmd {
	var nonNil []tea.Cmd
	for _, command := range commands {
		if command != nil {
			nonNil = append(nonNil, command)
		}
	}
	if len(nonNil) == 0 {
		return nil
	}
	return tea.Batch(nonNil...)
}

func (m *Model) requestRefresh(reason RefreshReason) tea.Cmd {
	if m.mutation != OperationNone {
		if reason != RefreshPoll {
			m.queuedRefreshReasons |= reason
		}
		return nil
	}
	if m.refreshBusy {
		if reason != RefreshPoll {
			m.queuedRefreshReasons |= reason
		}
		return nil
	}
	if reason&RefreshManual != 0 {
		m.clearTransient()
	}
	return m.startRefresh(reason, OperationNone)
}

func (m *Model) startRefresh(reason RefreshReason, syncPhase Operation) tea.Cmd {
	m.nextRequestID++
	id, epoch := m.nextRequestID, m.repositoryEpoch
	m.refreshID, m.refreshBusy, m.syncRefreshPhase = id, true, syncPhase
	m.refreshVisible = reason&(RefreshStartup|RefreshManual|RefreshMutation) != 0
	ctx, cancel := context.WithCancel(m.ctx)
	m.refreshCancel = cancel
	repo, factory, root := m.repo, m.factory, m.root
	started := time.Now()
	return m.track(func() tea.Msg {
		if repo == nil && factory != nil {
			var err error
			repo, err = factory(ctx, root)
			if err != nil {
				return snapshotLoadedMsg{RequestID: id, Epoch: epoch, Err: err}
			}
		}
		if repo == nil {
			return snapshotLoadedMsg{RequestID: id, Epoch: epoch, Err: m.lastError}
		}
		snapshot, err := repo.Snapshot(ctx)
		m.logger.Debug("repository refresh", "duration", time.Since(started), "changes", len(snapshot.Changes), "error", err)
		return snapshotLoadedMsg{RequestID: id, Epoch: epoch, Snapshot: snapshot, Repository: repo, Err: err}
	})
}

// startAutoFetch updates remote-tracking refs in the background so Herdr's own
// ahead/behind sidebar stays truthful. It never sets m.mutation: it must not
// block the UI or interfere with a user-initiated Sync.
func (m *Model) startAutoFetch(force bool) tea.Cmd {
	if !m.autoFetchReady() {
		return nil
	}
	m.fetchID++
	m.fetchBusy = true
	id := m.fetchID
	repo, branch := m.repo, m.snapshot.Branch
	store, key := m.fetchStore, state.Key(m.snapshot.Root)
	ctx, cancel := context.WithCancel(m.ctx)
	return m.track(func() tea.Msg {
		defer cancel()
		record, _ := store.Load(key)
		if !force && record.LastAttemptUnix != 0 && time.Since(time.Unix(record.LastAttemptUnix, 0)) < autoFetchInterval {
			return autoFetchFinishedMsg{ID: id, Record: record, Skipped: true}
		}
		record.LastAttemptUnix = time.Now().Unix()
		_ = store.Save(key, record)
		err := repo.Fetch(ctx, branch)
		if err != nil {
			record.LastError = err.Error()
		} else {
			record.LastSuccessUnix, record.LastError = time.Now().Unix(), ""
		}
		_ = store.Save(key, record)
		return autoFetchFinishedMsg{ID: id, Record: record, Err: err}
	})
}

func (m *Model) loadBranches() tea.Cmd {
	m.nextRequestID++
	id := m.nextRequestID
	m.branchLoadID = id
	repo := m.repo
	return m.track(func() tea.Msg {
		if repo == nil {
			return branchesLoadedMsg{RequestID: id, Err: m.lastError}
		}
		branches, err := repo.LocalBranches(m.ctx)
		return branchesLoadedMsg{RequestID: id, Branches: branches, Err: err}
	})
}

func (m *Model) validateBranch(name string) tea.Cmd {
	m.nextRequestID++
	id := m.nextRequestID
	m.validationID = id
	repo := m.repo
	return m.track(func() tea.Msg {
		if repo == nil {
			return branchValidatedMsg{RequestID: id, Name: name, Err: m.lastError}
		}
		return branchValidatedMsg{RequestID: id, Name: name, Err: repo.ValidateBranch(m.ctx, name)}
	})
}

func (m *Model) track(command tea.Cmd) tea.Cmd {
	return func() tea.Msg {
		if !m.commands.begin() {
			return nil
		}
		defer m.commands.done()
		return command()
	}
}

func mutationCmd(ctx context.Context, repo domain.Repository, id uint64, operation Operation, detail any) tea.Cmd {
	return func() tea.Msg {
		started := time.Now()
		var err error
		switch operation {
		case OperationCheckout:
			err = repo.Checkout(ctx, detail.(string))
		case OperationCreateBranch:
			err = repo.CreateBranch(ctx, detail.(string))
		case OperationSyncFetch:
			err = repo.Fetch(ctx, detail.(domain.BranchState))
		case OperationSyncFastForward:
			err = repo.FastForward(ctx, detail.(domain.BranchState))
		case OperationSyncPush:
			push := detail.(pushDetail)
			err = repo.Push(ctx, push.Branch, push.OID)
		}
		return mutationFinishedMsg{ID: id, Operation: operation, Detail: time.Since(started), Err: err}
	}
}

type pushDetail struct {
	Branch domain.BranchState
	OID    string
}

func noticeCmd(id uint64, duration time.Duration) tea.Cmd {
	return tea.Tick(duration, func(time.Time) tea.Msg { return noticeExpiredMsg{ID: id} })
}
