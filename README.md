# grove

One git worktree per task, and the place you work in it. A thin layer
over `git worktree` with a lifecycle — new, open, merge, remove — and
one small seam for whatever owns your sessions: a rook workspace, a
tmux session, or nothing at all.

```
brew install --cask incantery/tap/grove                  # macOS
go install github.com/incantery/grove/cmd/grove@latest   # anywhere with Go
```

## What it is

```
grove                          the manager: rows, and the lifecycle on keys
grove ls                       the rows, in a pipe or for the eye
grove new feature              a worktree on branch feature, opened
grove new theirs --fetch       …on origin's branch, tracked, if origin has it
grove open feature             go there; the session is made if it must be
grove pr                       the open pull requests that concern you (gh)
grove pr 420                   that PR's branch in a worktree, opened
grove merge feature            land it on the default branch; remove all three
grove rm feature [--force]     remove without merging
grove path feature             the directory, for cd "$(grove path feature)"
grove where                    which place worktrees are worked in here
```

Every verb works from inside any checkout of the repo: a worktree
answers with its true home.

A bare `grove` in a terminal is the manager, a Bubble Tea program:
the worktrees as rows with live state, `enter` opens, `n` names a new
one, `m` merges it home, `d` removes it (`D` without asking about the
branch), `r` refreshes, `q` leaves. Below the worktrees sit the open
pull requests that concern you and have no checkout yet; `enter` on
one makes the worktree and opens it. It draws to whatever size it is
given, so a multiplexer's popup is the same program — rook's
`prefix-w` floats it. Opening from outside a session lands you in it
when the manager exits.

## The layout

A worktree for repo `R` named `N` lives beside the repo at
`<parent>/R--N`, on a branch named `N`, in a session named `R--N`.
Unambiguous across repos, and it shows up in zoxide like any other
directory. rook and vera's fleet kept this convention before grove
did, so their worktrees and grove's are the same worktrees.

## Which branch

`grove new <name>` decides in three tiers and stops at the first that
answers: the local branch `<name>` if there is one; `origin/<name>`,
tracked, if origin has it; else a fresh branch off `--from` (the
default branch when empty). Nothing here needs the network. `--fetch`
asks origin first, for the branch somebody else pushed an hour ago.

## Which branches

The branches most worth a worktree are the ones with an open pull
request that concerns you. `grove pr` asks GitHub, through the `gh`
CLI you are already logged in to, for the open PRs on this repo where
your review was asked, you are the author (pushed from another
machine, say), you are assigned, or you were mentioned — in that
order, newest first — and says which worktree has each. `grove pr
420` (or the manager's `enter` on the row) puts #420's branch in a
worktree named for it — `seth/fix-thing` becomes `seth-fix-thing` —
fetching it if it is not here yet, from the PR itself when the head
lives in a fork, and opens it. A repo without `gh`, or without a
GitHub remote, simply has no pull requests to show.

## The place

Git owns the worktrees. Something else owns the place you sit to work
in one, and grove asks it three things: make a session named X at
directory P and go there, end session X, and which sessions exist.

| place | detected by | open | close |
| --- | --- | --- | --- |
| rook | `ROOK_MUX_PANE`, or `rook` on PATH | `rook new R--N <dir>` | `rook close R--N` |
| tmux | `TMUX` | `tmux new-session -d -s R--N -c <dir>`, then `switch-client` when inside | `tmux kill-session -t =R--N` |
| none | nothing above | prints nothing; the path is the answer | nothing |

`GROVE_PLACE=rook|tmux|none` overrides detection. The Go interface is
four methods; a new multiplexer is a few lines against it.

## What a checkout needs that git does not carry

```toml
# grove.toml, at the repo root — facts about this repo
copy = [".env", "config/local.toml"]   # copied from the main checkout
link = ["node_modules"]                # symlinked to it: too heavy to have twice
```

The same two lists in `~/.config/grove/grove.toml` apply to every
repo, merged. A `[worktree]` table in a `rook.toml` at the repo root
is read too, since that is where rook and vera's fleet kept them
before grove existed.

## For programs

`grove ls --json` and `grove new --json` print the rows: name, path,
branch, head, main, dirty, ahead, behind, session, live. `grove pr
--json` prints the pull requests: number, title, branch, base, author,
fork, draft, url, updated, role, worktree. The Go
package is the same thing without the process — `grove.Find`,
`Repo.New`, `Repo.Merge`, and the `Place` interface — and it imports
the standard library and nothing else. The command, with its UI, is a
module of its own under `cmd/grove`, so a program that wants the
model does not inherit a terminal.
