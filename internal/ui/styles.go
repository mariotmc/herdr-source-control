package ui

import (
	"os"

	"charm.land/lipgloss/v2"
	"charm.land/lipgloss/v2/compat"
	"github.com/mariotmc/herdr-source-control/internal/domain"
)

type Styles struct {
	Title         lipgloss.Style
	Muted         lipgloss.Style
	Divider       lipgloss.Style
	Button        lipgloss.Style
	ButtonFocused lipgloss.Style
	Selected      lipgloss.Style
	ModalBorder   lipgloss.Style
	ModalTitle    lipgloss.Style
	FooterKey     lipgloss.Style
	Error         lipgloss.Style
	Warning       lipgloss.Style
	Success       lipgloss.Style
	Info          lipgloss.Style
	Merge         lipgloss.Style
	Staged        lipgloss.Style
	Changes       lipgloss.Style
	Added         lipgloss.Style
	Modified      lipgloss.Style
	Deleted       lipgloss.Style
	Renamed       lipgloss.Style
	Conflict      lipgloss.Style
}

func NewStyles() Styles {
	styles := Styles{
		Title:         lipgloss.NewStyle().Bold(true),
		Muted:         lipgloss.NewStyle().Faint(true),
		Divider:       lipgloss.NewStyle().Faint(true),
		Button:        lipgloss.NewStyle().Bold(true).Padding(0, 1),
		ButtonFocused: lipgloss.NewStyle().Bold(true).Reverse(true).Padding(0, 1),
		Selected:      lipgloss.NewStyle().Reverse(true),
		ModalTitle:    lipgloss.NewStyle().Bold(true),
		FooterKey:     lipgloss.NewStyle().Bold(true),
		Error:         lipgloss.NewStyle().Bold(true),
		Warning:       lipgloss.NewStyle().Bold(true),
		Success:       lipgloss.NewStyle().Bold(true),
		Merge:         lipgloss.NewStyle().Bold(true),
		Staged:        lipgloss.NewStyle().Bold(true),
		Changes:       lipgloss.NewStyle().Bold(true),
		Added:         lipgloss.NewStyle().Bold(true),
		Modified:      lipgloss.NewStyle().Bold(true),
		Deleted:       lipgloss.NewStyle().Bold(true),
		Renamed:       lipgloss.NewStyle().Bold(true),
		Conflict:      lipgloss.NewStyle().Bold(true),
	}
	if os.Getenv("NO_COLOR") != "" {
		return styles
	}

	adaptive := func(light, dark string) compat.AdaptiveColor {
		return compat.AdaptiveColor{Light: lipgloss.Color(light), Dark: lipgloss.Color(dark)}
	}
	accent := adaptive("#34548A", "#7AA2F7")
	muted := adaptive("#6C6E75", "#7F849C")
	surface := adaptive("#DDE3F0", "#303446")
	selected := adaptive("#C9D5EC", "#3B4261")
	green := adaptive("#587539", "#9ECE6A")
	yellow := adaptive("#8C6C3E", "#E0AF68")
	red := adaptive("#8C4351", "#F7768E")
	cyan := adaptive("#33635C", "#7DCFFF")

	styles.Title = styles.Title.Foreground(accent)
	styles.Muted = styles.Muted.Foreground(muted)
	styles.Divider = styles.Divider.Foreground(muted)
	styles.Button = styles.Button.Foreground(accent).Background(surface)
	styles.ButtonFocused = styles.ButtonFocused.Reverse(false).Foreground(surface).Background(accent)
	styles.Selected = styles.Selected.Reverse(false).Background(selected)
	styles.ModalBorder = lipgloss.NewStyle().Foreground(accent)
	styles.ModalTitle = styles.ModalTitle.Foreground(accent)
	styles.FooterKey = styles.FooterKey.Foreground(accent)
	styles.Error = styles.Error.Foreground(red)
	styles.Warning = styles.Warning.Foreground(yellow)
	styles.Success = styles.Success.Foreground(green)
	styles.Info = lipgloss.NewStyle().Foreground(cyan)
	styles.Merge = styles.Merge.Foreground(red)
	styles.Staged = styles.Staged.Foreground(green)
	styles.Changes = styles.Changes.Foreground(yellow)
	styles.Added = styles.Added.Foreground(green)
	styles.Modified = styles.Modified.Foreground(yellow)
	styles.Deleted = styles.Deleted.Foreground(red)
	styles.Renamed = styles.Renamed.Foreground(cyan)
	styles.Conflict = styles.Conflict.Foreground(red)
	return styles
}

func (s Styles) ButtonText(text string, focused bool) string {
	if focused {
		return s.ButtonFocused.Render(text)
	}
	return s.Button.Render(text)
}

func (s Styles) Group(group domain.Group) lipgloss.Style {
	switch group {
	case domain.GroupMerge:
		return s.Merge
	case domain.GroupStaged:
		return s.Staged
	default:
		return s.Changes
	}
}

func (s Styles) Status(status domain.Status, untracked bool) lipgloss.Style {
	if untracked || status == domain.StatusAdded {
		return s.Added
	}
	switch status {
	case domain.StatusModified, domain.StatusTypeChanged:
		return s.Modified
	case domain.StatusDeleted:
		return s.Deleted
	case domain.StatusRenamed, domain.StatusCopied:
		return s.Renamed
	case domain.StatusUnmerged:
		return s.Conflict
	default:
		return s.Muted
	}
}
