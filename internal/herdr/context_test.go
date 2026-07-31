package herdr

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

func TestContextStartDirPrecedence(t *testing.T) {
	focused := "/focused"
	workspace := "/workspace"

	tests := []struct {
		name    string
		context Context
		want    string
	}{
		{"focused pane", Context{FocusedPaneCWD: &focused, WorkspaceCWD: &workspace}, focused},
		{"workspace", Context{WorkspaceCWD: &workspace}, workspace},
		{"pwd", Context{}, "/pwd"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := test.context.StartDir("/pwd"); got != test.want {
				t.Fatalf("StartDir() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestParseContextRejectsMalformedJSON(t *testing.T) {
	if _, err := ParseContext("{"); err == nil {
		t.Fatal("ParseContext() succeeded for malformed JSON")
	}
}

func TestResolveTargetRepositoryAndNonRepositoryIdentity(t *testing.T) {
	real := t.TempDir()
	link := filepath.Join(t.TempDir(), "checkout")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}

	repository, err := ResolveTarget(context.Background(), link, func(context.Context, string) (string, bool, error) {
		return link, true, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	fromReal, err := ResolveTarget(context.Background(), real, func(context.Context, string) (string, bool, error) {
		return real, true, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !repository.Repository || repository.Root != link {
		t.Fatalf("repository target = %#v", repository)
	}
	if repository.Identity != fromReal.Identity || repository.CanonicalRoot != real {
		t.Fatalf("symlink identity = %q/%q, real = %q/%q", repository.Identity, repository.CanonicalRoot, fromReal.Identity, fromReal.CanonicalRoot)
	}

	nonRepository, err := ResolveTarget(context.Background(), link, func(context.Context, string) (string, bool, error) {
		return "", false, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(real))
	if nonRepository.Repository || nonRepository.Root != link || nonRepository.Identity != hex.EncodeToString(sum[:]) {
		t.Fatalf("non-repository target = %#v", nonRepository)
	}
}
