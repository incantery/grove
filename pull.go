// The pull requests that want a checkout.
//
// A worktree is where a branch gets worked on, and the branches most
// worth a worktree are the ones with an open pull request that
// concerns you: a review someone asked of you, your own PR pushed
// from another machine, one you were assigned or drawn into. GitHub
// knows which those are; `gh` is already logged in; grove asks it and
// offers the answer as rows a keystroke away from a worktree.
package grove

import (
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Pull is an open pull request that concerns the current GitHub user.
type Pull struct {
	Number  int       `json:"number"`
	Title   string    `json:"title"`
	Branch  string    `json:"branch"` // the head branch
	Base    string    `json:"base"`
	Author  string    `json:"author"`
	Fork    bool      `json:"fork"` // the head lives in another repository
	Draft   bool      `json:"draft"`
	URL     string    `json:"url"`
	Updated time.Time `json:"updated"`
	// Role is why it concerns you, the strongest reason first:
	// "review" (your review was asked), "yours" (you opened it),
	// "assigned", or "involved" (you were mentioned or commented).
	Role string `json:"role"`
	// Worktree is the name of the worktree that has the branch out,
	// when one does; the Link step fills it.
	Worktree string `json:"worktree,omitempty"`
}

// Name is the worktree a checkout of the PR is named: the branch with
// the characters a worktree name cannot carry turned to `-`
// (`seth/fix-thing` → `seth-fix-thing`).
func (p Pull) Name() string { return WorktreeName(p.Branch) }

// WorktreeName turns a branch into a worktree name.
func WorktreeName(branch string) string {
	name := strings.Map(func(r rune) rune {
		switch r {
		case '/', '\\', ':', ' ', '\t', '\n':
			return '-'
		}
		return r
	}, strings.TrimSpace(branch))
	for strings.Contains(name, "..") {
		name = strings.ReplaceAll(name, "..", "-")
	}
	return strings.Trim(name, "-")
}

// roles in the order they outrank each other.
var roles = map[string]int{"review": 0, "yours": 1, "assigned": 2, "involved": 3}

// Said is the role in words for a row.
func (p Pull) Said() string {
	switch p.Role {
	case "review":
		return "review asked"
	case "involved":
		return "mentioned"
	}
	return p.Role
}

// Age is how long since the PR last moved, in the shortest words.
func (p Pull) Age(now time.Time) string {
	d := now.Sub(p.Updated)
	switch {
	case d < time.Hour:
		return fmt.Sprintf("%dm", max(1, int(d.Minutes())))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	case d < 14*24*time.Hour:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	default:
		return fmt.Sprintf("%dw", int(d.Hours()/24/7))
	}
}

// ErrNoGH says the pull requests could not be asked for because gh is
// not installed or not logged in — a repo without them is not an
// error, it just has no pull requests to show.
var ErrNoGH = errors.New("gh is not installed or not logged in (https://cli.github.com)")

// gh runs the GitHub CLI in dir; a variable so tests can answer for it.
var gh = func(dir string, args ...string) ([]byte, error) {
	if _, err := exec.LookPath("gh"); err != nil {
		return nil, ErrNoGH
	}
	cmd := exec.Command("gh", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			msg := strings.TrimSpace(string(ee.Stderr))
			if strings.Contains(msg, "gh auth login") || strings.Contains(msg, "not logged") {
				return nil, ErrNoGH
			}
			if msg != "" {
				return nil, fmt.Errorf("gh %s: %s", args[0], firstLine(msg))
			}
		}
		return nil, fmt.Errorf("gh %s: %w", args[0], err)
	}
	return out, nil
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// the fields asked of gh, and their shape.
const pullFields = "number,title,headRefName,baseRefName,isCrossRepository,author,isDraft,updatedAt,reviewRequests,assignees,url"

type ghPull struct {
	Number            int       `json:"number"`
	Title             string    `json:"title"`
	HeadRefName       string    `json:"headRefName"`
	BaseRefName       string    `json:"baseRefName"`
	IsCrossRepository bool      `json:"isCrossRepository"`
	Author            ghLogin   `json:"author"`
	IsDraft           bool      `json:"isDraft"`
	UpdatedAt         time.Time `json:"updatedAt"`
	ReviewRequests    []ghLogin `json:"reviewRequests"`
	Assignees         []ghLogin `json:"assignees"`
	URL               string    `json:"url"`
}

type ghLogin struct {
	Login string `json:"login"`
}

// PullRequests asks GitHub for the open pull requests on this repo
// that concern the logged-in user — review asked of them, opened by
// them, assigned to them, or mentioning them — strongest reason
// first, newest first within a reason. It needs the network and the
// gh CLI; ErrNoGH is the answer when gh is missing or logged out.
func (r Repo) PullRequests() ([]Pull, error) {
	// Three questions gh answers on its own; ask them at once.
	var (
		wg                sync.WaitGroup
		me                string
		involved, asked   []byte
		errMe, errI, errA error
	)
	ask := func(out *[]byte, err *error, search string) {
		defer wg.Done()
		*out, *err = gh(r.Root, "pr", "list", "--state", "open", "--limit", "100",
			"--search", search, "--json", pullFields)
	}
	wg.Add(3)
	go ask(&involved, &errI, "involves:@me")
	go ask(&asked, &errA, "review-requested:@me")
	go func() {
		defer wg.Done()
		out, err := gh(r.Root, "api", "user", "--jq", ".login")
		me, errMe = strings.TrimSpace(string(out)), err
	}()
	wg.Wait()
	if err := errors.Join(errMe, errI, errA); err != nil {
		if errors.Is(err, ErrNoGH) {
			return nil, ErrNoGH
		}
		return nil, err
	}
	var a, b []ghPull
	if err := json.Unmarshal(involved, &a); err != nil {
		return nil, fmt.Errorf("gh pr list: %w", err)
	}
	if err := json.Unmarshal(asked, &b); err != nil {
		return nil, fmt.Errorf("gh pr list: %w", err)
	}
	return pullsOf(me, a, b), nil
}

// pullsOf turns gh's rows into Pulls: one per number, each with the
// strongest reason it concerns me, ordered by reason then recency.
// The review-requested search is the authority on "review" — a team's
// request carries no login to match — so its rows go first.
func pullsOf(me string, involved, asked []ghPull) []Pull {
	seen := map[int]bool{}
	var out []Pull
	add := func(g ghPull, role string) {
		if seen[g.Number] {
			return
		}
		seen[g.Number] = true
		out = append(out, Pull{
			Number:  g.Number,
			Title:   g.Title,
			Branch:  g.HeadRefName,
			Base:    g.BaseRefName,
			Author:  g.Author.Login,
			Fork:    g.IsCrossRepository,
			Draft:   g.IsDraft,
			URL:     g.URL,
			Updated: g.UpdatedAt,
			Role:    role,
		})
	}
	for _, g := range asked {
		add(g, "review")
	}
	for _, g := range involved {
		add(g, roleOf(me, g))
	}
	sort.SliceStable(out, func(i, j int) bool {
		if roles[out[i].Role] != roles[out[j].Role] {
			return roles[out[i].Role] < roles[out[j].Role]
		}
		return out[i].Updated.After(out[j].Updated)
	})
	return out
}

// roleOf is why a row from involves:@me concerns me.
func roleOf(me string, g ghPull) string {
	for _, r := range g.ReviewRequests {
		if strings.EqualFold(r.Login, me) {
			return "review"
		}
	}
	if strings.EqualFold(g.Author.Login, me) {
		return "yours"
	}
	for _, a := range g.Assignees {
		if strings.EqualFold(a.Login, me) {
			return "assigned"
		}
	}
	return "involved"
}

// Link marks each pull with the worktree that has its branch out, and
// answers the ones still without one — the checkouts worth making.
func Link(pulls []Pull, wts []Worktree) (all, unchecked []Pull) {
	byBranch := map[string]Worktree{}
	for _, wt := range wts {
		if wt.Branch != "" {
			byBranch[wt.Branch] = wt
		}
	}
	for _, p := range pulls {
		if wt, ok := byBranch[p.Branch]; ok {
			p.Worktree = wt.Name
			if wt.Main {
				p.Worktree = "main"
			}
		} else {
			unchecked = append(unchecked, p)
		}
		all = append(all, p)
	}
	return all, unchecked
}

// Adopt creates a worktree named name on an existing branch whose
// name is not a worktree name — a PR's seth/fix-thing — from the
// local branch if there is one, else origin's, tracked. Unlike New it
// never invents a branch: a branch that exists nowhere is an error.
func (r Repo) Adopt(name, branch string, conv Conventions) (Worktree, error) {
	if err := checkName(name); err != nil {
		return Worktree{}, err
	}
	path := r.Path(name)
	if exists(path) {
		return Worktree{}, fmt.Errorf("%s already exists", path)
	}
	args := []string{"worktree", "add"}
	switch {
	case r.hasRef("refs/heads/" + branch):
		args = append(args, path, branch)
	case r.hasRef("refs/remotes/origin/" + branch):
		args = append(args, "--track", "-b", branch, path, "origin/"+branch)
	default:
		return Worktree{}, fmt.Errorf("no branch %s here or on origin (grove new --fetch?)", branch)
	}
	if out, err := git(r.Root, args...); err != nil {
		return Worktree{}, fmt.Errorf("git worktree add: %s", strings.TrimSpace(out))
	}
	r.apply(path, conv)
	return r.Get(name)
}

// Checkout puts the pull request's branch in a worktree named for it
// and answers the row: the worktree that already has the branch, else
// a new one. The branch is fetched when it is not here yet — from the
// PR itself when the head lives in a fork, so a contributor's branch
// needs no remote of its own.
func (r Repo) Checkout(p Pull, conv Conventions) (Worktree, error) {
	if p.Branch == "" {
		return Worktree{}, fmt.Errorf("pull request #%d has no branch", p.Number)
	}
	if wt, err := r.Get(p.Branch); err == nil {
		return wt, nil
	}
	if !r.hasRef("refs/heads/" + p.Branch) {
		spec := "+refs/heads/" + p.Branch + ":refs/remotes/origin/" + p.Branch
		if p.Fork {
			spec = "refs/pull/" + strconv.Itoa(p.Number) + "/head:refs/heads/" + p.Branch
		}
		if out, err := git(r.Root, "fetch", "--quiet", "origin", spec); err != nil {
			return Worktree{}, fmt.Errorf("git fetch: %s", strings.TrimSpace(out))
		}
	}
	return r.Adopt(p.Name(), p.Branch, conv)
}
