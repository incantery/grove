// The manager: the repo's worktrees as rows, with the lifecycle on
// keys. One program for both homes — `grove` in a terminal, and a
// multiplexer's popup — because it draws to whatever size it is
// given, and every row is the same row `grove ls` prints.
package main

import (
	"errors"
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

// pullsMsg is GitHub's answer: the open pull requests that concern
// the person, or why it could not be asked.
type pullsMsg struct {
	pulls []grove.Pull
	err   error
}

type pullTickMsg struct{}

// pullsEvery is how often GitHub is asked again; gh is slow and rate
// limited where git is neither.
const pullsEvery = 90 * time.Second

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
	all     []grove.Pull   // open PRs concerning the person, as GitHub answered
	pulls   []grove.Pull   // the ones without a worktree yet: the section below the rows
	byPR    map[string]int // branch → PR number, for the worktree rows that have one
	pullErr error
	noGH    bool // gh is missing or logged out: no pull request section
	cursor  int  // over rows then pulls, one list
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
	return tea.Batch(m.load(), m.loadPulls(), tick(), pullTick())
}

func tick() tea.Cmd {
	return tea.Tick(refreshEvery, func(time.Time) tea.Msg { return tickMsg{} })
}

func pullTick() tea.Cmd {
	return tea.Tick(pullsEvery, func(time.Time) tea.Msg { return pullTickMsg{} })
}

// loadPulls asks GitHub off the UI thread.
func (m model) loadPulls() tea.Cmd {
	if m.noGH {
		return nil
	}
	repo := m.repo
	return func() tea.Msg {
		pulls, err := repo.PullRequests()
		return pullsMsg{pulls: pulls, err: err}
	}
}

// entries is how many rows the cursor can be on: worktrees, then
// pull requests.
func (m model) entries() int { return len(m.rows) + len(m.pulls) }

// currentPull is the pull request under the cursor, when it is on one.
func (m model) currentPull() (grove.Pull, bool) {
	i := m.cursor - len(m.rows)
	if i < 0 || i >= len(m.pulls) {
		return grove.Pull{}, false
	}
	return m.pulls[i], true
}

// relink joins the pull requests to the worktrees: the ones with a
// checkout mark their row, the rest are the section below.
func (m *model) relink() {
	all, unchecked := grove.Link(m.all, m.rows)
	m.pulls = unchecked
	m.byPR = map[string]int{}
	for _, p := range all {
		if p.Worktree != "" {
			m.byPR[p.Branch] = p.Number
		}
	}
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
	case pullTickMsg:
		if m.mode == modeBusy {
			return m, pullTick()
		}
		return m, tea.Batch(m.loadPulls(), pullTick())
	case rowsMsg:
		if msg.err != nil {
			m.err = msg.err
			return m, nil
		}
		// keep the cursor on the same worktree, or pull request,
		// across a reload
		keepPath, keepPR := "", 0
		if cur, ok := m.current(); ok {
			keepPath = cur.Path
		} else if p, ok := m.currentPull(); ok && len(m.rows) > 0 {
			// (the first rows to arrive put the cursor on the first
			// worktree, even when GitHub answered before git did)
			keepPR = p.Number
		}
		m.rows = msg.rows
		m.relink()
		m.cursor = 0
		for i, r := range m.rows {
			if r.Path == keepPath {
				m.cursor = i
			}
		}
		for i, p := range m.pulls {
			if p.Number == keepPR {
				m.cursor = len(m.rows) + i
			}
		}
		return m, nil
	case pullsMsg:
		if errors.Is(msg.err, grove.ErrNoGH) {
			m.noGH, m.pullErr, m.pulls = true, nil, nil
			return m, nil
		}
		m.pullErr = msg.err
		if msg.err != nil {
			return m, nil
		}
		keepPR := 0
		if p, ok := m.currentPull(); ok {
			keepPR = p.Number
		}
		m.all = msg.pulls
		m.relink()
		if m.cursor >= m.entries() {
			m.cursor = max(0, m.entries()-1)
		}
		for i, p := range m.pulls {
			if p.Number == keepPR {
				m.cursor = len(m.rows) + i
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
		if m.cursor < m.entries()-1 {
			m.cursor++
		}
	case "k", "up":
		if m.cursor > 0 {
			m.cursor--
		}
	case "g", "home":
		m.cursor = 0
	case "G", "end":
		m.cursor = max(0, m.entries()-1)
	case "r":
		return m, tea.Batch(m.load(), m.loadPulls())
	case "enter", "o":
		if cur, ok := m.current(); ok {
			m.mode = modeBusy
			return m, open(m.repo, cur)
		}
		if p, ok := m.currentPull(); ok {
			m.mode = modeBusy
			return m, checkout(m.repo, p, m.conv)
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

// checkout puts the pull request's branch in a worktree and opens it.
func checkout(repo grove.Repo, p grove.Pull, conv grove.Conventions) tea.Cmd {
	return func() tea.Msg {
		wt, err := repo.Checkout(p, conv)
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
		if n := m.byPR[r.Branch]; n > 0 {
			notes = append(notes, fmt.Sprintf("#%d", n))
		}
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

	// The pull requests that concern the person and have no worktree
	// yet: the checkouts worth making, a keystroke from being made.
	if !m.noGH && (len(m.pulls) > 0 || m.pullErr != nil) {
		b.WriteString("\n" + dim.Render("pull requests for you") + "\n")
		if m.pullErr != nil {
			b.WriteString(dim.Render("  "+m.pullErr.Error()) + "\n")
		}
		branchW := 12
		for _, p := range m.pulls {
			branchW = max(branchW, len([]rune(p.Branch)))
		}
		branchW = min(branchW, 32)
		now := time.Now()
		for i, p := range m.pulls {
			branch := p.Branch
			if rs := []rune(branch); len(rs) > branchW {
				branch = string(rs[:branchW-1]) + "…"
			}
			pad := strings.Repeat(" ", branchW-len([]rune(branch)))
			why := p.Said()
			if p.Draft {
				why += " · draft"
			}
			head := fmt.Sprintf("#%-5d %s", p.Number, branch)
			rest := fmt.Sprintf("%s  %-16s %-4s %s", pad, why, p.Age(now), dim.Render(p.Title))
			if len(m.rows)+i == m.cursor {
				b.WriteString(selected.Render("▸ "+head) + rest + "\n")
			} else {
				b.WriteString(dim.Render("○") + " " + head + rest + "\n")
			}
		}
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
			// one line: git's multi-line advice would push the rows up
			b.WriteString(errStyle.Render(strings.Join(strings.Fields(m.err.Error()), " ")))
		} else if m.note != "" {
			b.WriteString(accent.Render(m.note))
		} else {
			if _, ok := m.currentPull(); ok {
				b.WriteString(dim.Render("enter check out and open · n new · r refresh · q quit"))
			} else {
				b.WriteString(dim.Render("enter open · n new · m merge home · d remove (D force) · r refresh · q quit"))
			}
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
