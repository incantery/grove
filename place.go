// Where you work in a checkout.
//
// Git owns the worktrees. Something else owns the place you sit to
// work in one — a rook workspace, a tmux session, or nothing at all
// when the caller is a script that only wanted the path. That
// something is a Place, and it is the only part of grove that knows
// any multiplexer exists: three verbs, each a few lines per program,
// chosen by the environment variables every multiplexer already sets.
package grove

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
)

// Place is where a worktree is worked in: the session side of the
// lifecycle. Open makes a session named session exist, rooted at dir,
// and goes there when the program can; Close ends it; Live lists the
// sessions that exist right now, by name.
type Place interface {
	Name() string
	Open(session, dir string) error
	Close(session string) error
	Live() map[string]bool
}

// InsideSession says whether this process runs inside a multiplexer's
// session — a rook pane or a tmux client. Opening a worktree from
// inside one switches there; from outside, there is a session to
// attach to afterwards.
func InsideSession() bool {
	return os.Getenv("ROOK_MUX_PANE") != "" || os.Getenv("TMUX") != ""
}

// Attacher is a Place that can put this terminal into a session: the
// thing to do after opening a worktree from outside one. Attach
// replaces the process and does not return on success.
type Attacher interface {
	Attach(session string) error
}

// Detect picks the Place from the environment: GROVE_PLACE when set
// (rook, tmux, none), else the multiplexer this process is inside,
// else rook if its front door is on PATH, else none.
func Detect() Place {
	switch strings.TrimSpace(os.Getenv("GROVE_PLACE")) {
	case "rook":
		return Rook{}
	case "tmux":
		return Tmux{}
	case "none":
		return None{}
	}
	if os.Getenv("ROOK_MUX_PANE") != "" {
		return Rook{}
	}
	if os.Getenv("TMUX") != "" {
		return Tmux{}
	}
	if _, err := exec.LookPath("rook"); err == nil {
		return Rook{}
	}
	return None{}
}

// None is no multiplexer: opening prints nothing and closes nothing.
// A caller that wanted the path has it from the worktree.
type None struct{}

func (None) Name() string           { return "none" }
func (None) Open(_, _ string) error { return nil }
func (None) Close(string) error     { return nil }
func (None) Live() map[string]bool  { return map[string]bool{} }

// Rook is a rook workspace per worktree, through rook's front door.
// `rook new` creates the workspace with its first window rooted at
// the directory, or switches to it when it exists; the server dedupes
// by name, so create and go are one verb. A stopped server lists
// nothing and closes nothing, which is not an error.
type Rook struct{}

func (Rook) Name() string { return "rook" }

func (Rook) Open(session, dir string) error {
	_, err := run("rook", "new", session, dir)
	return err
}

func (r Rook) Close(session string) error {
	if !r.Live()[session] {
		return nil
	}
	_, err := run("rook", "close", session)
	return err
}

// Attach makes this terminal a rook client landing in the workspace.
func (Rook) Attach(session string) error {
	return become("rook", "--space", session)
}

func (Rook) Live() map[string]bool {
	out, err := run("rook", "ls")
	if err != nil {
		return map[string]bool{}
	}
	return lines(out)
}

// Tmux is a tmux session per worktree. Inside tmux, opening switches
// the client there; outside, the session is made detached and the
// person attaches when they like. Names are matched exactly (`=`), so
// a session named for one worktree never answers for another whose
// name it prefixes.
type Tmux struct{}

func (Tmux) Name() string { return "tmux" }

func (t Tmux) Open(session, dir string) error {
	if !t.Live()[session] {
		if _, err := run("tmux", "new-session", "-d", "-s", session, "-c", dir); err != nil {
			return err
		}
	}
	if os.Getenv("TMUX") != "" {
		_, err := run("tmux", "switch-client", "-t", "="+session)
		return err
	}
	return nil
}

func (t Tmux) Close(session string) error {
	if !t.Live()[session] {
		return nil
	}
	_, err := run("tmux", "kill-session", "-t", "="+session)
	return err
}

// Attach makes this terminal a tmux client on the session.
func (Tmux) Attach(session string) error {
	return become("tmux", "attach-session", "-t", "="+session)
}

func (Tmux) Live() map[string]bool {
	out, err := run("tmux", "list-sessions", "-F", "#S")
	if err != nil {
		return map[string]bool{}
	}
	return lines(out)
}

// become replaces this process with the program, the way a shell's
// exec does; it returns only when that failed.
func become(name string, args ...string) error {
	path, err := exec.LookPath(name)
	if err != nil {
		return err
	}
	return syscall.Exec(path, append([]string{name}, args...), os.Environ())
}

// run runs a program and returns its combined output, with the
// program's own words as the error when it fails.
func run(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) && strings.TrimSpace(string(out)) != "" {
			return string(out), fmt.Errorf("%s %s: %s", name, args[0], strings.TrimSpace(string(out)))
		}
		return string(out), fmt.Errorf("%s %s: %w", name, args[0], err)
	}
	return string(out), nil
}

func lines(s string) map[string]bool {
	out := map[string]bool{}
	for _, l := range strings.Split(s, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			out[l] = true
		}
	}
	return out
}
