// Command grove: one worktree per task, and the place you work in it.
//
//	grove                          the repo's worktrees, with live state
//	grove new <name> [--from REF] [--fetch] [--no-open]
//	grove open <name>              go there (the session is made if it must be)
//	grove merge <name>             land the branch on the default branch; remove all three
//	grove rm <name> [--force]      remove the worktree, its session, and its branch
//	grove path <name>              the worktree's directory, for cd "$(grove path x)"
//	grove where                    which place worktrees are worked in here
//
// The repo is whichever one the current directory is in — a worktree
// answers with its true home, so every verb works from inside any
// checkout. The place is detected: a rook pane, a tmux session, rook
// on PATH, or nothing (GROVE_PLACE=rook|tmux|none overrides).
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"runtime/debug"
	"strings"

	"github.com/incantery/grove"
)

// version is stamped by the release; otherwise the module's own build
// info, so a `go install` still says something true.
var version = ""

func main() {
	args := os.Args[1:]
	if len(args) > 0 {
		switch args[0] {
		case "help", "-h", "--help":
			fmt.Print(usage)
			return
		case "version", "--version":
			fmt.Println("grove " + groveVersion())
			return
		}
	}
	if err := run(args); err != nil {
		fmt.Fprintln(os.Stderr, "grove: "+err.Error())
		os.Exit(1)
	}
}

func run(args []string) error {
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	repo, err := grove.Find(cwd)
	if err != nil {
		return err
	}
	if len(args) == 0 {
		return list(repo, false)
	}
	verb, rest := args[0], args[1:]
	switch verb {
	case "ls", "list":
		return list(repo, has(rest, "--json"))
	case "new", "add":
		name, from := "", ""
		fetch, open, asJSON := false, true, false
		for i := 0; i < len(rest); i++ {
			switch {
			case rest[i] == "--from" && i+1 < len(rest):
				i++
				from = rest[i]
			case rest[i] == "--fetch":
				fetch = true
			case rest[i] == "--no-open":
				open = false
			case rest[i] == "--json":
				asJSON = true
			case strings.HasPrefix(rest[i], "-"):
				return fmt.Errorf("new: unknown flag %s", rest[i])
			default:
				name = rest[i]
			}
		}
		if name == "" {
			return fmt.Errorf("usage: grove new <name> [--from REF] [--fetch] [--no-open]")
		}
		if fetch {
			if err := repo.Fetch(); err != nil {
				return err
			}
		}
		conv := grove.UserConventions().Merge(grove.LoadConventions(repo.Root))
		wt, err := repo.New(name, from, conv)
		if err != nil {
			return err
		}
		if open {
			if err := repo.Open(wt); err != nil {
				return err
			}
			wt.Live = repo.Place.Live()[wt.Session]
		}
		if asJSON {
			return json.NewEncoder(os.Stdout).Encode(wt)
		}
		fmt.Println(wt.Path)
		return nil
	case "open", "go":
		if len(rest) != 1 {
			return fmt.Errorf("usage: grove open <name>")
		}
		wt, err := repo.Get(rest[0])
		if rest[0] == repo.Name {
			wt, err = repo.Main()
		}
		if err != nil {
			return err
		}
		return repo.Open(wt)
	case "merge":
		if len(rest) != 1 {
			return fmt.Errorf("usage: grove merge <name>")
		}
		if err := repo.Merge(rest[0]); err != nil {
			return err
		}
		fmt.Printf("merged %s into %s; worktree, session and branch removed\n", rest[0], repo.DefaultBranch())
		return nil
	case "rm", "remove":
		force := has(rest, "--force") || has(rest, "-f")
		name := ""
		for _, a := range rest {
			if !strings.HasPrefix(a, "-") {
				name = a
			}
		}
		if name == "" {
			return fmt.Errorf("usage: grove rm <name> [--force]")
		}
		wt, err := repo.Get(name)
		if err != nil {
			return err
		}
		return repo.Remove(wt, force)
	case "path":
		if len(rest) != 1 {
			return fmt.Errorf("usage: grove path <name>")
		}
		if rest[0] == repo.Name {
			fmt.Println(repo.Root)
			return nil
		}
		wt, err := repo.Get(rest[0])
		if err != nil {
			return err
		}
		fmt.Println(wt.Path)
		return nil
	case "where":
		fmt.Println(repo.Place.Name())
		return nil
	}
	return fmt.Errorf("unknown command %q\n%s", verb, usage)
}

func list(repo grove.Repo, asJSON bool) error {
	wts, err := repo.List()
	if err != nil {
		return err
	}
	if asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(wts)
	}
	for _, wt := range wts {
		name := wt.Name
		if wt.Main {
			name = repo.Name
		}
		var marks []string
		if wt.Live {
			marks = append(marks, "live")
		}
		if wt.Dirty {
			marks = append(marks, "dirty")
		}
		if wt.Ahead > 0 {
			marks = append(marks, fmt.Sprintf("+%d", wt.Ahead))
		}
		if wt.Behind > 0 {
			marks = append(marks, fmt.Sprintf("-%d", wt.Behind))
		}
		branch := wt.Branch
		if branch == "" {
			branch = "(detached " + wt.Head + ")"
		}
		fmt.Printf("%-24s %-24s %-16s %s\n", name, branch, strings.Join(marks, " "), wt.Path)
	}
	return nil
}

func has(args []string, flag string) bool {
	for _, a := range args {
		if a == flag {
			return true
		}
	}
	return false
}

const usage = `grove — one worktree per task, and the place you work in it

usage:
  grove                          the repo's worktrees, with live state
  grove ls [--json]              the same, for programs
  grove new <name> [--from REF] [--fetch] [--no-open] [--json]
                                 a worktree on branch <name>: the local branch
                                 if there is one, origin's if origin has it
                                 (--fetch asks origin first), else a fresh one
                                 off REF (the default branch). Then opens it.
  grove open <name>              go there; the session is made if it must be
  grove merge <name>             land the branch on the default branch and
                                 remove the worktree, its session, its branch
  grove rm <name> [--force]      remove all three without merging
  grove path <name>              the directory, for cd "$(grove path <name>)"
  grove where                    which place worktrees are worked in here
  grove version

A worktree for repo R named N lives beside the repo at <parent>/R--N,
and its session is named R--N. The place is detected: a rook pane, a
tmux session, rook on PATH, else none (GROVE_PLACE=rook|tmux|none
overrides). What a fresh checkout needs that git does not carry —
copy = [".env"], link = ["node_modules"] — goes in grove.toml at the
repo root, and in ~/.config/grove/grove.toml for every repo.
`

func groveVersion() string {
	if version != "" {
		return version
	}
	if bi, ok := debug.ReadBuildInfo(); ok {
		if bi.Main.Version != "" && bi.Main.Version != "(devel)" {
			return bi.Main.Version
		}
		for _, s := range bi.Settings {
			if s.Key == "vcs.revision" && len(s.Value) >= 7 {
				return s.Value[:7]
			}
		}
	}
	return "dev"
}
