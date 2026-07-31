package ui

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/mariotmc/herdr-source-control/internal/domain"
)

func TestRowsProjectsGroupsAndRawSelectionIdentity(t *testing.T) {
	rows := Rows([]domain.Change{
		{Path: []byte("mixed"), IndexStatus: domain.StatusAdded, WorktreeStatus: domain.StatusModified},
		{Path: []byte("conflict"), Conflicted: true},
		{Path: []byte("new"), Untracked: true},
	})
	selectable := Selectable(rows)
	if len(selectable) != 4 {
		t.Fatalf("got %d selectable rows, want 4", len(selectable))
	}
	if selectable[0].Group != domain.GroupMerge || selectable[1].Group != domain.GroupStaged {
		t.Fatalf("unexpected group order: %#v", selectable)
	}
	if selectable[1].Path != "mixed" || selectable[2].Path != "mixed" {
		t.Fatalf("mixed path was not projected twice: %#v", selectable)
	}
}

func TestEscapeAndTruncateAreTerminalSafe(t *testing.T) {
	escaped := EscapeBytes([]byte{'a', 0x1b, '\n', '\\', 0xff})
	if strings.ContainsRune(escaped, 0x1b) || escaped != `a\x1B\x0A\\\xFF` {
		t.Fatalf("unexpected escaped value %q", escaped)
	}
	for _, value := range []string{"component/very-long-file.go", "a界b界c", "e\u0301/filename"} {
		got := Truncate(value, 10)
		if lipgloss.Width(got) > 10 {
			t.Fatalf("%q has width %d", got, lipgloss.Width(got))
		}
	}
}

func TestTruncatePreservesBothEnds(t *testing.T) {
	if got := Truncate("first/component/file.go", 17); !strings.HasPrefix(got, "first/") || !strings.HasSuffix(got, "file.go") {
		t.Fatalf("Truncate() = %q", got)
	}
}

func TestUnknownStatusLabelPreservesRawCode(t *testing.T) {
	resource := domain.Resource{Status: domain.StatusUnknown, RawStatus: 'X'}
	if got := StatusLabel(resource, false); got != "Unknown (X)" {
		t.Fatalf("StatusLabel() = %q", got)
	}
}

func TestClassifyResponsiveSizes(t *testing.T) {
	cases := []struct {
		width, height int
		want          Size
	}{
		{39, 30, SizeMinimum}, {50, 16, SizeSmall}, {80, 24, SizeNarrow}, {120, 35, SizeWide},
	}
	for _, test := range cases {
		if got := Classify(test.width, test.height); got != test.want {
			t.Errorf("Classify(%d, %d) = %v, want %v", test.width, test.height, got, test.want)
		}
	}
}
