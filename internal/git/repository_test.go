package git

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/mariotmc/herdr-source-control/internal/domain"
)

func gitTest(t *testing.T, dir string, args ...string) []byte {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "HOME="+t.TempDir(), "LC_ALL=C", "GIT_CONFIG_NOSYSTEM=1")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, output)
	}
	return bytes.TrimSuffix(output, []byte("\n"))
}

func initRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	gitTest(t, dir, "init", "-b", "main")
	gitTest(t, dir, "config", "user.name", "Test User")
	gitTest(t, dir, "config", "user.email", "test@example.com")
	return dir
}

func commitFile(t *testing.T, dir, name, content string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	gitTest(t, dir, "add", "--", name)
	gitTest(t, dir, "commit", "-m", name)
}

func newTestRepository(t *testing.T, dir string) *Repository {
	t.Helper()
	repo, err := New(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	return repo
}

func TestDiscoveryCanonicalRootNestedAndRejections(t *testing.T) {
	repoDir := initRepo(t)
	sub := filepath.Join(repoDir, "a", "b")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	discovery, err := Discover(context.Background(), sub)
	if err != nil {
		t.Fatal(err)
	}
	if discovery.Root != repoDir || !filepath.IsAbs(discovery.GitDir) || !filepath.IsAbs(discovery.CommonDir) {
		t.Fatalf("discovery = %+v", discovery)
	}
	if runtime.GOOS != "windows" {
		link := filepath.Join(t.TempDir(), "repo-link")
		if err := os.Symlink(repoDir, link); err != nil {
			t.Fatal(err)
		}
		linked, err := Discover(context.Background(), link)
		if err != nil {
			t.Fatal(err)
		}
		if linked.CanonicalRoot != discovery.CanonicalRoot {
			t.Fatalf("canonical roots differ: %q != %q", linked.CanonicalRoot, discovery.CanonicalRoot)
		}
	}
	if _, err := Discover(context.Background(), t.TempDir()); !IsKind(err, ErrorNotRepository) {
		t.Fatalf("non-repository error = %v", err)
	}
	bare := t.TempDir()
	gitTest(t, bare, "init", "--bare")
	if _, err := Discover(context.Background(), bare); !IsKind(err, ErrorBareRepository) {
		t.Fatalf("bare error = %v", err)
	}
}

func TestSnapshotTracksRawChangesHeadAndOperation(t *testing.T) {
	dir := initRepo(t)
	commitFile(t, dir, "tracked.txt", "one")
	if err := os.WriteFile(filepath.Join(dir, "tracked.txt"), []byte("two"), 0o644); err != nil {
		t.Fatal(err)
	}
	weird := []byte{'w', 'e', 'i', 'r', 'd', '-', 0xff}
	if err := os.WriteFile(filepath.Join(dir, string(weird)), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	repo := newTestRepository(t, dir)
	snapshot, err := repo.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Branch.State != domain.HeadAttached || snapshot.Branch.Name != "main" || len(snapshot.Branch.OID) != 40 || snapshot.Branch.CountsKnown || len(snapshot.Changes) != 2 {
		t.Fatalf("snapshot = %+v", snapshot)
	}
	if !bytes.Equal(snapshot.Changes[1].Path, weird) {
		t.Fatalf("raw path = %q, want %q", snapshot.Changes[1].Path, weird)
	}
	mergeHead := filepath.Join(snapshot.GitDir, "MERGE_HEAD")
	if err := os.WriteFile(mergeHead, []byte(snapshot.Branch.OID), 0o644); err != nil {
		t.Fatal(err)
	}
	snapshot, err = repo.Snapshot(context.Background())
	if err != nil || !snapshot.Operation.Merge {
		t.Fatalf("operation snapshot = %+v, %v", snapshot.Operation, err)
	}
}

func TestLocalBranchesCheckoutAndCreate(t *testing.T) {
	dir := initRepo(t)
	commitFile(t, dir, "file", "main")
	gitTest(t, dir, "branch", "older")
	repo := newTestRepository(t, dir)
	branches, err := repo.LocalBranches(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(branches) != 2 || !branches[0].Current || branches[0].Name != "main" {
		t.Fatalf("branches = %+v", branches)
	}
	if err := repo.Checkout(context.Background(), "older"); err != nil {
		t.Fatal(err)
	}
	if got := string(gitTest(t, dir, "branch", "--show-current")); got != "older" {
		t.Fatalf("current branch = %q", got)
	}
	if err := repo.CreateBranch(context.Background(), "created"); err != nil {
		t.Fatal(err)
	}
	if got := string(gitTest(t, dir, "branch", "--show-current")); got != "created" {
		t.Fatalf("current branch = %q", got)
	}
	if err := repo.ValidateBranch(context.Background(), "created"); !IsKind(err, ErrorValidation) {
		t.Fatalf("existing validation error = %v", err)
	}
	if err := repo.ValidateBranch(context.Background(), "bad name"); !IsKind(err, ErrorValidation) {
		t.Fatalf("invalid validation error = %v", err)
	}
	oid := string(gitTest(t, dir, "rev-parse", "HEAD"))
	gitTest(t, dir, "switch", "--detach", oid)
	if err := repo.CreateBranch(context.Background(), "from-detached"); err != nil {
		t.Fatal(err)
	}
	if got := string(gitTest(t, dir, "branch", "--show-current")); got != "from-detached" {
		t.Fatalf("detached creation current branch = %q", got)
	}
}

func TestCheckoutRefusesUnlistedAndOtherWorktreeBranch(t *testing.T) {
	dir := initRepo(t)
	commitFile(t, dir, "file", "main")
	gitTest(t, dir, "branch", "other")
	repo := newTestRepository(t, dir)
	if err := repo.Checkout(context.Background(), "other"); !IsKind(err, ErrorValidation) {
		t.Fatalf("unlisted checkout error = %v", err)
	}
	if _, err := repo.LocalBranches(context.Background()); err != nil {
		t.Fatal(err)
	}
	worktree := filepath.Join(t.TempDir(), "worktree")
	gitTest(t, dir, "worktree", "add", worktree, "other")
	if err := repo.Checkout(context.Background(), "other"); !IsKind(err, ErrorWorktreeConflict) {
		t.Fatalf("worktree checkout error = %v", err)
	}
}

func TestCheckoutRefusesConflictingDirtyTreeWithoutDataLoss(t *testing.T) {
	dir := initRepo(t)
	commitFile(t, dir, "file", "main")
	gitTest(t, dir, "switch", "-c", "other")
	commitFile(t, dir, "file", "other")
	gitTest(t, dir, "switch", "main")
	if err := os.WriteFile(filepath.Join(dir, "file"), []byte("local work"), 0o644); err != nil {
		t.Fatal(err)
	}
	repo := newTestRepository(t, dir)
	if _, err := repo.LocalBranches(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := repo.Checkout(context.Background(), "other"); !IsKind(err, ErrorDirtyCheckout) {
		t.Fatalf("dirty checkout error = %v", err)
	}
	content, err := os.ReadFile(filepath.Join(dir, "file"))
	if err != nil || string(content) != "local work" {
		t.Fatalf("working file = %q, %v", content, err)
	}
	if got := string(gitTest(t, dir, "branch", "--show-current")); got != "main" {
		t.Fatalf("branch changed to %q", got)
	}
}

func TestUnbornSnapshotAndCreateRefusal(t *testing.T) {
	dir := initRepo(t)
	repo := newTestRepository(t, dir)
	snapshot, err := repo.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Branch.State != domain.HeadUnborn || snapshot.Branch.Name != "main" {
		t.Fatalf("branch = %+v", snapshot.Branch)
	}
	if err := repo.CreateBranch(context.Background(), "topic"); !IsKind(err, ErrorValidation) {
		t.Fatalf("create error = %v", err)
	}
}

func TestErrorsAreTypedAndUnwrap(t *testing.T) {
	cause := errors.New("cause")
	err := &OperationError{Kind: ErrorParse, Err: cause}
	if !IsKind(err, ErrorParse) || !errors.Is(err, cause) {
		t.Fatalf("typed error = %v", err)
	}
}

func TestExactUpstreamCountsFetchFastForwardAndPush(t *testing.T) {
	root := t.TempDir()
	bare := filepath.Join(root, "remote.git")
	gitTest(t, root, "init", "--bare", bare)
	local := filepath.Join(root, "local")
	if err := os.Mkdir(local, 0o755); err != nil {
		t.Fatal(err)
	}
	gitTest(t, local, "init", "-b", "main")
	gitTest(t, local, "config", "user.name", "Local")
	gitTest(t, local, "config", "user.email", "local@example.com")
	commitFile(t, local, "file", "base")
	gitTest(t, local, "remote", "add", "origin", bare)
	gitTest(t, local, "push", "-u", "origin", "main")

	repo := newTestRepository(t, local)
	initial, err := repo.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !initial.Branch.CountsKnown || initial.Branch.Ahead != 0 || initial.Branch.Behind != 0 || initial.Branch.UpstreamRef != "refs/remotes/origin/main" || initial.Branch.RemoteRef != "refs/heads/main" {
		t.Fatalf("initial branch = %+v", initial.Branch)
	}

	other := filepath.Join(root, "other")
	gitTest(t, root, "clone", "--branch", "main", bare, other)
	gitTest(t, other, "config", "user.name", "Other")
	gitTest(t, other, "config", "user.email", "other@example.com")
	commitFile(t, other, "remote", "remote")
	gitTest(t, other, "push", "origin", "main")

	trace := filepath.Join(root, "trace")
	t.Setenv("GIT_TRACE", trace)
	if err := repo.Fetch(context.Background(), initial.Branch); err != nil {
		t.Fatal(err)
	}
	behind, err := repo.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if behind.Branch.Ahead != 0 || behind.Branch.Behind != 1 {
		t.Fatalf("after fetch counts = %d/%d", behind.Branch.Ahead, behind.Branch.Behind)
	}
	if err := repo.FastForward(context.Background(), behind.Branch); err != nil {
		t.Fatal(err)
	}
	upToDate, err := repo.Snapshot(context.Background())
	if err != nil || upToDate.Branch.Ahead != 0 || upToDate.Branch.Behind != 0 {
		t.Fatalf("after fast-forward = %+v, %v", upToDate.Branch, err)
	}

	commitFile(t, local, "local", "local")
	ahead, err := repo.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if ahead.Branch.Ahead != 1 || ahead.Branch.Behind != 0 {
		t.Fatalf("ahead counts = %d/%d", ahead.Branch.Ahead, ahead.Branch.Behind)
	}
	if err := repo.Push(context.Background(), ahead.Branch, ahead.Branch.OID); err != nil {
		t.Fatal(err)
	}
	if got := string(gitTest(t, bare, "rev-parse", "refs/heads/main")); got != ahead.Branch.OID {
		t.Fatalf("remote oid = %q, want %q", got, ahead.Branch.OID)
	}
	traceData, err := os.ReadFile(trace)
	if err != nil {
		t.Fatal(err)
	}
	for _, exact := range []string{
		"fetch --no-tags --no-prune --no-prune-tags --no-recurse-submodules -- origin +refs/heads/main:refs/remotes/origin/main",
		"merge --ff-only --no-autostash -- refs/remotes/origin/main",
		"push --porcelain --no-follow-tags --recurse-submodules=no -- origin " + ahead.Branch.OID + ":refs/heads/main",
	} {
		if !bytes.Contains(traceData, []byte(exact)) {
			t.Errorf("trace does not contain exact command %q\n%s", exact, traceData)
		}
	}
	gitTest(t, local, "config", "remote.origin.mirror", "true")
	if err := repo.Fetch(context.Background(), ahead.Branch); !IsKind(err, ErrorValidation) {
		t.Fatalf("mirror fetch error = %v", err)
	}
}

func TestPushRejectsOIDOtherThanVerifiedSnapshotOID(t *testing.T) {
	branch := domain.BranchState{
		State: domain.HeadAttached, Name: "main", OID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Upstream: "origin/main", UpstreamRef: "refs/remotes/origin/main", RemoteName: "origin", RemoteRef: "refs/heads/main", CountsKnown: true,
	}
	repo := &Repository{}
	if err := repo.Push(context.Background(), branch, "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"); !IsKind(err, ErrorValidation) {
		t.Fatalf("push error = %v", err)
	}
}
