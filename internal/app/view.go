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
		return fitScreen([]string{m.styles.Title.Render(" SOURCE CONTROL"), "", m.styles.Warning.Render(" Terminal too small"), m.styles.Muted.Render(" Resize to at least 40x12."), "", " q: close"}, width, height)
	}
	compact := size != ui.SizeWide
	small := size == ui.SizeSmall
	refresh := "Refresh"
	if small {
		refresh = "R"
	}
	refresh = m.styles.ButtonText(refresh, m.focus == FocusRefresh)
	title := m.styles.Title.Render(" SOURCE CONTROL")
	root := " " + m.styles.Muted.Render(ui.Truncate(ui.EscapeText(ui.DisplayRoot(m.root)), width-2))
	lines := []string{spread(title, refresh, width), root}
	if !small {
		lines = append(lines, m.divider(width))
	}
	if m.snapshot != nil {
		branch, upstreamPlain, countsPlain := branchLines(m.snapshot.Branch, compact)
		syncLabel := "Sync"
		if small {
			syncLabel = "S"
		}
		sync := m.styles.ButtonText(syncLabel, m.focus == FocusSync)
		upstream := m.styles.Muted.Render(upstreamPlain)
		counts := m.branchState(countsPlain, m.snapshot.Branch)
		if size == ui.SizeWide {
			reserved := lipgloss.Width(upstream) + lipgloss.Width(counts) + lipgloss.Width(sync) + 7
			branch = ui.Truncate(branch, max(12, width-reserved))
			branchControl := m.styles.ButtonText(branch, m.focus == FocusBranch)
			lines = append(lines, spread(" "+branchControl+"  "+upstream+"  "+counts, sync, width), m.divider(width))
		} else if size == ui.SizeNarrow {
			branch = ui.Truncate(branch, width-4)
			branchControl := m.styles.ButtonText(branch, m.focus == FocusBranch)
			metaWidth := max(1, width-lipgloss.Width(sync)-3)
			meta := ui.Truncate(upstreamPlain+"  "+countsPlain, metaWidth)
			lines = append(lines, " "+branchControl, spread(" "+m.styles.Muted.Render(meta), sync, width), m.divider(width))
		} else {
			branch = ui.Truncate(branch, width-4)
			branchControl := m.styles.ButtonText(branch, m.focus == FocusBranch)
			lines = append(lines, " "+branchControl, spread(" "+counts, sync, width), m.divider(width))
		}
	} else {
		if small {
			lines = append(lines, m.divider(width))
		}
	}
	bodyHeight := max(1, height-len(lines)-2)
	lines = append(lines, m.renderBody(size, bodyHeight)...)
	lines = append(lines, m.helpLine(size, width), m.statusFooter(size, width))
	return fitScreen(lines, width, height)
}

func (m *Model) renderBody(size ui.Size, height int) []string {
	if m.snapshot == nil {
		if m.lastError != nil {
			message := m.lastError.Error()
			switch message {
			case "No Git repository found.":
				return clipLines([]string{m.styles.Warning.Render(" No Git repository found"), m.styles.Muted.Render(" Source Control could not find a working tree from:"), " " + ui.EscapeText(m.root), "", " Initialize or open a repository, then refresh.", " " + m.styles.ButtonText("Refresh", m.focus == FocusRefresh)}, height)
			case "Git is not installed or not in PATH.":
				return clipLines([]string{m.styles.Error.Render(" Git is not available"), m.styles.Muted.Render(" Install Git and ensure \"git\" is on PATH, then refresh."), " " + m.styles.ButtonText("Refresh", m.focus == FocusRefresh)}, height)
			default:
				return clipLines([]string{m.styles.Error.Render(" Repository unavailable"), " " + message, "", " " + m.styles.ButtonText("Refresh", m.focus == FocusRefresh)}, height)
			}
		}
		return clipLines([]string{m.styles.Info.Render(" | Loading repository...")}, height)
	}
	if len(m.rows) == 0 {
		return clipLines([]string{m.styles.Success.Render(" ✓ Working tree clean"), m.styles.Muted.Render("   No staged, modified, conflicting, or untracked files.")}, height)
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
			heading := m.styles.Group(row.Group).Render(" " + label)
			count := m.styles.Muted.Render(fmt.Sprintf("  %d", row.Count))
			remaining := max(0, m.width-lipgloss.Width(heading)-lipgloss.Width(count)-2)
			lines = append(lines, heading+count+"  "+m.styles.Divider.Render(strings.Repeat("─", remaining)))
			continue
		}
		selected := "  "
		if row.Identity == m.selected && m.focus == FocusChanges {
			selected = "› "
		}
		status := ui.StatusLabel(row.Resource, true)
		status = m.styles.Status(row.Resource.Status, row.Resource.Untracked).Render(fmt.Sprintf("%-2s", status))
		prefix := selected + status + " "
		path := ui.Truncate(ui.DisplayPath(row.Resource), max(1, m.width-lipgloss.Width(prefix)-1))
		line := prefix + path
		if row.Identity == m.selected && m.focus == FocusChanges {
			line = m.styles.Selected.Width(m.width).Render(line)
		}
		lines = append(lines, line)
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
	lines := []string{centerTitle(m.styles.ModalTitle.Render("Switch Branch"), inner), m.styles.Muted.Render(" Search") + "  " + ui.Truncate(m.input.View(), inner-10)}
	createMarker := "  "
	if m.branchIndex == 0 {
		createMarker = "› "
	}
	createLine := createMarker + "+ Create new branch..."
	if m.branchIndex == 0 {
		createLine = m.styles.Selected.Width(inner).Render(createLine)
	}
	lines = append(lines, createLine)
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
			marker = "› "
		}
		firstLine := marker + ui.Truncate(first, inner-2)
		secondLine := "  " + m.styles.Muted.Render(ui.Truncate(second, inner-2))
		if start+index+1 == m.branchIndex {
			firstLine = m.styles.Selected.Width(inner).Render(firstLine)
		}
		lines = append(lines, firstLine, secondLine)
	}
	footer := m.styles.ButtonText("Cancel", false)
	if m.mutation == OperationCheckout {
		footer = m.styles.Info.Render("| checkout in progress...")
	}
	lines = append(lines, spread(m.styles.Muted.Render(fmt.Sprintf(" %d branches", len(m.filtered))), footer, inner))
	return box(lines, width, height, m.styles.ModalBorder)
}

func (m *Model) renderCreateModal(width, height int) string {
	inner := width - 4
	create := m.styles.ButtonText("Create", m.createFocus == 1)
	cancel := m.styles.ButtonText("Cancel", m.createFocus == 2)
	errorLine := ""
	if m.status.Error {
		errorLine = m.status.Text
	} else if m.mutation == OperationCreateBranch {
		errorLine = "| create branch in progress..."
	}
	if m.status.Error {
		errorLine = m.styles.Error.Render(errorLine)
	} else {
		errorLine = m.styles.Info.Render(errorLine)
	}
	lines := []string{
		centerTitle(m.styles.ModalTitle.Render("Create Branch"), inner), m.styles.Muted.Render(" New branch name"), " " + ui.Truncate(m.input.View(), inner-1), "",
		m.styles.Muted.Render(" Creates from current HEAD and switches to the branch."), "", " " + errorLine, "",
		spread("", create+" "+cancel, inner),
	}
	if height < 11 {
		lines = []string{
			centerTitle(m.styles.ModalTitle.Render("Create Branch"), inner), m.styles.Muted.Render(" New branch name"), " " + ui.Truncate(m.input.View(), inner-1),
			" " + errorLine, spread("", create+" "+cancel, inner),
		}
	}
	return box(lines, width, height, m.styles.ModalBorder)
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
	lines := []string{centerTitle(m.styles.ModalTitle.Render("Help"), width-4)}
	lines = append(lines, content[offset:end]...)
	lines = append(lines, m.styles.Muted.Render(" Esc closes help.  Up/Down scroll."))
	return box(lines, width, height, m.styles.ModalBorder)
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
			counts = fmt.Sprintf("↑%d ↓%d", branch.Ahead, branch.Behind)
		} else {
			switch {
			case branch.Ahead == 0 && branch.Behind == 0:
				counts = "✓ Up to date"
			case branch.Ahead > 0 && branch.Behind > 0:
				counts = fmt.Sprintf("↑ Ahead %d  ↓ Behind %d", branch.Ahead, branch.Behind)
			case branch.Ahead > 0:
				counts = fmt.Sprintf("↑ Ahead %d", branch.Ahead)
			default:
				counts = fmt.Sprintf("↓ Behind %d", branch.Behind)
			}
		}
	}
	return name, upstream, counts
}

func (m *Model) divider(width int) string {
	return m.styles.Divider.Render(strings.Repeat("─", max(0, width)))
}

func (m *Model) branchState(text string, branch domain.BranchState) string {
	if text == "" {
		return ""
	}
	if branch.CountsKnown && branch.Ahead == 0 && branch.Behind == 0 {
		return m.styles.Success.Render(text)
	}
	if branch.Behind > 0 {
		return m.styles.Warning.Render(text)
	}
	return m.styles.Info.Render(text)
}

func (m *Model) helpLine(size ui.Size, width int) string {
	hint := func(key, label string) string {
		return m.styles.FooterKey.Render(key) + m.styles.Muted.Render(" "+label)
	}
	var hints []string
	switch size {
	case ui.SizeWide:
		hints = []string{hint("Tab", "focus"), hint("Enter", "open"), hint("b", "branch"), hint("s", "sync"), hint("r", "refresh"), hint("?", "help"), hint("q", "close")}
	case ui.SizeNarrow:
		hints = []string{hint("b", "branch"), hint("s", "sync"), hint("r", "refresh"), hint("?", "help")}
	default:
		hints = []string{hint("b", "branch"), hint("?", "help"), hint("q", "close")}
	}
	return lipgloss.NewStyle().MaxWidth(width).Render(" " + strings.Join(hints, m.styles.Muted.Render("   ")))
}

func (m *Model) statusFooter(size ui.Size, width int) string {
	text := m.statusLine()
	style := m.styles.Info
	switch {
	case m.status.Error:
		style = m.styles.Error
	case m.stale:
		style = m.styles.Warning
	case text == "Ready":
		style = m.styles.Success
	}
	if text == "Ready" && size != ui.SizeSmall {
		text += " · " + m.fetchStatus()
	}
	left := " " + style.Render(text)
	if size == ui.SizeSmall {
		return lipgloss.NewStyle().MaxWidth(width).Render(left)
	}
	right := m.styles.Muted.Render("refresh 2s · fetch 3m ")
	return spread(left, right, width)
}

func (m *Model) fetchStatus() string {
	switch {
	case m.lastFetchFailed && !m.lastFetchAttempt.IsZero():
		return "check failed " + fetchAge(time.Since(m.lastFetchAttempt))
	case !m.lastFetchSuccess.IsZero():
		return "checked " + fetchAge(time.Since(m.lastFetchSuccess))
	default:
		return "not checked yet"
	}
}

func fetchAge(duration time.Duration) string {
	switch {
	case duration < time.Minute:
		return "just now"
	case duration < time.Hour:
		return fmt.Sprintf("%dm ago", int(duration.Minutes()))
	case duration < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(duration.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(duration.Hours()/24))
	}
}

func (m *Model) statusLine() string {
	if m.status.Error && m.status.Text != "" {
		return m.status.Text
	}
	if m.mutation != OperationNone {
		return "| " + operationName(m.mutation) + " in progress..."
	}
	if m.refreshBusy && m.refreshVisible {
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
		lines[index] = lipgloss.NewStyle().MaxWidth(width).Render(lines[index])
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

func box(lines []string, width, height int, border lipgloss.Style) string {
	inner := max(1, width-2)
	lines = clipLines(lines, max(1, height-2))
	for len(lines) < height-2 {
		lines = append(lines, "")
	}
	var output []string
	output = append(output, border.Render("╭"+strings.Repeat("─", inner)+"╮"))
	for _, line := range lines {
		line = lipgloss.NewStyle().MaxWidth(inner).Render(line)
		output = append(output, border.Render("│")+line+strings.Repeat(" ", max(0, inner-lipgloss.Width(line)))+border.Render("│"))
	}
	output = append(output, border.Render("╰"+strings.Repeat("─", inner)+"╯"))
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
