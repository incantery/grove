package grove

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func gitRun(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

// recorder is a Place that remembers what it was asked, so the
// lifecycle can be checked without a multiplexer.
type recorder struct {
	opened []string
	closed []string
	live   map[string]bool
}

func (r *recorder) Name() string { return "recorder" }
func (r *recorder) Open(session, dir string) error {
	r.opened = append(r.opened, session+"@"+dir)
	if r.live == nil {
		r.live = map[string]bool{}
	}
	r.live[session] = true
	return nil
}
func (r *recorder) Close(session string) error {
	r.closed = append(r.closed, session)
	delete(r.live, session)
	return nil
}
func (r *recorder) Live() map[string]bool {
	if r.live == nil {
		return map[string]bool{}
	}
	return r.live
}

func newRepo(t *testing.T) (Repo, *recorder) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	root := filepath.Join(t.TempDir(), "myrepo")
	gitRun(t, ".", "init", "-q", "-b", "main", root)
	os.WriteFile(filepath.Join(root, "a.txt"), []byte("a\n"), 0o644)
	os.WriteFile(filepath.Join(root, ".env"), []byte("SECRET=1\n"), 0o644)
	os.WriteFile(filepath.Join(root, ".gitignore"), []byte(".env\nnode_modules\n"), 0o644)
	os.MkdirAll(filepath.Join(root, "node_modules", "x"), 0o755)
	gitRun(t, root, "add", "a.txt", ".gitignore")
	gitRun(t, root, "commit", "-q", "-m", "init")
	repo, err := Find(root)
	if err != nil {
		t.Fatal(err)
	}
	if repo.Name != "myrepo" {
		t.Fatalf("repo name %q", repo.Name)
	}
	rec := &recorder{}
	repo.Place = rec
	return repo, rec
}

func TestLifecycle(t *testing.T) {
	repo, rec := newRepo(t)

	wt, err := repo.New("feature", "", Conventions{Copy: []string{".env"}, Link: []string{"node_modules"}})
	if err != nil {
		t.Fatal(err)
	}
	if wt.Path != repo.Path("feature") || wt.Branch != "feature" || wt.Name != "feature" {
		t.Fatalf("new worktree = %+v", wt)
	}
	if wt.Session != "myrepo--feature" {
		t.Errorf("session %q", wt.Session)
	}
	if b, _ := os.ReadFile(filepath.Join(wt.Path, ".env")); string(b) != "SECRET=1\n" {
		t.Errorf(".env not copied: %q", b)
	}
	if target, err := os.Readlink(filepath.Join(wt.Path, "node_modules")); err != nil || target != filepath.Join(repo.Root, "node_modules") {
		t.Errorf("node_modules not linked: %q %v", target, err)
	}

	// Found from inside the worktree too: it answers with its home.
	if r2, err := Find(wt.Path); err != nil || r2.Root != repo.Root {
		t.Errorf("Find from worktree = %+v %v", r2, err)
	}

	// Opening is the place's business, and the row says it is live.
	if err := repo.Open(wt); err != nil {
		t.Fatal(err)
	}
	if len(rec.opened) != 1 || rec.opened[0] != "myrepo--feature@"+wt.Path {
		t.Errorf("the place was asked %v", rec.opened)
	}
	wts, err := repo.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(wts) != 2 || !wts[0].Main || wts[1].Name != "feature" || !wts[1].Live {
		t.Fatalf("list = %+v", wts)
	}

	// Work on it: dirty, then committed and ahead.
	os.WriteFile(filepath.Join(wt.Path, "b.txt"), []byte("b\n"), 0o644)
	if got, _ := repo.Get("feature"); !got.Dirty {
		t.Error("untracked file must read as dirty")
	}
	if err := repo.Merge("feature"); err == nil {
		t.Error("merge must refuse a dirty worktree")
	}
	gitRun(t, wt.Path, "add", "b.txt")
	gitRun(t, wt.Path, "commit", "-q", "-m", "b")
	got, _ := repo.Get("feature")
	if got.Dirty || got.Ahead != 1 || got.Behind != 0 {
		t.Errorf("after commit: %+v", got)
	}

	// rm without force refuses the unmerged branch and keeps the tree,
	// and the session with it.
	if err := repo.Remove(got, false); err == nil {
		t.Error("rm must refuse an unmerged branch")
	}
	if _, err := os.Stat(filepath.Join(wt.Path, "b.txt")); err != nil {
		t.Fatal("a refused rm must not touch the worktree")
	}
	if len(rec.closed) != 0 {
		t.Error("a refused rm must not close the session")
	}

	if err := repo.Merge("feature"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(wt.Path); !os.IsNotExist(err) {
		t.Error("worktree dir must be gone after merge")
	}
	if _, err := os.Stat(filepath.Join(repo.Root, "b.txt")); err != nil {
		t.Error("merge must land b.txt on main")
	}
	if out := gitRun(t, repo.Root, "branch", "--list", "feature"); out != "" {
		t.Errorf("branch must be deleted, got %q", out)
	}
	if len(rec.closed) != 1 || rec.closed[0] != "myrepo--feature" {
		t.Errorf("the session was not closed: %v", rec.closed)
	}
	if wts, _ := repo.List(); len(wts) != 1 {
		t.Errorf("list after merge = %+v", wts)
	}
}

// A branch that exists only on origin is checked out tracking it,
// not recreated off the default branch — the thing `git worktree add`
// alone gets wrong, and the reason people fetch first.
func TestABranchOnOriginIsTracked(t *testing.T) {
	repo, _ := newRepo(t)
	origin := filepath.Join(t.TempDir(), "origin.git")
	gitRun(t, ".", "init", "-q", "--bare", "-b", "main", origin)
	gitRun(t, repo.Root, "remote", "add", "origin", origin)
	gitRun(t, repo.Root, "push", "-q", "-u", "origin", "main")
	// Somebody else pushed a branch.
	other := filepath.Join(t.TempDir(), "other")
	gitRun(t, ".", "clone", "-q", origin, other)
	gitRun(t, other, "checkout", "-q", "-b", "theirs")
	os.WriteFile(filepath.Join(other, "theirs.txt"), []byte("t\n"), 0o644)
	gitRun(t, other, "add", "theirs.txt")
	gitRun(t, other, "commit", "-q", "-m", "theirs")
	gitRun(t, other, "push", "-q", "-u", "origin", "theirs")

	// Not fetched yet: origin/theirs is unknown here, so it would be
	// a fresh branch. Fetch, and it is theirs.
	if err := repo.Fetch(); err != nil {
		t.Fatal(err)
	}
	wt, err := repo.New("theirs", "", Conventions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(wt.Path, "theirs.txt")); err != nil {
		t.Fatal("the worktree is not on origin's branch")
	}
	up := strings.TrimSpace(gitRun(t, wt.Path, "rev-parse", "--abbrev-ref", "theirs@{upstream}"))
	if up != "origin/theirs" {
		t.Errorf("upstream is %q, want origin/theirs", up)
	}
	if wt.Ahead != 1 {
		t.Errorf("ahead = %d, want 1 (their commit)", wt.Ahead)
	}
}

func TestNewRejectsBadNames(t *testing.T) {
	repo, _ := newRepo(t)
	for _, bad := range []string{"", "a b", "a/b", "..", "a:b"} {
		if _, err := repo.New(bad, "", Conventions{}); err == nil {
			t.Errorf("New(%q) must fail", bad)
		}
	}
}

// The session name is the directory's, scrubbed of what a multiplexer
// cannot carry — rook's rule, so the two agree.
func TestSessionNames(t *testing.T) {
	r := Repo{Root: "/src/rookide.com", Name: "rookide.com"}
	if got := r.Session(""); got != "rookide_com" {
		t.Errorf("main session %q", got)
	}
	if got := r.Session("v2"); got != "rookide_com--v2" {
		t.Errorf("worktree session %q", got)
	}
	if got := SessionName("/"); got != "grove" {
		t.Errorf("root session %q", got)
	}
}

func TestConventionsComeFromTheRepoAndThePerson(t *testing.T) {
	root := t.TempDir()
	// rook.toml's [worktree] table still counts, where a repo has one.
	os.WriteFile(filepath.Join(root, "rook.toml"), []byte("[land]\ncheck = [\"go test ./...\"]\n[worktree]\ncopy = [\".env\", 'config/local.toml'] # secrets\nlink = [\"node_modules\"]\n"), 0o644)
	c := LoadConventions(root)
	if strings.Join(c.Copy, ",") != ".env,config/local.toml" || strings.Join(c.Link, ",") != "node_modules" {
		t.Errorf("from rook.toml: %+v", c)
	}
	// grove.toml wins when both are there.
	os.WriteFile(filepath.Join(root, "grove.toml"), []byte("copy = [\".env.local\"]\n"), 0o644)
	c = LoadConventions(root)
	if strings.Join(c.Copy, ",") != ".env.local" || len(c.Link) != 0 {
		t.Errorf("from grove.toml: %+v", c)
	}
	// The person's own, merged without duplicates.
	cfg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfg)
	os.MkdirAll(filepath.Join(cfg, "grove"), 0o755)
	os.WriteFile(filepath.Join(cfg, "grove", "grove.toml"), []byte("copy = [\".env.local\", \".envrc\"]\nlink = [\"target\"]\n"), 0o644)
	all := UserConventions().Merge(c)
	if strings.Join(all.Copy, ",") != ".env.local,.envrc" || strings.Join(all.Link, ",") != "target" {
		t.Errorf("merged: %+v", all)
	}
	if c := LoadConventions(t.TempDir()); len(c.Copy)+len(c.Link) != 0 {
		t.Errorf("a repo with no file has conventions: %+v", c)
	}
}

func TestDetectReadsTheEnvironment(t *testing.T) {
	t.Setenv("GROVE_PLACE", "none")
	if Detect().Name() != "none" {
		t.Error("GROVE_PLACE=none was not honoured")
	}
	t.Setenv("GROVE_PLACE", "")
	t.Setenv("ROOK_MUX_PANE", "3")
	t.Setenv("TMUX", "")
	if Detect().Name() != "rook" {
		t.Error("a rook pane was not detected")
	}
	t.Setenv("ROOK_MUX_PANE", "")
	t.Setenv("TMUX", "/tmp/tmux-1/default,1,0")
	if Detect().Name() != "tmux" {
		t.Error("a tmux session was not detected")
	}
}
