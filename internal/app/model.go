package app

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"github.com/mariotmc/herdr-source-control/internal/domain"
	"github.com/mariotmc/herdr-source-control/internal/ui"
)

const pollInterval = 2 * time.Second

type Mode uint8

const (
	ModeMain Mode = iota
	ModeBranches
	ModeCreateBranch
	ModeHelp
)

type FocusTarget uint8

const (
	FocusBranch FocusTarget = iota
	FocusSync
	FocusRefresh
	FocusChanges
)

type Operation uint8

const (
	OperationNone Operation = iota
	OperationCheckout
	OperationCreateBranch
	OperationSyncFetch
	OperationSyncFastForward
	OperationSyncPush
)

type RefreshReason uint8

const (
	RefreshStartup RefreshReason = 1 << iota
	RefreshPoll
	RefreshFocus
	RefreshManual
	RefreshMutation
)

type StatusMessage struct {
	ID      uint64
	Text    string
	Error   bool
	Expires time.Time
}

type SyncState struct {
	ID          uint64
	Phase       Operation
	Branch      string
	Upstream    string
	UpstreamRef string
	RemoteName  string
	RemoteRef   string
	CommitOID   string
	FinalErr    error
}

type RepositoryFactory func(context.Context, string) (domain.Repository, error)

type Config struct {
	Context           context.Context
	Repository        domain.Repository
	RepositoryFactory RepositoryFactory
	StartRoot         string
	Logger            *slog.Logger
	InitialError      error
}

type Model struct {
	ctx     context.Context
	cancel  context.CancelFunc
	repo    domain.Repository
	factory RepositoryFactory
	root    string
	logger  *slog.Logger
	styles  ui.Styles

	width, height int
	mode          Mode
	focus         FocusTarget
	focused       bool
	snapshot      *domain.Snapshot
	selected      ui.ChangeIdentity
	rows          []ui.ChangeRow
	scrollOffset  int

	input        textinput.Model
	branches     []domain.LocalBranch
	filtered     []domain.LocalBranch
	branchIndex  int
	branchLoadID uint64
	validationID uint64
	createFocus  int
	helpOffset   int

	nextRequestID        uint64
	refreshID            uint64
	refreshBusy          bool
	refreshVisible       bool
	refreshCancel        context.CancelFunc
	queuedRefreshReasons RefreshReason
	repositoryEpoch      uint64

	mutationID       uint64
	mutation         Operation
	syncState        *SyncState
	syncRefreshPhase Operation

	stale     bool
	status    StatusMessage
	lastError error

	lastMouseSelection ui.ChangeIdentity
	lastMouseClick     time.Time
	lastMouseBranch    int
	lastBranchClick    time.Time
	commands           commandTracker
}

func New(config Config) *Model {
	ctx := config.Context
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithCancel(ctx)
	logger := config.Logger
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(discardWriter{}, nil))
	}
	input := textinput.New()
	input.Prompt = ""
	input.CharLimit = 255
	input.SetVirtualCursor(true)
	model := &Model{
		ctx: ctx, cancel: cancel, repo: config.Repository, factory: config.RepositoryFactory,
		root: config.StartRoot, logger: logger, mode: ModeMain, focus: FocusRefresh,
		input: input, styles: ui.NewStyles(), lastError: config.InitialError,
	}
	if config.InitialError != nil {
		model.status = StatusMessage{Text: config.InitialError.Error(), Error: true}
	}
	return model
}

type discardWriter struct{}

func (discardWriter) Write(p []byte) (int, error) { return len(p), nil }

func (m *Model) Init() tea.Cmd {
	return batchCmd(m.requestRefresh(RefreshStartup), pollCmd())
}

func (m *Model) Close() {
	m.cancel()
	m.commands.closeAndWait()
}

type commandTracker struct {
	mu     sync.Mutex
	cond   *sync.Cond
	active int
	closed bool
}

func (t *commandTracker) begin() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return false
	}
	t.active++
	return true
}

func (t *commandTracker) done() {
	t.mu.Lock()
	t.active--
	if t.active == 0 && t.cond != nil {
		t.cond.Broadcast()
	}
	t.mu.Unlock()
}

func (t *commandTracker) closeAndWait() {
	t.mu.Lock()
	t.closed = true
	if t.cond == nil {
		t.cond = sync.NewCond(&t.mu)
	}
	for t.active > 0 {
		t.cond.Wait()
	}
	t.mu.Unlock()
}
