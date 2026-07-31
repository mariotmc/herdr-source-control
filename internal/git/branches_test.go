package git

import (
	"testing"
	"time"
)

func framed(fields ...string) []byte {
	var data []byte
	for i, field := range fields {
		if i > 0 {
			data = append(data, 0)
		}
		data = append(data, field...)
	}
	return append(data, 0, 0, '\n')
}

func TestParseBranchesUsesExplicitFramingAndSanitizesDisplayData(t *testing.T) {
	data := append(framed("topic", "*", "/tmp/work\ntree", "0123", "A\x1b[31m", "123", "subject\nline"), framed("main", "", "", "4567", "B", "0", "clean")...)
	got, err := parseBranches(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || !got[0].Current || got[0].WorktreePath != "/tmp/work\ntree" || got[0].Author != `A\x1B[31m` || got[0].Subject != "subject line" || !got[0].CommitTime.Equal(time.Unix(123, 0)) {
		t.Fatalf("branches = %+v", got)
	}
}

func TestParseFramedRecordsRejectsAmbiguousOrMalformedOutput(t *testing.T) {
	for name, data := range map[string][]byte{
		"single nul":        []byte("a\x00b\x00"),
		"missing newline":   []byte("a\x00b\x00\x00"),
		"wrong field count": framed("a"),
		"trailing bytes":    append(framed("a", "b"), 'x'),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := parseFramedRecords(data, 2); err == nil {
				t.Fatal("parse succeeded")
			}
		})
	}
}

func TestParseUpstreamExactTuple(t *testing.T) {
	got, err := parseUpstream(framed("refs/remotes/origin/topic", "origin/topic", "origin", "refs/heads/topic"))
	if err != nil {
		t.Fatal(err)
	}
	if got != (upstreamTuple{"refs/remotes/origin/topic", "origin/topic", "origin", "refs/heads/topic"}) {
		t.Fatalf("tuple = %+v", got)
	}
}
