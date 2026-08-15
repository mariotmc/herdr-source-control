package app

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"github.com/mariotmc/herdr-source-control/internal/domain"
	"github.com/mariotmc/herdr-source-control/internal/ui"
)

func (m *Model) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := message.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.ensureSelectionVisible()
		return m, nil
	case tea.BlurMsg:
		m.focused = false
		return m, nil
	case tea.FocusMsg:
		if m.focused {
			return m, nil
		}
		m.focused = true
		return m, m.requestRefresh(RefreshFocus)
	case pollTickMsg:
		if !m.focused {
			return m, pollCmd()
		}
		return m, batchCmd(pollCmd(), m.requestRefresh(RefreshPoll))
	case autoFetchTickMsg:
		return m, batchCmd(autoFetchCmd(), m.startAutoFetch(false))
	case autoFetchFinishedMsg:
		return m, m.autoFetchFinished(msg)
	case snapshotLoadedMsg:
		return m, m.snapshotLoaded(msg)
	case branchesLoadedMsg:
		return m, m.branchesLoaded(msg)
	case branchValidatedMsg:
		return m, m.branchValidated(msg)
	case mutationFinishedMsg:
		return m, m.mutationFinished(msg)
	case noticeExpiredMsg:
		if m.status.ID == msg.ID {
			m.status = StatusMessage{}
		}
		return m, nil
	case activateMsg:
		return m, m.activate(FocusTarget(msg))
	case mouseActionMsg:
		return m, m.mouseAction(msg)
	case tea.KeyPressMsg:
		return m, m.keyPress(msg)
	}
	if m.mode == ModeBranches || m.mode == ModeCreateBranch {
		return m, m.updateInput(message)
	}
	return m, nil
}

func (m *Model) snapshotLoaded(msg snapshotLoadedMsg) tea.Cmd {
	if msg.RequestID != m.refreshID || msg.Epoch != m.repositoryEpoch {
		return nil
	}
	if m.refreshCancel != nil {
		m.refreshCancel()
	}
	m.refreshBusy, m.refreshVisible, m.refreshCancel = false, false, nil
	phase := m.syncRefreshPhase
	m.syncRefreshPhase = OperationNone
	if msg.Err != nil {
		m.stale = m.snapshot != nil
		m.lastError = msg.Err
		m.setError(msg.Err.Error())
		if m.syncState != nil && phase != OperationNone {
			m.syncState.FinalErr = msg.Err
			return m.finishSyncAfterRefresh()
		}
		return m.serviceQueuedRefresh()
	}
	m.repo = msg.Repository
	m.lastError, m.stale = nil, false
	m.publishSnapshot(msg.Snapshot)
	if phase != OperationNone && m.syncState != nil {
		return m.continueSync(phase, msg.Snapshot)
	}
	if m.status.Error {
		m.status = StatusMessage{}
	}
	return batchCmd(m.servicePendingFetch(), m.serviceQueuedRefresh())
}

func (m *Model) autoFetchReady() bool {
	return m.repo != nil && m.snapshot != nil && !m.fetchBusy &&
		m.mutation == OperationNone && m.syncState == nil && usableUpstream(m.snapshot.Branch)
}

func usableUpstream(branch domain.BranchState) bool {
	return branch.State == domain.HeadAttached && branch.Upstream != "" && branch.UpstreamRef != "" &&
		branch.RemoteName != "" && branch.RemoteRef != "" && branch.CountsKnown
}

// servicePendingFetch runs after a snapshot lands so the fetch uses the branch
// Git just confirmed rather than the one that was current before a checkout.
func (m *Model) servicePendingFetch() tea.Cmd {
	if !m.pendingFetch {
		return nil
	}
	command := m.startAutoFetch(m.pendingFetchForce)
	if command == nil {
		return nil
	}
	m.pendingFetch, m.pendingFetchForce = false, false
	return command
}

func (m *Model) autoFetchFinished(msg autoFetchFinishedMsg) tea.Cmd {
	if msg.ID != m.fetchID {
		return nil
	}
	m.fetchBusy = false
	if msg.Record.LastAttemptUnix != 0 {
		m.lastFetchAttempt = time.Unix(msg.Record.LastAttemptUnix, 0)
	}
	if msg.Record.LastSuccessUnix != 0 {
		m.lastFetchSuccess = time.Unix(msg.Record.LastSuccessUnix, 0)
	}
	m.lastFetchFailed = msg.Record.LastError != ""
	if msg.Skipped {
		return nil
	}
	m.logger.Debug("auto fetch", "error", msg.Err)
	return m.requestRefresh(RefreshPoll)
}

func (m *Model) publishSnapshot(snapshot domain.Snapshot) {
	hadSnapshot := m.snapshot != nil
	oldFocus := m.focus
	previous := ui.Selectable(m.rows)
	previousIndex := -1
	for index, identity := range previous {
		if identity == m.selected {
			previousIndex = index
			break
		}
	}
	m.snapshot = &snapshot
	m.root = snapshot.Root
	m.rows = ui.Rows(snapshot.Changes)
	current := ui.Selectable(m.rows)
	if len(current) == 0 {
		m.selected = ui.ChangeIdentity{}
		m.focus = FocusBranch
		m.scrollOffset = 0
		return
	}
	for _, identity := range current {
		if identity == m.selected {
			if !hadSnapshot {
				m.focus = FocusChanges
			} else {
				m.focus = oldFocus
			}
			m.ensureSelectionVisible()
			return
		}
	}
	present := make(map[ui.ChangeIdentity]bool, len(current))
	for _, identity := range current {
		present[identity] = true
	}
	for index := previousIndex - 1; index >= 0; index-- {
		if present[previous[index]] {
			m.selected = previous[index]
			if !hadSnapshot {
				m.focus = FocusChanges
			} else {
				m.focus = oldFocus
			}
			m.ensureSelectionVisible()
			return
		}
	}
	for index := previousIndex + 1; index < len(previous); index++ {
		if present[previous[index]] {
			m.selected = previous[index]
			if !hadSnapshot {
				m.focus = FocusChanges
			} else {
				m.focus = oldFocus
			}
			m.ensureSelectionVisible()
			return
		}
	}
	m.selected = current[0]
	if !hadSnapshot {
		m.focus = FocusChanges
	} else {
		m.focus = oldFocus
	}
	m.ensureSelectionVisible()
}

func (m *Model) serviceQueuedRefresh() tea.Cmd {
	reasons := m.queuedRefreshReasons
	m.queuedRefreshReasons = 0
	if reasons == 0 {
		return nil
	}
	return m.requestRefresh(reasons)
}

func (m *Model) branchesLoaded(msg branchesLoadedMsg) tea.Cmd {
	if m.mode != ModeBranches || msg.RequestID != m.branchLoadID {
		return nil
	}
	if msg.Err != nil {
		m.setError(msg.Err.Error())
		return nil
	}
	m.branches = msg.Branches
	m.filterBranches()
	return nil
}

func (m *Model) branchValidated(msg branchValidatedMsg) tea.Cmd {
	if m.mode != ModeCreateBranch || msg.RequestID != m.validationID || msg.Name != m.input.Value() {
		return nil
	}
	m.validationID = 0
	if msg.Err != nil {
		m.setError(msg.Err.Error())
		return nil
	}
	return m.startMutation(OperationCreateBranch, msg.Name)
}

func (m *Model) startMutation(operation Operation, detail any) tea.Cmd {
	if m.mutation != OperationNone || m.repo == nil {
		m.setInformation("Wait for the current Git operation to finish.", 3*time.Second)
		return nil
	}
	m.repositoryEpoch++
	if m.refreshCancel != nil {
		m.refreshCancel()
	}
	m.refreshCancel, m.refreshBusy = nil, false
	m.refreshVisible = false
	m.nextRequestID++
	m.refreshID = m.nextRequestID
	m.queuedRefreshReasons |= RefreshMutation
	m.mutationID++
	m.mutation = operation
	ctx := context.Background()
	if operation == OperationSyncFetch {
		ctx = m.ctx
	}
	command := mutationCmd(ctx, m.repo, m.mutationID, operation, detail)
	if operation == OperationSyncFetch {
		return m.track(command)
	}
	return command
}

func (m *Model) mutationFinished(msg mutationFinishedMsg) tea.Cmd {
	if msg.ID != m.mutationID || msg.Operation != m.mutation {
		return nil
	}
	m.logger.Info("Git mutation", "operation", operationName(msg.Operation), "duration", msg.Detail, "error", msg.Err)
	if m.syncState != nil {
		return m.syncMutationFinished(msg)
	}
	if msg.Err != nil {
		m.setError(msg.Err.Error())
	} else {
		if msg.Operation == OperationCheckout {
			m.setSuccess("Branch switched.")
		} else {
			m.setSuccess("Branch created.")
		}
		m.mode = ModeMain
		m.input.Blur()
		m.pendingFetch, m.pendingFetchForce = true, true
	}
	m.mutation = OperationNone
	return m.serviceQueuedRefresh()
}

func (m *Model) startSync() tea.Cmd {
	if m.snapshot == nil {
		m.setInformation("Wait for the repository to load.", 3*time.Second)
		return nil
	}
	branch := m.snapshot.Branch
	if branch.State == domain.HeadUnborn || branch.OID == "" {
		m.setInformation("Create the first commit before syncing.", 3*time.Second)
		return nil
	}
	if branch.State != domain.HeadAttached {
		m.setInformation("Sync requires an attached local branch.", 3*time.Second)
		return nil
	}
	if branch.Upstream == "" || branch.UpstreamRef == "" || branch.RemoteName == "" || branch.RemoteRef == "" || !branch.CountsKnown {
		m.setInformation("The current branch has no usable remote upstream.", 3*time.Second)
		return nil
	}
	if m.snapshot.Operation.Active() {
		m.setInformation("Finish the current merge, rebase, cherry-pick, or revert before syncing.", 3*time.Second)
		return nil
	}
	if m.mutation != OperationNone {
		m.setInformation("Wait for the current Git operation to finish.", 3*time.Second)
		return nil
	}
	m.syncState = &SyncState{
		ID: m.mutationID + 1, Phase: OperationSyncFetch, Branch: branch.Name,
		Upstream: branch.Upstream, UpstreamRef: branch.UpstreamRef,
		RemoteName: branch.RemoteName, RemoteRef: branch.RemoteRef,
	}
	return m.startMutation(OperationSyncFetch, branch)
}

func (m *Model) syncMutationFinished(msg mutationFinishedMsg) tea.Cmd {
	if msg.Err != nil {
		m.syncState.FinalErr = msg.Err
		return m.startRefresh(RefreshMutation, msg.Operation)
	}
	return m.startRefresh(RefreshMutation, msg.Operation)
}

func (m *Model) continueSync(phase Operation, snapshot domain.Snapshot) tea.Cmd {
	if m.syncState.FinalErr != nil {
		return m.finishSyncAfterRefresh()
	}
	if snapshot.Operation.Active() {
		m.syncState.FinalErr = fmt.Errorf("finish the current merge, rebase, cherry-pick, or revert before syncing")
		return m.finishSyncAfterRefresh()
	}
	if !m.syncIdentityMatches(snapshot.Branch) {
		m.syncState.FinalErr = fmt.Errorf("branch or upstream changed during sync")
		return m.finishSyncAfterRefresh()
	}
	branch := snapshot.Branch
	switch phase {
	case OperationSyncFetch:
		if branch.Ahead > 0 && branch.Behind > 0 {
			m.syncState.FinalErr = fmt.Errorf("branch has diverged; sync only supports fast-forward updates")
			return m.finishSyncAfterRefresh()
		}
		if branch.Behind > 0 {
			m.mutation = OperationNone
			m.syncState.Phase = OperationSyncFastForward
			return m.startMutation(OperationSyncFastForward, branch)
		}
		return m.pushOrFinish(branch)
	case OperationSyncFastForward:
		if branch.Behind != 0 {
			m.syncState.FinalErr = fmt.Errorf("Upstream changed during sync.")
			return m.finishSyncAfterRefresh()
		}
		return m.pushOrFinish(branch)
	case OperationSyncPush:
		return m.finishSyncAfterRefresh()
	}
	return nil
}

func (m *Model) pushOrFinish(branch domain.BranchState) tea.Cmd {
	if branch.Ahead == 0 {
		return m.finishSyncAfterRefresh()
	}
	if !validOID(branch.OID) {
		m.syncState.FinalErr = fmt.Errorf("Git returned an invalid commit object ID")
		return m.finishSyncAfterRefresh()
	}
	m.syncState.CommitOID = branch.OID
	m.syncState.Phase = OperationSyncPush
	m.mutation = OperationNone
	return m.startMutation(OperationSyncPush, pushDetail{Branch: branch, OID: branch.OID})
}

func (m *Model) finishSyncAfterRefresh() tea.Cmd {
	err := m.syncState.FinalErr
	m.syncState, m.mutation = nil, OperationNone
	m.queuedRefreshReasons &^= RefreshMutation
	if err != nil {
		m.setError(err.Error())
	} else {
		m.setSuccess("Sync complete.")
	}
	return m.serviceQueuedRefresh()
}

func (m *Model) syncIdentityMatches(branch domain.BranchState) bool {
	sync := m.syncState
	return branch.State == domain.HeadAttached && branch.Name == sync.Branch &&
		branch.Upstream == sync.Upstream && branch.UpstreamRef == sync.UpstreamRef &&
		branch.RemoteName == sync.RemoteName && branch.RemoteRef == sync.RemoteRef && branch.CountsKnown
}

func validOID(value string) bool {
	if len(value) != 40 && len(value) != 64 {
		return false
	}
	for _, r := range value {
		if !strings.ContainsRune("0123456789abcdefABCDEF", r) {
			return false
		}
	}
	return true
}

func (m *Model) keyPress(message tea.KeyPressMsg) tea.Cmd {
	key := message.String()
	if m.mode == ModeHelp {
		switch key {
		case "esc", "?", "q":
			m.mode = ModeMain
		case "up", "k":
			m.helpOffset = max(0, m.helpOffset-1)
		case "down", "j":
			m.helpOffset++
		case "pgup":
			m.helpOffset = max(0, m.helpOffset-5)
		case "pgdown":
			m.helpOffset += 5
		}
		return nil
	}
	if m.mode == ModeCreateBranch {
		return m.createKey(message)
	}
	if m.mode == ModeBranches {
		return m.branchKey(message)
	}
	switch key {
	case "ctrl+c", "q":
		if isNonCancellable(m.mutation) {
			m.setInformation("Wait for the Git operation to finish.", 3*time.Second)
			return nil
		}
		m.cancel()
		return tea.Quit
	case "?":
		m.mode, m.helpOffset = ModeHelp, 0
	case "r":
		return m.requestRefresh(RefreshManual)
	case "b":
		return m.openBranches()
	case "s":
		return m.startSync()
	case "tab":
		m.moveFocus(1)
	case "shift+tab":
		m.moveFocus(-1)
	case "up", "k":
		m.moveSelection(-1)
	case "down", "j":
		m.moveSelection(1)
	case "home", "g":
		m.selectEdge(false)
	case "end", "G":
		m.selectEdge(true)
	case "pgup", "ctrl+u":
		m.moveSelection(-m.viewportRows() / 2)
	case "pgdown", "ctrl+d":
		m.moveSelection(m.viewportRows() / 2)
	case "enter", " ":
		return m.activate(m.focus)
	case "esc":
		if m.status.Text != "" {
			m.status = StatusMessage{}
		} else if len(ui.Selectable(m.rows)) > 0 {
			m.focus = FocusChanges
		}
	}
	return nil
}

func (m *Model) activate(target FocusTarget) tea.Cmd {
	switch target {
	case FocusBranch:
		return m.openBranches()
	case FocusSync:
		return m.startSync()
	case FocusRefresh:
		return m.requestRefresh(RefreshManual)
	case FocusChanges:
		m.setInformation("Diff view is not available yet.", 3*time.Second)
	}
	return nil
}

func (m *Model) openBranches() tea.Cmd {
	if m.repo == nil || m.snapshot == nil {
		m.setInformation("Wait for the repository to load.", 3*time.Second)
		return nil
	}
	m.mode, m.branchIndex = ModeBranches, 0
	m.branches, m.filtered = nil, nil
	m.input.Reset()
	m.input.Placeholder = "Search local branches"
	m.input.SetWidth(max(10, min(68, m.width-10)))
	return batchCmd(m.input.Focus(), m.loadBranches())
}

func (m *Model) branchKey(message tea.KeyPressMsg) tea.Cmd {
	key := message.String()
	if m.mutation == OperationCheckout {
		m.setInformation("Wait for the Git operation to finish.", 3*time.Second)
		return nil
	}
	switch key {
	case "esc":
		m.mode = ModeMain
		m.input.Blur()
		return nil
	case "up", "ctrl+p":
		m.branchIndex = max(0, m.branchIndex-1)
		return nil
	case "down", "ctrl+n":
		m.branchIndex = min(len(m.filtered), m.branchIndex+1)
		return nil
	case "pgup":
		m.branchIndex = max(0, m.branchIndex-5)
		return nil
	case "pgdown":
		m.branchIndex = min(len(m.filtered), m.branchIndex+5)
		return nil
	case "enter":
		return m.activateBranchSelection()
	case "n":
		if m.input.Value() == "" {
			return m.openCreate()
		}
	}
	var command tea.Cmd
	m.input, command = m.input.Update(message)
	m.filterBranches()
	return command
}

func (m *Model) activateBranchSelection() tea.Cmd {
	if m.branchIndex == 0 {
		return m.openCreate()
	}
	branch := m.filtered[m.branchIndex-1]
	if branch.Current {
		m.setInformation(fmt.Sprintf("Already on branch %q.", branch.Name), 3*time.Second)
		return nil
	}
	if branch.WorktreePath != "" {
		m.setInformation("Branch is already checked out in another worktree: "+ui.EscapeText(branch.WorktreePath), 3*time.Second)
		return nil
	}
	return m.startMutation(OperationCheckout, branch.Name)
}

func (m *Model) openCreate() tea.Cmd {
	if m.snapshot == nil || m.snapshot.Branch.State == domain.HeadUnborn {
		m.setInformation("Create the first commit before creating another branch.", 3*time.Second)
		return nil
	}
	m.mode, m.createFocus = ModeCreateBranch, 0
	m.validationID = 0
	m.input.Reset()
	m.input.Placeholder = "feature/name"
	return m.input.Focus()
}

func (m *Model) createKey(message tea.KeyPressMsg) tea.Cmd {
	key := message.String()
	if m.mutation == OperationCreateBranch {
		m.setInformation("Wait for the Git operation to finish.", 3*time.Second)
		return nil
	}
	switch key {
	case "esc":
		m.mode = ModeBranches
		m.validationID = 0
		m.input.Reset()
		return m.input.Focus()
	case "tab":
		m.createFocus = (m.createFocus + 1) % 3
	case "shift+tab":
		m.createFocus = (m.createFocus + 2) % 3
	case "enter":
		if m.createFocus == 2 {
			m.mode = ModeBranches
			m.input.Reset()
			return m.input.Focus()
		}
		return m.validateBranch(m.input.Value())
	default:
		if m.createFocus == 0 {
			return m.updateInput(message)
		}
	}
	return nil
}

func (m *Model) updateInput(message tea.Msg) tea.Cmd {
	before := m.input.Value()
	var command tea.Cmd
	m.input, command = m.input.Update(message)
	if m.input.Value() != before {
		m.validationID = 0
		if m.mode == ModeBranches {
			m.filterBranches()
		}
	}
	return command
}

func (m *Model) mouseAction(msg mouseActionMsg) tea.Cmd {
	switch msg.Action {
	case mouseActivate:
		m.focus = msg.Target
		return m.activate(msg.Target)
	case mouseSelectChange:
		m.selected, m.focus = msg.Identity, FocusChanges
		m.ensureSelectionVisible()
		if msg.Identity == m.lastMouseSelection && msg.When.Sub(m.lastMouseClick) <= 500*time.Millisecond {
			m.lastMouseSelection, m.lastMouseClick = ui.ChangeIdentity{}, time.Time{}
			return m.activate(FocusChanges)
		}
		m.lastMouseSelection, m.lastMouseClick = msg.Identity, msg.When
	case mouseSelectBranch:
		m.branchIndex = msg.Index
		if msg.Index == m.lastMouseBranch && msg.When.Sub(m.lastBranchClick) <= 500*time.Millisecond {
			m.lastMouseBranch, m.lastBranchClick = -1, time.Time{}
			return m.activateBranchSelection()
		}
		m.lastMouseBranch, m.lastBranchClick = msg.Index, msg.When
	case mouseCancelModal:
		if m.mutation == OperationCheckout || m.mutation == OperationCreateBranch {
			return nil
		}
		if m.mode == ModeCreateBranch {
			m.mode = ModeBranches
			m.validationID = 0
			m.input.Reset()
			return m.input.Focus()
		}
		m.mode = ModeMain
		m.input.Blur()
	case mouseScrollChanges:
		m.scrollOffset = max(0, min(max(0, len(m.rows)-m.viewportRows()), m.scrollOffset+msg.Delta))
	case mouseOpenCreate:
		return m.openCreate()
	case mouseSubmitCreate:
		return m.validateBranch(m.input.Value())
	}
	return nil
}

func (m *Model) filterBranches() {
	query := strings.ToLower(m.input.Value())
	m.filtered = m.filtered[:0]
	for _, branch := range m.branches {
		if fuzzyMatch(strings.ToLower(branch.Name), query) {
			m.filtered = append(m.filtered, branch)
		}
	}
	m.branchIndex = min(m.branchIndex, len(m.filtered))
}

func fuzzyMatch(value, query string) bool {
	if query == "" {
		return true
	}
	for _, needle := range query {
		found := false
		for len(value) > 0 {
			r, size := utf8.DecodeRuneInString(value)
			value = value[size:]
			if r == needle {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func (m *Model) moveFocus(direction int) {
	targets := []FocusTarget{FocusBranch, FocusSync, FocusRefresh}
	if len(ui.Selectable(m.rows)) > 0 {
		targets = append(targets, FocusChanges)
	}
	index := 0
	for i, target := range targets {
		if target == m.focus {
			index = i
		}
	}
	index = (index + direction + len(targets)) % len(targets)
	m.focus = targets[index]
}

func (m *Model) moveSelection(delta int) {
	identities := ui.Selectable(m.rows)
	if len(identities) == 0 {
		return
	}
	index := 0
	for i, identity := range identities {
		if identity == m.selected {
			index = i
		}
	}
	index = max(0, min(len(identities)-1, index+delta))
	m.selected, m.focus = identities[index], FocusChanges
	m.ensureSelectionVisible()
}

func (m *Model) selectEdge(last bool) {
	identities := ui.Selectable(m.rows)
	if len(identities) == 0 {
		return
	}
	index := 0
	if last {
		index = len(identities) - 1
	}
	m.selected, m.focus = identities[index], FocusChanges
	m.ensureSelectionVisible()
}

func (m *Model) ensureSelectionVisible() {
	row := ui.Find(m.rows, m.selected)
	if row < 0 {
		return
	}
	height := m.viewportRows()
	if row < m.scrollOffset {
		m.scrollOffset = row
	} else if row >= m.scrollOffset+height {
		m.scrollOffset = row - height + 1
	}
	m.scrollOffset = max(0, min(m.scrollOffset, max(0, len(m.rows)-height)))
}

func (m *Model) viewportRows() int { return max(1, m.height-9) }

func isNonCancellable(operation Operation) bool {
	return operation == OperationCheckout || operation == OperationCreateBranch ||
		operation == OperationSyncFastForward || operation == OperationSyncPush
}

func operationName(operation Operation) string {
	return []string{"none", "checkout", "create branch", "sync fetch", "sync fast-forward", "sync push"}[operation]
}

func (m *Model) setError(text string) {
	m.status = StatusMessage{ID: m.status.ID + 1, Text: text, Error: true}
}

func (m *Model) setInformation(text string, duration time.Duration) {
	m.status = StatusMessage{ID: m.status.ID + 1, Text: text, Expires: time.Now().Add(duration)}
}

func (m *Model) setSuccess(text string) { m.setInformation(text, 5*time.Second) }

func (m *Model) clearTransient() {
	if !m.status.Error {
		m.status = StatusMessage{}
	}
}
