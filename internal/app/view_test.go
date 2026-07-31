package app

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/mariotmc/herdr-source-control/internal/domain"
)

func TestResponsiveRendering(t *testing.T) {
	snapshot := attachedSnapshot(2, 1)
	snapshot.Changes = []domain.Change{{Path: []byte("internal/git/status.go"), IndexStatus: domain.StatusAdded}}
	cases := []struct {
		width, height int
		contains      []string
	}{
		{120, 35, []string{"STAGED CHANGES  1", "A  internal/git/status.go", "↑ Ahead 2  ↓ Behind 1"}},
		{80, 24, []string{"STAGED CHANGES  1", "A  internal/git/status.go", "↑2 ↓1"}},
		{50, 16, []string{"STAGED  1", "SOURCE CONTROL", "Branch: main"}},
		{39, 11, []string{"Terminal too small", "Resize to at least 40x12."}},
	}
	for _, test := range cases {
		model := New(Config{StartRoot: "/repo"})
		model.width, model.height = test.width, test.height
		model.publishSnapshot(snapshot)
		view := model.View()
		if !view.AltScreen || !view.ReportFocus || view.MouseMode == 0 {
			t.Fatalf("view options missing at %dx%d", test.width, test.height)
		}
		content := ansi.Strip(view.Content)
		for _, expected := range test.contains {
			if !strings.Contains(content, expected) {
				t.Errorf("%dx%d missing %q:\n%s", test.width, test.height, expected, view.Content)
			}
		}
	}
}

func TestReadOnlyActivationNoticeAndHelpContent(t *testing.T) {
	model := New(Config{})
	model.rows = nil
	model.activate(FocusChanges)
	if model.status.Text != "Diff view is not available yet." {
		t.Fatalf("status = %q", model.status.Text)
	}
	model.width, model.height, model.mode = 80, 24, ModeHelp
	view := ansi.Strip(model.View().Content)
	for _, expected := range []string{"Files are read-only", "fast-forward", "non-force pushes"} {
		if !strings.Contains(view, expected) {
			t.Errorf("help missing %q", expected)
		}
	}
}

func TestBranchModalKeepsSelectedPageVisible(t *testing.T) {
	model := New(Config{})
	model.width, model.height, model.mode = 80, 16, ModeBranches
	for index := range 12 {
		model.filtered = append(model.filtered, domain.LocalBranch{Name: fmt.Sprintf("branch-%02d", index)})
	}
	model.branchIndex = 12
	view := ansi.Strip(model.View().Content)
	if !strings.Contains(view, "› branch-11") || !strings.Contains(view, "+ Create new branch...") {
		t.Fatalf("selected branch page is not visible:\n%s", view)
	}
}

func TestRepositoryRootIsEscaped(t *testing.T) {
	model := New(Config{StartRoot: "/repo\x1b[2J\nname"})
	model.width, model.height = 80, 24
	view := ansi.Strip(model.View().Content)
	if strings.Contains(view, "\x1b") || strings.Contains(view, "\nname") || !strings.Contains(view, `\x1B[2J\x0Aname`) {
		t.Fatalf("root was not escaped: %q", view)
	}
}

func TestMouseRefreshTargetActivatesRefresh(t *testing.T) {
	repository := &fakeRepository{snapshots: []domain.Snapshot{{Root: "/repo"}}}
	model := New(Config{Repository: repository, StartRoot: "/repo"})
	model.width, model.height = 80, 24
	view := model.View()
	command := view.OnMouse(tea.MouseClickMsg{X: 75, Y: 0, Button: tea.MouseLeft})
	message := execute(t, command)
	command = update(t, model, message)
	update(t, model, execute(t, command))
	if repository.snapshotCalls != 1 {
		t.Fatalf("refresh calls = %d", repository.snapshotCalls)
	}
}

func TestMinimumSizeSuppressesModal(t *testing.T) {
	model := New(Config{})
	model.width, model.height, model.mode = 39, 11, ModeBranches
	view := ansi.Strip(model.View().Content)
	if !strings.Contains(view, "Terminal too small") || strings.Contains(view, "Switch Branch") {
		t.Fatalf("minimum view was replaced by modal:\n%s", view)
	}
}

func TestShortOIDIsTerminalSafe(t *testing.T) {
	if got := shortOID("abc\x1b[2Jdef"); strings.ContainsRune(got, '\x1b') {
		t.Fatalf("shortOID() = %q", got)
	}
}

func TestBackgroundPollDoesNotReplaceReadyStatus(t *testing.T) {
	model := New(Config{Repository: &fakeRepository{}, StartRoot: "/repo"})
	model.publishSnapshot(attachedSnapshot(0, 0))
	model.requestRefresh(RefreshPoll)
	if got := model.statusLine(); got != "Ready" {
		t.Fatalf("poll status = %q", got)
	}
	model.refreshBusy = false
	model.requestRefresh(RefreshManual)
	if got := model.statusLine(); got != "| Refreshing repository..." {
		t.Fatalf("manual refresh status = %q", got)
	}
}
