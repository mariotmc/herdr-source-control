package domain

import "testing"

func TestProjectChanges(t *testing.T) {
	changes := []Change{
		{Path: []byte("z.txt"), IndexStatus: StatusModified, WorktreeStatus: StatusModified},
		{Path: []byte("a.txt"), Untracked: true},
		{Path: []byte("conflict.txt"), Conflicted: true, IndexStatus: StatusUnmerged, WorktreeStatus: StatusUnmerged},
	}

	groups := ProjectChanges(changes)
	if got := len(groups[GroupMerge]); got != 1 {
		t.Fatalf("merge count = %d, want 1", got)
	}
	if got := len(groups[GroupStaged]); got != 1 {
		t.Fatalf("staged count = %d, want 1", got)
	}
	if got := len(groups[GroupChanges]); got != 2 {
		t.Fatalf("changes count = %d, want 2", got)
	}
	if got := string(groups[GroupChanges][0].Path); got != "a.txt" {
		t.Fatalf("first change = %q, want a.txt", got)
	}
}
