// The command is its own module so that the library stays standard
// library only: a program that wants the model of a repo's worktrees
// should not inherit a terminal UI. This module has the UI.
module github.com/incantery/grove/cmd/grove

go 1.25

require (
	charm.land/bubbles/v2 v2.2.1
	charm.land/bubbletea/v2 v2.0.9
	charm.land/lipgloss/v2 v2.0.6
	github.com/incantery/grove v0.0.0-20260911121639-3e779dfa070e
)
