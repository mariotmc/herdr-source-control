package git

import (
	"bytes"
	"math"
	"strconv"
	"testing"

	"github.com/mariotmc/herdr-source-control/internal/domain"
)

func nul(records ...[]byte) []byte {
	return append(bytes.Join(records, []byte{0}), 0)
}

func TestParsePorcelainPreservesPathsAndMetadata(t *testing.T) {
	weird := []byte{'-', 'a', '\t', '\n', '\x1b', '\\', 0xff}
	data := nul(
		[]byte("# branch.oid 0123456789012345678901234567890123456789"),
		[]byte("# branch.head topic"),
		[]byte("# branch.upstream origin/topic"),
		[]byte("# branch.ab +12 -3"),
		append([]byte("1 MM N... 100644 100644 100644 aaaaaaa bbbbbbb "), weird...),
		[]byte("2 RM N... 100644 100644 100644 aaaaaaa bbbbbbb R087 new name"),
		[]byte("old\nname"),
		[]byte("u UU N... 100644 100644 100644 100644 aaaaaaa bbbbbbb ccccccc conflict"),
		[]byte("? untracked"),
		[]byte("! ignored"),
	)
	got, err := parsePorcelain(data)
	if err != nil {
		t.Fatal(err)
	}
	if got.branch.Name != "topic" || got.branch.OID == "" || got.statusUpstream != "origin/topic" || got.statusAhead != 12 || got.statusBehind != 3 || !got.statusCounts {
		t.Fatalf("branch metadata = %+v, upstream=%q counts=%d/%d", got.branch, got.statusUpstream, got.statusAhead, got.statusBehind)
	}
	if len(got.changes) != 4 {
		t.Fatalf("changes = %d, want 4", len(got.changes))
	}
	if !bytes.Equal(got.changes[0].Path, weird) || got.changes[0].IndexStatus != domain.StatusModified || got.changes[0].WorktreeStatus != domain.StatusModified {
		t.Fatalf("ordinary change = %+v", got.changes[0])
	}
	rename := got.changes[1]
	if rename.Score != 87 || string(rename.Path) != "new name" || string(rename.OriginalPath) != "old\nname" || rename.IndexStatus != domain.StatusRenamed || rename.WorktreeStatus != domain.StatusModified {
		t.Fatalf("rename = %+v", rename)
	}
	if !got.changes[2].Conflicted || got.changes[2].IndexStatus != domain.StatusUnmerged || !got.changes[3].Untracked {
		t.Fatalf("conflict/untracked = %+v / %+v", got.changes[2], got.changes[3])
	}
}

func TestParsePorcelainHeadStatesAndUnknownStatus(t *testing.T) {
	tests := []struct {
		name  string
		data  []byte
		state domain.HeadState
		code  domain.Status
	}{
		{"detached", nul([]byte("# branch.oid abc"), []byte("# branch.head (detached)"), []byte("1 X. N... 100644 100644 100644 a b file")), domain.HeadDetached, domain.StatusUnknown},
		{"unborn", nul([]byte("# branch.oid (initial)"), []byte("# branch.head main")), domain.HeadUnborn, domain.StatusUnmodified},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parsePorcelain(tt.data)
			if err != nil {
				t.Fatal(err)
			}
			if got.branch.State != tt.state {
				t.Fatalf("state = %v, want %v", got.branch.State, tt.state)
			}
			if len(got.changes) > 0 && got.changes[0].IndexStatus != tt.code {
				t.Fatalf("status = %v, want %v", got.changes[0].IndexStatus, tt.code)
			}
		})
	}
}

func TestParsePorcelainRejectsMalformedFramingAndCounts(t *testing.T) {
	overflow := strconv.FormatUint(math.MaxUint64, 10) + "0"
	tests := map[string][]byte{
		"unterminated":       []byte("? file"),
		"empty record":       []byte("? file\x00\x00"),
		"unknown record":     nul([]byte("x data")),
		"unknown header":     nul([]byte("# branch.future data")),
		"malformed ordinary": nul([]byte("1 M file")),
		"missing old path":   nul([]byte("2 R. N... 1 1 1 a b R100 new")),
		"bad rename score":   nul([]byte("2 R. N... 1 1 1 a b R101 new"), []byte("old")),
		"overflow":           nul([]byte("# branch.ab +" + overflow + " -0")),
	}
	for name, data := range tests {
		t.Run(name, func(t *testing.T) {
			if got, err := parsePorcelain(data); err == nil {
				t.Fatalf("parse succeeded: %+v", got)
			}
		})
	}
}

func TestParsePorcelainDoesNotPublishPrefixOnLaterError(t *testing.T) {
	data := nul([]byte("? valid"), []byte("broken"))
	got, err := parsePorcelain(data)
	if err == nil || len(got.changes) != 0 {
		t.Fatalf("got %+v, err %v", got, err)
	}
}
