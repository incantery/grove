// The manager: the repo's worktrees as rows, with the lifecycle on
// keys. One program for both homes — `grove` in a terminal, and a
// multiplexer's popup — because it draws to whatever size it is
// given, and every row is the same row `grove ls` prints.
package main

import (
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/incantery/grove"
)

// ANSI names on purpose: the terminal owns the colours, grove owns
// emphasis.
var (
	accent   = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
	dim      = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	bold     = lipgloss.NewStyle().Bold(true)
	selected = lipgloss.NewStyle().Foreground(lipgloss.Color("3")).Background(lipgloss.Color("8")).Bold(true)
	errStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
)

const refreshEvery = 2 * time.Second

type mode int

const (
	modeList mode = iota
	modeNaming
	modeConfirm
	modeBusy
)

type rowsMsg struct {
	rows []grove.Worktree
	err  error
}

type tickMsg struct{}

type doneMsg struct {
	note string
	err  error
	// attach names a session to land in when the program exits: the
	// person opened a worktree from outside any session.
	attach string
}

type model struct {
	repo    grove.Repo
	conv    grove.Conventions
	rows    []grove.Worktree
	cursor  int
	width   int
	height  int
	mode    mode
	input   textinput.Model
	confirm string // the pending destructive verb: "merge" or "remove"
	force   bool
	note    string
	err     error
	attach  string
}

// manage shows the manager and blocks until it exits. It returns the
// session to attach to, when the person opened a worktree from
// outside a session (inside one, the place has already switched).
func manage(repo grove.Repo, conv grove.Conventions) (attach string, err error) {
	in := textinput.New()
	in.Prompt = accent.Render("new worktree ") + dim.Render(repo.Name+"--")
	in.CharLimit = 64
	m := model{repo: repo, conv: conv, input: in}
	out, err := tea.NewProgram(m).Run()
	if err != nil {
		return "", err
	}
	final := out.(model)
	return final.attach, final.err
}

func (m model) Init() tea.Cmd {
	return tea.Batch(m.load(), tick())
}

func tick() tea.Cmd {
	return tea.Tick(refreshEvery, func(time.Time) tea.Msg { return tickMsg{} })
}

// load re-reads the worktrees off the UI thread.
func (m model) load() tea.Cmd {
	repo := m.repo
	return func() tea.Msg {
		wts, err := repo.List()
		return rowsMsg{rows: wts, err: err}
	}
}

func (m model) current() (grove.Worktree, bool) {
	if m.cursor < 0 || m.cursor >= len(m.rows) {
		return grove.Worktree{}, false
	}
	return m.rows[m.cursor], true
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil
	case tickMsg:
		if m.mode == modeBusy {
			return m, tick()
		}
		return m, tea.Batch(m.load(), tick())
	case rowsMsg:
		if msg.err != nil {
			m.err = msg.err
			return m, nil
		}
		// keep the cursor on the same worktree across a reload
		var keep string
		if cur, ok := m.current(); ok {
			keep = cur.Path
		}
		m.rows = msg.rows
		m.cursor = 0
		for i, r := range m.rows {
			if r.Path == keep {
				m.cursor = i
			}
		}
		return m, nil
	case doneMsg:
		m.mode = modeList
		m.err, m.note = msg.err, msg.note
		if msg.attach != "" || (msg.err == nil && msg.note == "opened") {
			m.attach = msg.attach
			return m, tea.Quit
		}
		return m, m.load()
	case tea.KeyPressMsg:
		switch m.mode {
		case modeNaming:
			return m.updateNaming(msg)
		case modeConfirm:
			return m.updateConfirm(msg)
		case modeBusy:
			if msg.String() == "ctrl+c" {
				return m, tea.Quit
			}
			return m, nil
		}
		return m.updateList(msg)
	}
	return m, nil
}

func (m model) updateList(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	m.err, m.note = nil, ""
	switch msg.String() {
	case "q", "esc", "ctrl+c":
		return m, tea.Quit
	case "j", "down":
		if m.cursor < len(m.rows)-1 {
			m.cursor++
		}
	case "k", "up":
		if m.cursor > 0 {
			m.cursor--
		}
	case "g", "home":
		m.cursor = 0
	case "G", "end":
		m.cursor = max(0, len(m.rows)-1)
	case "r":
		return m, m.load()
	case "enter", "o":
		if cur, ok := m.current(); ok {
			m.mode = modeBusy
			return m, open(m.repo, cur)
		}
	case "n":
		m.mode = modeNaming
		m.input.SetValue("")
		return m, m.input.Focus()
	case "m":
		if cur, ok := m.current(); ok && !cur.Main {
			m.mode, m.confirm, m.force = modeConfirm, "merge", false
		}
	case "d", "x":
		if cur, ok := m.current(); ok && !cur.Main {
			m.mode, m.confirm, m.force = modeConfirm, "remove", false
		}
	case "D", "X":
		if cur, ok := m.current(); ok && !cur.Main {
			m.mode, m.confirm, m.force = modeConfirm, "remove", true
		}
	}
	return m, nil
}

func (m model) updateNaming(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "ctrl+c":
		m.mode = modeList
		m.input.Blur()
		return m, nil
	case "enter":
		name := strings.TrimSpace(m.input.Value())
		m.input.Blur()
		if name == "" {
			m.mode = modeList
			return m, nil
		}
		m.mode = modeBusy
		return m, create(m.repo, name, m.conv)
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func (m model) updateConfirm(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	cur, ok := m.current()
	switch msg.String() {
	case "y", "enter":
		if !ok {
			m.mode = modeList
			return m, nil
		}
		m.mode = modeBusy
		if m.confirm == "merge" {
			return m, merge(m.repo, cur)
		}
		return m, remove(m.repo, cur, m.force)
	default:
		m.mode = modeList
		return m, nil
	}
}

func open(repo grove.Repo, wt grove.Worktree) tea.Cmd {
	return func() tea.Msg {
		if err := repo.Open(wt); err != nil {
			return doneMsg{err: err}
		}
		if grove.InsideSession() {
			return doneMsg{note: "opened"}
		}
		return doneMsg{note: "opened", attach: wt.Session}
	}
}

func create(repo grove.Repo, name string, conv grove.Conventions) tea.Cmd {
	return func() tea.Msg {
		wt, err := repo.New(name, "", conv)
		if err != nil {
			return doneMsg{err: err}
		}
		return open(repo, wt)()
	}
}

func merge(repo grove.Repo, wt grove.Worktree) tea.Cmd {
	return func() tea.Msg {
		if err := repo.Merge(wt.Name); err != nil {
			return doneMsg{err: err}
		}
		return doneMsg{note: fmt.Sprintf("merged %s into %s", wt.Branch, repo.DefaultBranch())}
	}
}

func remove(repo grove.Repo, wt grove.Worktree, force bool) tea.Cmd {
	return func() tea.Msg {
		if err := repo.Remove(wt, force); err != nil {
			return doneMsg{err: err}
		}
		return doneMsg{note: "removed " + displayName(repo, wt)}
	}
}

func displayName(repo grove.Repo, wt grove.Worktree) string {
	switch {
	case wt.Main:
		return repo.Name
	case wt.Name != "":
		return wt.Name
	default:
		return wt.Path[strings.LastIndex(wt.Path, "/")+1:]
	}
}

func (m model) View() tea.View {
	var b strings.Builder
	b.WriteString(bold.Render(accent.Render("⌥ grove")) + "  " + dim.Render(m.repo.Name) + "  " + dim.Render(m.repo.Place.Name()) + "\n\n")

	nameW := 12
	for _, r := range m.rows {
		nameW = max(nameW, len(displayName(m.repo, r)))
	}
	nameW = min(nameW, 28)

	for i, r := range m.rows {
		name := displayName(m.repo, r)
		if rs := []rune(name); len(rs) > nameW {
			name = string(rs[:nameW-1]) + "…"
		}
		mark := dim.Render("○")
		if r.Live {
			mark = accent.Render("●")
		}
		branch := r.Branch
		if branch == "" {
			branch = "detached " + r.Head
		}
		var notes []string
		if r.Live {
			notes = append(notes, "live")
		}
		if r.Dirty {
			notes = append(notes, "dirty")
		}
		if r.Ahead > 0 {
			notes = append(notes, fmt.Sprintf("+%d", r.Ahead))
		}
		if r.Behind > 0 {
			notes = append(notes, fmt.Sprintf("-%d", r.Behind))
		}
		pad := strings.Repeat(" ", nameW-len([]rune(name)))
		rest := fmt.Sprintf("%s  ⎇ %-24s %s", pad, branch, dim.Render(strings.Join(notes, " ")))
		if i == m.cursor {
			b.WriteString(selected.Render("▸ "+name) + rest + "\n")
		} else {
			b.WriteString(mark + " " + name + rest + "\n")
		}
	}
	if len(m.rows) == 0 {
		b.WriteString(dim.Render("  (loading…)") + "\n")
	}

	b.WriteString("\n")
	switch m.mode {
	case modeNaming:
		b.WriteString(m.input.View() + "\n" + dim.Render("enter create · esc cancel"))
	case modeConfirm:
		cur, _ := m.current()
		verb := m.confirm
		if m.force {
			verb = "force-remove"
		}
		b.WriteString(accent.Render(fmt.Sprintf("%s %s? ", verb, displayName(m.repo, cur))) + dim.Render("y/enter confirm · any key cancel"))
	case modeBusy:
		b.WriteString(dim.Render("working…"))
	default:
		if m.err != nil {
			b.WriteString(errStyle.Render(m.err.Error()))
		} else if m.note != "" {
			b.WriteString(accent.Render(m.note))
		} else {
			b.WriteString(dim.Render("enter open · n new · m merge home · d remove (D force) · r refresh · q quit"))
		}
	}
	out := b.String()
	if m.width > 0 {
		out = lipgloss.NewStyle().MaxWidth(m.width).Render(out)
	}
	v := tea.NewView(out)
	v.AltScreen = true
	return v
}
