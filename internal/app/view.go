package app

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/mariotmc/herdr-source-control/internal/domain"
	"github.com/mariotmc/herdr-source-control/internal/ui"
)

func (m *Model) View() tea.View {
	content := m.renderMain()
	if m.mode != ModeMain && ui.Classify(m.width, m.height) != ui.SizeMinimum {
		content = m.renderModal(content)
	}
	view := tea.NewView(content)
	view.AltScreen = true
	view.ReportFocus = true
	view.MouseMode = tea.MouseModeCellMotion
	targets := m.mouseTargets()
	view.OnMouse = func(message tea.MouseMsg) tea.Cmd {
		mouse := message.Mouse()
		for index := len(targets) - 1; index >= 0; index-- {
			target := targets[index]
			if !target.Contains(mouse.X, mouse.Y) {
				continue
			}
			action := target.Message
			switch mouse.Button {
			case tea.MouseWheelUp:
				if action.Action != mouseScrollChanges {
					continue
				}
				action.Delta = -3
			case tea.MouseWheelDown:
				if action.Action != mouseScrollChanges {
					continue
				}
				action.Delta = 3
			case tea.MouseLeft:
				if action.Action == mouseScrollChanges {
					return nil
				}
				action.When = time.Now()
			default:
				return nil
			}
			return func() tea.Msg { return action }
		}
		return nil
	}
	return view
}

type mouseTarget struct {
	X, Y, Width, Height int
	Message             mouseActionMsg
}

func (t mouseTarget) Contains(x, y int) bool {
	return x >= t.X && x < t.X+t.Width && y >= t.Y && y < t.Y+t.Height
}

func (m *Model) mouseTargets() []mouseTarget {
	if ui.Classify(m.width, m.height) == ui.SizeMinimum {
		return nil
	}
	if m.mode == ModeBranches {
		return m.branchMouseTargets()
	}
	if m.mode == ModeCreateBranch {
		return m.createMouseTargets()
	}
	if m.mode == ModeHelp {
		return nil
	}

	size := ui.Classify(m.width, m.height)
	refreshWidth := len("[Refresh]")
	if size == ui.SizeSmall {
		refreshWidth = len("[R]")
	}
	targets := []mouseTarget{{X: max(0, m.width-refreshWidth-1), Y: 0, Width: refreshWidth + 1, Height: 1, Message: mouseActionMsg{Action: mouseActivate, Target: FocusRefresh}}}
	if m.snapshot == nil {
		return targets
	}
	branchY, syncY, bodyY := 3, 3, 5
	if size == ui.SizeNarrow {
		syncY, bodyY = 4, 6
	} else if size == ui.SizeSmall {
		branchY, syncY, bodyY = 2, 3, 5
	}
	targets = append(targets,
		mouseTarget{X: 0, Y: branchY, Width: m.width, Height: 1, Message: mouseActionMsg{Action: mouseActivate, Target: FocusBranch}},
		mouseTarget{X: max(0, m.width-8), Y: syncY, Width: 8, Height: 1, Message: mouseActionMsg{Action: mouseActivate, Target: FocusSync}},
	)
	bodyHeight := max(1, m.height-bodyY-2)
	targets = append(targets, mouseTarget{X: 0, Y: bodyY, Width: m.width, Height: bodyHeight, Message: mouseActionMsg{Action: mouseScrollChanges}})
	start := min(m.scrollOffset, max(0, len(m.rows)-bodyHeight))
	end := min(len(m.rows), start+bodyHeight)
	for index, row := range m.rows[start:end] {
		if row.Heading {
			continue
		}
		targets = append(targets, mouseTarget{X: 0, Y: bodyY + index, Width: m.width, Height: 1, Message: mouseActionMsg{Action: mouseSelectChange, Identity: row.Identity}})
	}
	return targets
}

func (m *Model) branchMouseTargets() []mouseTarget {
	width := min(76, max(36, m.width-4))
	height := min(22, max(8, m.height-4))
	left, top := max(0, (m.width-width)/2), max(0, (m.height-height)/2)
	inner := width - 4
	capacity := max(1, (height-6)/2)
	start := 0
	if m.branchIndex > capacity {
		start = m.branchIndex - capacity
	}
	end := min(len(m.filtered), start+capacity)
	targets := []mouseTarget{{X: left + 1, Y: top + 3, Width: inner, Height: 1, Message: mouseActionMsg{Action: mouseOpenCreate}}}
	for index := range m.filtered[start:end] {
		targets = append(targets, mouseTarget{X: left + 1, Y: top + 4 + index*2, Width: inner, Height: 2, Message: mouseActionMsg{Action: mouseSelectBranch, Index: start + index + 1}})
	}
	footerY := top + 4 + (end-start)*2
	targets = append(targets, mouseTarget{X: left + width - 11, Y: footerY, Width: 9, Height: 1, Message: mouseActionMsg{Action: mouseCancelModal}})
	return targets
}

func (m *Model) createMouseTargets() []mouseTarget {
	width := min(76, max(36, m.width-4))
	height := min(22, max(8, m.height-4))
	left, top := max(0, (m.width-width)/2), max(0, (m.height-height)/2)
	buttonY := top + 9
	if height < 11 {
		buttonY = top + 5
	}
	return []mouseTarget{
		{X: left + width - 21, Y: buttonY, Width: 10, Height: 1, Message: mouseActionMsg{Action: mouseSubmitCreate}},
		{X: left + width - 11, Y: buttonY, Width: 9, Height: 1, Message: mouseActionMsg{Action: mouseCancelModal}},
	}
}

func (m *Model) renderMain() string {
	width, height := max(1, m.width), max(1, m.height)
	size := ui.Classify(width, height)
	if size == ui.SizeMinimum {
		return fitScreen([]string{" Source Control", "", " Terminal too small", " Resize to at least 40x12.", "", " q: close"}, width, height)
	}
	compact := size != ui.SizeWide
	small := size == ui.SizeSmall
	refresh := "[Refresh]"
	if small {
		refresh = "[R]"
	}
	if m.focus == FocusRefresh {
		refresh = ">" + refresh
	}
	lines := []string{spread(" Source Control", refresh, width), " " + ui.Truncate(ui.EscapeText(ui.DisplayRoot(m.root)), width-2)}
	if m.snapshot != nil {
		branch, upstream, counts := branchLines(m.snapshot.Branch, compact)
		branchControl := "[" + branch + "]"
		if small {
			branchControl = branch
		}
		if m.focus == FocusBranch {
			branchControl = ">" + branchControl
		}
		sync := "[Sync]"
		if small {
			sync = "[S]"
		}
		if m.focus == FocusSync {
			sync = ">" + sync
		}
		if size == ui.SizeWide {
			lines = append(lines, "", spread(" "+branchControl+"  "+upstream+"  "+counts, sync, width), "")
		} else if size == ui.SizeNarrow {
			lines = append(lines, "", " "+ui.Truncate(branchControl, width-2), spread(" "+upstream+"  "+counts, sync, width), "")
		} else {
			lines = append(lines, " "+ui.Truncate(branchControl, width-2), spread(" "+counts, sync, width), "")
		}
	} else {
		lines = append(lines, "")
	}
	bodyHeight := max(1, height-len(lines)-2)
	lines = append(lines, m.renderBody(size, bodyHeight)...)
	help := " Tab focus  Enter activate  b branches  s sync  r refresh  ? help  q close"
	if size == ui.SizeNarrow {
		help = " b branches  s sync  r refresh  ? help"
	} else if size == ui.SizeSmall {
		help = " b branch  ? help"
	}
	lines = append(lines, ui.Truncate(help, width), ui.Truncate(" "+m.statusLine(), width))
	return fitScreen(lines, width, height)
}

func (m *Model) renderBody(size ui.Size, height int) []string {
	if m.snapshot == nil {
		if m.lastError != nil {
			message := m.lastError.Error()
			switch message {
			case "No Git repository found.":
				return clipLines([]string{" No Git repository found", " Source Control could not find a working tree from:", " " + ui.EscapeText(m.root), "", " Initialize or open a repository, then refresh.", " [Refresh]"}, height)
			case "Git is not installed or not in PATH.":
				return clipLines([]string{" Git is not available", " Install Git and ensure \"git\" is on PATH, then refresh.", " [Refresh]"}, height)
			default:
				return clipLines([]string{" Repository unavailable", " " + message, "", " [Refresh]"}, height)
			}
		}
		return clipLines([]string{" Loading repository..."}, height)
	}
	if len(m.rows) == 0 {
		return clipLines([]string{" Working tree clean", " No staged, modified, conflicting, or untracked files."}, height)
	}
	start := min(m.scrollOffset, max(0, len(m.rows)-height))
	end := min(len(m.rows), start+height)
	lines := make([]string, 0, height)
	for _, row := range m.rows[start:end] {
		if row.Heading {
			label := strings.ToUpper(row.Group.Label())
			if size == ui.SizeSmall {
				switch row.Group {
				case domain.GroupMerge:
					label = "MERGE"
				case domain.GroupStaged:
					label = "STAGED"
				default:
					label = "CHANGES"
				}
			}
			lines = append(lines, fmt.Sprintf(" %s (%d)", label, row.Count))
			continue
		}
		selected := "  "
		if row.Identity == m.selected && m.focus == FocusChanges {
			selected = "> "
		}
		compact := size != ui.SizeWide
		status := ui.StatusLabel(row.Resource, compact)
		statusWidth := 12
		if compact {
			statusWidth = 2
		}
		prefix := selected + fmt.Sprintf("%-*s", statusWidth, status)
		path := ui.Truncate(ui.DisplayPath(row.Resource), max(1, m.width-lipgloss.Width(prefix)-1))
		lines = append(lines, prefix+path)
	}
	return clipLines(lines, height)
}

func (m *Model) renderModal(background string) string {
	width := min(76, max(36, m.width-4))
	height := min(22, max(8, m.height-4))
	var modal string
	switch m.mode {
	case ModeBranches:
		modal = m.renderBranchesModal(width, height)
	case ModeCreateBranch:
		modal = m.renderCreateModal(width, height)
	case ModeHelp:
		modal = m.renderHelpModal(width, height)
	}
	_ = background
	return lipgloss.Place(max(1, m.width), max(1, m.height), lipgloss.Center, lipgloss.Center, modal)
}

func (m *Model) renderBranchesModal(width, height int) string {
	inner := width - 4
	lines := []string{centerTitle("Switch Branch", inner), " Search: " + ui.Truncate(m.input.View(), inner-9)}
	createMarker := "  "
	if m.branchIndex == 0 {
		createMarker = "> "
	}
	lines = append(lines, createMarker+"+ Create new branch...")
	capacity := max(1, (height-6)/2)
	start := 0
	if m.branchIndex > capacity {
		start = m.branchIndex - capacity
	}
	end := min(len(m.filtered), start+capacity)
	for index, branch := range m.filtered[start:end] {
		name := ui.EscapeText(branch.Name)
		if branch.Current {
			name += " (current)"
		} else if branch.WorktreePath != "" {
			name += " (in " + ui.EscapeText(branch.WorktreePath) + ")"
		}
		age := relativeAge(branch.CommitTime)
		first := spread(name, age, inner-2)
		second := ui.EscapeText(branch.Author) + "  " + shortOID(branch.OID) + "  " + ui.EscapeText(branch.Subject)
		marker := "  "
		if start+index+1 == m.branchIndex {
			marker = "> "
		}
		lines = append(lines, marker+ui.Truncate(first, inner-2), "  "+ui.Truncate(second, inner-2))
	}
	footer := "[Cancel]"
	if m.mutation == OperationCheckout {
		footer = "| checkout in progress..."
	}
	lines = append(lines, spread(fmt.Sprintf(" %d branches", len(m.filtered)), footer, inner))
	return box(lines, width, height)
}

func (m *Model) renderCreateModal(width, height int) string {
	inner := width - 4
	create, cancel := "[Create]", "[Cancel]"
	if m.createFocus == 1 {
		create = ">" + create
	}
	if m.createFocus == 2 {
		cancel = ">" + cancel
	}
	errorLine := ""
	if m.status.Error {
		errorLine = m.status.Text
	} else if m.mutation == OperationCreateBranch {
		errorLine = "| create branch in progress..."
	}
	lines := []string{
		centerTitle("Create Branch", inner), " New branch name", " " + ui.Truncate(m.input.View(), inner-1), "",
		" Creates from current HEAD and switches to the branch.", "", " " + ui.Truncate(errorLine, inner-1), "",
		spread("", create+" "+cancel, inner),
	}
	if height < 11 {
		lines = []string{
			centerTitle("Create Branch", inner), " New branch name", " " + ui.Truncate(m.input.View(), inner-1),
			" " + ui.Truncate(errorLine, inner-1), spread("", create+" "+cancel, inner),
		}
	}
	return box(lines, width, height)
}

func (m *Model) renderHelpModal(width, height int) string {
	content := []string{
		" Files are read-only. Enter shows a notice; it does not open a diff.",
		"",
		" b branches   s sync   r refresh   ? help   q close",
		" Up/Down or j/k moves through files. Tab moves between controls.",
		" +N means commits ahead/pushable; -N means behind/pullable.",
		"",
		" Sync fetches, applies only fast-forward updates, then non-force pushes",
		" the verified commit to the exact configured upstream ref.",
	}
	available := max(1, height-4)
	maxOffset := max(0, len(content)-available)
	offset := min(m.helpOffset, maxOffset)
	end := min(len(content), offset+available)
	lines := []string{centerTitle("Help", width-4)}
	lines = append(lines, content[offset:end]...)
	lines = append(lines, " Esc closes help.  Up/Down scroll.")
	return box(lines, width, height)
}

func branchLines(branch domain.BranchState, compact bool) (string, string, string) {
	name := "Branch: " + ui.EscapeText(branch.Name)
	if branch.State == domain.HeadDetached {
		name = "Detached at " + shortOID(branch.OID)
	} else if branch.State == domain.HeadUnborn {
		name += " (no commits yet)"
	}
	upstream := "No upstream"
	if branch.Upstream != "" {
		upstream = ui.EscapeText(branch.Upstream)
		if !branch.CountsKnown {
			upstream = "Upstream unavailable"
		}
	}
	counts := ""
	if branch.CountsKnown {
		if compact {
			counts = fmt.Sprintf("+%d -%d", branch.Ahead, branch.Behind)
		} else {
			switch {
			case branch.Ahead == 0 && branch.Behind == 0:
				counts = "Up to date"
			case branch.Ahead > 0 && branch.Behind > 0:
				counts = fmt.Sprintf("Ahead %d, behind %d", branch.Ahead, branch.Behind)
			case branch.Ahead > 0:
				counts = fmt.Sprintf("Ahead %d", branch.Ahead)
			default:
				counts = fmt.Sprintf("Behind %d", branch.Behind)
			}
		}
	}
	return name, upstream, counts
}

func (m *Model) statusLine() string {
	if m.status.Error && m.status.Text != "" {
		return m.status.Text
	}
	if m.mutation != OperationNone {
		return "| " + operationName(m.mutation) + " in progress..."
	}
	if m.refreshBusy {
		return "| Refreshing repository..."
	}
	if m.stale {
		return "Repository status is stale."
	}
	if m.status.Text != "" && (m.status.Expires.IsZero() || time.Now().Before(m.status.Expires)) {
		return m.status.Text
	}
	return "Ready"
}

func fitScreen(lines []string, width, height int) string {
	lines = clipLines(lines, height)
	for len(lines) < height {
		lines = append(lines, "")
	}
	for index := range lines {
		lines[index] = ui.Truncate(lines[index], width)
	}
	return strings.Join(lines, "\n")
}

func clipLines(lines []string, height int) []string {
	if len(lines) > height {
		return lines[:height]
	}
	return lines
}

func spread(left, right string, width int) string {
	space := width - lipgloss.Width(left) - lipgloss.Width(right)
	if space < 1 {
		left = ui.Truncate(left, max(0, width-lipgloss.Width(right)-1))
		space = max(1, width-lipgloss.Width(left)-lipgloss.Width(right))
	}
	return left + strings.Repeat(" ", space) + right
}

func box(lines []string, width, height int) string {
	inner := max(1, width-2)
	lines = clipLines(lines, max(1, height-2))
	for len(lines) < height-2 {
		lines = append(lines, "")
	}
	var output []string
	output = append(output, "+"+strings.Repeat("-", inner)+"+")
	for _, line := range lines {
		line = ui.Truncate(line, inner)
		output = append(output, "|"+line+strings.Repeat(" ", max(0, inner-lipgloss.Width(line)))+"|")
	}
	output = append(output, "+"+strings.Repeat("-", inner)+"+")
	return strings.Join(output, "\n")
}

func centerTitle(title string, width int) string {
	return strings.Repeat(" ", max(0, (width-lipgloss.Width(title))/2)) + title
}

func shortOID(oid string) string {
	if len(oid) > 8 {
		oid = oid[:8]
	}
	return ui.EscapeText(oid)
}

func relativeAge(then time.Time) string {
	if then.IsZero() {
		return ""
	}
	duration := time.Since(then)
	switch {
	case duration < time.Minute:
		return "just now"
	case duration < time.Hour:
		return fmt.Sprintf("%d minutes ago", int(duration.Minutes()))
	case duration < 24*time.Hour:
		return fmt.Sprintf("%d hours ago", int(duration.Hours()))
	default:
		return fmt.Sprintf("%d days ago", int(duration.Hours()/24))
	}
}
