package grove

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestWorktreeName(t *testing.T) {
	for in, want := range map[string]string{
		"seth/fix-thing":   "seth-fix-thing",
		"feature":          "feature",
		"a/b/c":            "a-b-c",
		"weird..name":      "weird-name",
		"/leading/":        "leading",
		"user:branch name": "user-branch-name",
	} {
		if got := WorktreeName(in); got != want {
			t.Errorf("WorktreeName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPullRequests(t *testing.T) {
	old := gh
	defer func() { gh = old }()
	gh = func(dir string, args ...string) ([]byte, error) {
		if args[0] == "api" {
			return []byte("me\n"), nil
		}
		search := ""
		for i, a := range args {
			if a == "--search" {
				search = args[i+1]
			}
		}
		switch search {
		case "involves:@me":
			return []byte(`[
			 {"number":1,"title":"mine","headRefName":"me/one","baseRefName":"main","author":{"login":"me"},"updatedAt":"2026-09-10T00:00:00Z","reviewRequests":[],"assignees":[]},
			 {"number":2,"title":"mentioned","headRefName":"bob/two","baseRefName":"main","author":{"login":"bob"},"updatedAt":"2026-09-11T00:00:00Z","reviewRequests":[{"login":"carol"}],"assignees":[]},
			 {"number":3,"title":"assigned","headRefName":"bob/three","baseRefName":"main","author":{"login":"bob"},"updatedAt":"2026-09-09T00:00:00Z","reviewRequests":[],"assignees":[{"login":"ME"}]},
			 {"number":4,"title":"both","headRefName":"bob/four","baseRefName":"main","author":{"login":"bob"},"isCrossRepository":true,"updatedAt":"2026-09-01T00:00:00Z","reviewRequests":[{"login":"me"}],"assignees":[{"login":"me"}]}
			]`), nil
		case "review-requested:@me":
			return []byte(`[
			 {"number":4,"title":"both","headRefName":"bob/four","baseRefName":"main","author":{"login":"bob"},"isCrossRepository":true,"updatedAt":"2026-09-01T00:00:00Z","reviewRequests":[{"login":"me"}],"assignees":[{"login":"me"}]},
			 {"number":5,"title":"team","headRefName":"dan/five","baseRefName":"main","author":{"login":"dan"},"updatedAt":"2026-09-08T00:00:00Z","reviewRequests":[{"name":"backend","slug":"backend"}],"assignees":[]}
			]`), nil
		}
		t.Fatalf("unexpected search %q", search)
		return nil, nil
	}
	pulls, err := (Repo{Root: t.TempDir()}).PullRequests()
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, p := range pulls {
		got = append(got, p.Branch+"="+p.Role)
	}
	want := "dan/five=review bob/four=review me/one=yours bob/three=assigned bob/two=involved"
	if strings.Join(got, " ") != want {
		t.Fatalf("got %q\nwant %q", strings.Join(got, " "), want)
	}
	if !pulls[1].Fork || pulls[1].Name() != "bob-four" {
		t.Errorf("fork/name: %+v", pulls[1])
	}

	all, unchecked := Link(pulls, []Worktree{{Name: "", Branch: "main", Main: true}, {Name: "me-one", Branch: "me/one"}})
	if all[2].Worktree != "me-one" || len(unchecked) != 4 || unchecked[2].Number != 3 {
		t.Errorf("Link: all=%+v unchecked=%+v", all, unchecked)
	}
}

func TestPullAge(t *testing.T) {
	now := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	for d, want := range map[time.Duration]string{
		30 * time.Second:    "1m",
		5 * time.Minute:     "5m",
		3 * time.Hour:       "3h",
		2 * 24 * time.Hour:  "2d",
		20 * 24 * time.Hour: "2w",
	} {
		if got := (Pull{Updated: now.Add(-d)}).Age(now); got != want {
			t.Errorf("Age(%v) = %q, want %q", d, got, want)
		}
	}
}

func TestAdoptAndCheckout(t *testing.T) {
	r, _ := newRepo(t)
	gitRun(t, r.Root, "checkout", "-q", "-b", "seth/fix-thing")
	os.WriteFile(filepath.Join(r.Root, "fix.txt"), []byte("fix\n"), 0o644)
	gitRun(t, r.Root, "add", "fix.txt")
	gitRun(t, r.Root, "commit", "-q", "-m", "fix")
	gitRun(t, r.Root, "checkout", "-q", "main")

	wt, err := r.Checkout(Pull{Number: 7, Branch: "seth/fix-thing"}, Conventions{})
	if err != nil {
		t.Fatal(err)
	}
	if wt.Name != "seth-fix-thing" || wt.Branch != "seth/fix-thing" || wt.Ahead != 1 {
		t.Errorf("checkout: %+v", wt)
	}
	// again: the same worktree answers, nothing is made twice
	again, err := r.Checkout(Pull{Number: 7, Branch: "seth/fix-thing"}, Conventions{})
	if err != nil || again.Path != wt.Path {
		t.Errorf("second checkout: %+v %v", again, err)
	}
	if _, err := r.Adopt("nowhere", "no/such", Conventions{}); err == nil || !strings.Contains(err.Error(), "no branch") {
		t.Errorf("adopt of a missing branch: %v", err)
	}
}

func TestCheckoutFromFork(t *testing.T) {
	r, _ := newRepo(t)
	// an origin whose only copy of the branch is the PR ref, the way
	// GitHub holds a fork's head
	origin := filepath.Join(t.TempDir(), "origin.git")
	gitRun(t, ".", "init", "-q", "--bare", origin)
	gitRun(t, r.Root, "remote", "add", "origin", origin)
	gitRun(t, r.Root, "checkout", "-q", "-b", "bob/theirs")
	os.WriteFile(filepath.Join(r.Root, "theirs.txt"), []byte("x\n"), 0o644)
	gitRun(t, r.Root, "add", "theirs.txt")
	gitRun(t, r.Root, "commit", "-q", "-m", "theirs")
	gitRun(t, r.Root, "push", "-q", "origin", "main", "bob/theirs:refs/pull/9/head")
	gitRun(t, r.Root, "checkout", "-q", "main")
	gitRun(t, r.Root, "branch", "-D", "bob/theirs")

	wt, err := r.Checkout(Pull{Number: 9, Branch: "bob/theirs", Fork: true}, Conventions{})
	if err != nil {
		t.Fatal(err)
	}
	if wt.Name != "bob-theirs" || wt.Branch != "bob/theirs" || wt.Ahead != 1 {
		t.Errorf("fork checkout: %+v", wt)
	}
	if _, err := os.Stat(filepath.Join(wt.Path, "theirs.txt")); err != nil {
		t.Errorf("the PR's file is not in the worktree: %v", err)
	}
}
