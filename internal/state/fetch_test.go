package state

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSaveAndLoadRoundTripUsesPrivatePermissions(t *testing.T) {
	store := Store{Dir: t.TempDir()}
	key := Key("/repo/.git")
	want := Record{LastAttemptUnix: 1700000000, LastSuccessUnix: 1699999000, LastError: "boom"}
	if err := store.Save(key, want); err != nil {
		t.Fatal(err)
	}
	got, err := store.Load(key)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("Load() = %#v, want %#v", got, want)
	}
	directory := filepath.Join(store.Dir, "fetch")
	for path, mode := range map[string]os.FileMode{
		directory:                             0o700,
		filepath.Join(directory, key+".json"): 0o600,
	} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != mode {
			t.Errorf("%s mode = %o, want %o", path, got, mode)
		}
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("atomic write left %d entries, want 1", len(entries))
	}
}

func TestLoadTreatsMissingAndCorruptFileAsZeroRecord(t *testing.T) {
	store := Store{Dir: t.TempDir()}
	key := Key("/repo/.git")
	got, err := store.Load(key)
	if err != nil || got != (Record{}) {
		t.Fatalf("missing file: Load() = %#v, %v", got, err)
	}
	if err := os.MkdirAll(filepath.Join(store.Dir, "fetch"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(store.Dir, "fetch", key+".json"), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err = store.Load(key)
	if err != nil || got != (Record{}) {
		t.Fatalf("corrupt file: Load() = %#v, %v", got, err)
	}
}

func TestEmptyDirectoryDegradesToNoOp(t *testing.T) {
	var store Store
	if err := store.Save(Key("/repo/.git"), Record{LastAttemptUnix: 1}); err != nil {
		t.Fatalf("Save() = %v, want nil", err)
	}
	got, err := store.Load(Key("/repo/.git"))
	if err != nil || got != (Record{}) {
		t.Fatalf("Load() = %#v, %v", got, err)
	}
}

func TestSaveSanitizesAndCapsLastError(t *testing.T) {
	store := Store{Dir: t.TempDir()}
	key := Key("/repo/.git")
	if err := store.Save(key, Record{LastError: "line\x1b[2Jone\nline\ttwo " + strings.Repeat("x", 300)}); err != nil {
		t.Fatal(err)
	}
	got, err := store.Load(key)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.LastError) != errorLimit {
		t.Fatalf("len(LastError) = %d, want %d", len(got.LastError), errorLimit)
	}
	if strings.ContainsAny(got.LastError, "\x1b\n\t") {
		t.Fatalf("LastError kept control characters: %q", got.LastError)
	}
}

func TestKeyIsStableSixteenHexCharacters(t *testing.T) {
	key := Key("/repo/.git")
	if len(key) != 16 {
		t.Fatalf("len(Key()) = %d, want 16", len(key))
	}
	if strings.Trim(key, "0123456789abcdef") != "" {
		t.Fatalf("Key() = %q, want lowercase hex", key)
	}
	if key == Key("/other/.git") {
		t.Fatal("different common directories share a key")
	}
	if key != Key("/repo/.git") {
		t.Fatal("Key() is not stable")
	}
}

func TestStaleOnlyWhenFetchingIsBrokenAndUnverified(t *testing.T) {
	after := 15 * time.Minute
	now := time.Now().Unix()
	cases := []struct {
		name   string
		record Record
		want   bool
	}{
		{"healthy", Record{LastSuccessUnix: now}, false},
		{"failing but verified recently", Record{LastError: "boom", LastSuccessUnix: now}, false},
		{"failing and never verified", Record{LastError: "boom"}, true},
		{"failing and last success too old", Record{LastError: "boom", LastSuccessUnix: time.Now().Add(-time.Hour).Unix()}, true},
		{"permanent failure is not staleness", Record{LastError: "upstream gone", LastErrorPermanent: true}, false},
	}
	for _, test := range cases {
		if got := test.record.Stale(after); got != test.want {
			t.Errorf("%s: Stale() = %v, want %v", test.name, got, test.want)
		}
	}
}
