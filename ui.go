package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

var (
	dimStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	liveStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	idStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("6"))
	renamedStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
	cursorStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("5")).Bold(true)
	boldStyle    = lipgloss.NewStyle().Bold(true)
	warnStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("1")).Bold(true)
	noteStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
)

// ---------------------------------------------------------------- list rows

type projectItem struct{ p *Project }
type sessionItem struct{ s *Session }

func (i projectItem) FilterValue() string { return i.p.Name() }
func (i sessionItem) FilterValue() string {
	return i.s.Title + " " + i.s.FirstPrompt + " " + i.s.ID
}

type rowDelegate struct{}

func (rowDelegate) Height() int                         { return 1 }
func (rowDelegate) Spacing() int                        { return 0 }
func (rowDelegate) Update(tea.Msg, *list.Model) tea.Cmd { return nil }

func (rowDelegate) Render(w io.Writer, m list.Model, index int, item list.Item) {
	sel := index == m.Index()
	strong := func(s string) string {
		if sel {
			return boldStyle.Render(s)
		}
		return s
	}
	dot := func(live bool) string {
		if live {
			return liveStyle.Render("●")
		}
		return " "
	}

	var line string
	switch it := item.(type) {
	case projectItem:
		p := it.p
		line = fmt.Sprintf("%s %s  %s  %s", dot(p.Live()),
			idStyle.Render(fmt.Sprintf("%3d", len(p.Sessions))),
			dimStyle.Render(fmtTime(p.Modified())), strong(p.Name()))
	case sessionItem:
		s := it.s
		var title string
		switch s.Kind {
		case Renamed:
			title = renamedStyle.Bold(sel).Render("✎ " + s.Title)
		case AITitle:
			title = strong("  " + s.Title)
		default:
			title = dimStyle.Render("  " + s.Label())
		}
		line = fmt.Sprintf("%s %s  %s  %s  %s", dot(s.LivePID != 0), idStyle.Render(s.ID),
			dimStyle.Render(fmtTime(s.Modified)), dimStyle.Render(fmt.Sprintf("%6s", fmtSize(s.Size))), title)
	}

	prefix := "  "
	if sel {
		prefix = cursorStyle.Render("▌") + " "
	}
	fmt.Fprint(w, prefix+ansi.Truncate(line, max(m.Width()-2, 0), "…"))
}

func newList(title string, extra ...key.Binding) list.Model {
	l := list.New(nil, rowDelegate{}, 0, 0)
	l.Title = title
	l.DisableQuitKeybindings() // q, Esc and ←/→ are handled in Update
	l.KeyMap.PrevPage = key.NewBinding(key.WithKeys("pgup", "b"), key.WithHelp("pgup", "prev page"))
	l.KeyMap.NextPage = key.NewBinding(key.WithKeys("pgdown", "f"), key.WithHelp("pgdn", "next page"))
	l.AdditionalShortHelpKeys = func() []key.Binding { return extra }
	l.AdditionalFullHelpKeys = func() []key.Binding { return extra }
	return l
}

var (
	keyOpen   = key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "open"))
	keyView   = key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "view"))
	keyCode   = key.NewBinding(key.WithKeys("o"), key.WithHelp("o", "VS Code"))
	keyDelete = key.NewBinding(key.WithKeys("D"), key.WithHelp("D", "delete"))
	keyBack   = key.NewBinding(key.WithKeys("left"), key.WithHelp("←", "back"))
	keyRescan = key.NewBinding(key.WithKeys("r"), key.WithHelp("r", "rescan"))
	keyQuit   = key.NewBinding(key.WithKeys("q"), key.WithHelp("q", "quit"))
)

// ---------------------------------------------------------------- model

type screen int

const (
	projectsScreen screen = iota
	sessionsScreen
)

type model struct {
	projects []*Project
	cur      *Project // project whose sessions are shown
	screen   screen
	projList list.Model
	sessList list.Model
	confirm  *Session // pending delete, waiting for y/N
	footer   string
}

type execDoneMsg struct {
	what string
	err  error
}

type rescannedMsg struct {
	projects []*Project
	err      error
}

func newModel(projects []*Project, startIn string) model {
	m := model{
		projList: newList("Claude Code sessions", keyOpen, keyRescan, keyQuit),
		sessList: newList("", keyView, keyCode, keyDelete, keyBack, keyQuit),
	}
	m.projList.SetStatusBarItemName("project", "projects")
	m.sessList.SetStatusBarItemName("session", "sessions")
	m.setProjects(projects)
	for _, p := range projects {
		if p.Dir == startIn {
			m.openProject(p)
		}
	}
	return m
}

func (m *model) setProjects(projects []*Project) tea.Cmd {
	m.projects = projects
	items := make([]list.Item, len(projects))
	for i, p := range projects {
		items[i] = projectItem{p}
	}
	return m.projList.SetItems(items)
}

func (m *model) openProject(p *Project) tea.Cmd {
	m.cur, m.screen = p, sessionsScreen
	m.sessList.Title = p.Name()
	m.sessList.ResetFilter()
	cmd := m.setSessions()
	m.sessList.Select(0)
	return cmd
}

func (m *model) setSessions() tea.Cmd {
	items := make([]list.Item, len(m.cur.Sessions))
	for i, s := range m.cur.Sessions {
		items[i] = sessionItem{s}
	}
	return m.sessList.SetItems(items)
}

func (m *model) back() {
	m.screen = projectsScreen
	for i, p := range m.projects { // keep the cursor on the project we came from
		if p == m.cur {
			m.projList.Select(i)
		}
	}
}

func (m *model) active() *list.Model {
	if m.screen == projectsScreen {
		return &m.projList
	}
	return &m.sessList
}

func (m *model) selectedSession() *Session {
	if it, ok := m.sessList.SelectedItem().(sessionItem); ok {
		return it.s
	}
	return nil
}

func (m model) Init() tea.Cmd { return nil }

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.projList.SetSize(msg.Width, msg.Height-1) // one line for the footer
		m.sessList.SetSize(msg.Width, msg.Height-1)
		return m, nil

	case execDoneMsg:
		if msg.err != nil {
			m.footer = fmt.Sprintf("%s: %v", msg.what, msg.err)
		}
		return m, nil

	case rescannedMsg:
		if msg.err != nil {
			m.footer = "rescan: " + msg.err.Error()
			return m, nil
		}
		prev := m.cur
		cmd := m.setProjects(msg.projects)
		m.screen, m.cur = projectsScreen, nil
		for _, p := range msg.projects {
			if prev != nil && p.Dir == prev.Dir {
				cmd = tea.Batch(cmd, m.openProject(p))
			}
		}
		m.footer = "rescanned"
		return m, cmd

	case tea.KeyPressMsg:
		if m.confirm != nil {
			s := m.confirm
			m.confirm = nil
			if msg.String() == "y" || msg.String() == "Y" {
				return m, m.deleteSession(s)
			}
			m.footer = "kept"
			return m, nil
		}
		if msg.String() == "ctrl+c" {
			return m, tea.Quit
		}
		l := m.active()
		if l.SettingFilter() { // typing a filter: every key belongs to the list
			break
		}
		m.footer = ""
		switch msg.String() {
		case "q":
			return m, tea.Quit
		case "r":
			return m, func() tea.Msg {
				p, err := scanAll()
				return rescannedMsg{p, err}
			}
		case "enter", "right", "l":
			if m.screen == projectsScreen {
				if it, ok := m.projList.SelectedItem().(projectItem); ok {
					return m, m.openProject(it.p)
				}
				return m, nil
			}
			if s := m.selectedSession(); s != nil {
				return m, m.view(s)
			}
			return m, nil
		case "left", "h", "backspace":
			if m.screen == sessionsScreen {
				m.back()
			}
			return m, nil
		case "esc":
			if l.IsFiltered() {
				break // let the list clear its filter
			}
			if m.screen == sessionsScreen {
				m.back()
			}
			return m, nil
		case "D", "d", "delete":
			if s := m.selectedSession(); m.screen == sessionsScreen && s != nil {
				if s.LivePID != 0 {
					m.footer = fmt.Sprintf("%s is open in a running Claude Code (pid %d); exit it first", s.ID[:8], s.LivePID)
				} else {
					m.confirm = s
				}
			}
			return m, nil
		case "o":
			if s := m.selectedSession(); m.screen == sessionsScreen && s != nil {
				m.openInCode(s)
			}
			return m, nil
		}
	}

	var cmd tea.Cmd
	if m.screen == projectsScreen {
		m.projList, cmd = m.projList.Update(msg)
	} else {
		m.sessList, cmd = m.sessList.Update(msg)
	}
	return m, cmd
}

func (m model) View() tea.View {
	body := m.projList.View()
	if m.screen == sessionsScreen {
		body = m.sessList.View()
	}
	foot := noteStyle.Render(m.footer)
	if s := m.confirm; s != nil {
		foot = warnStyle.Render(fmt.Sprintf("Delete %s “%s”? This cannot be undone. [y/N] ",
			s.ID[:8], ansi.Truncate(s.Label(), 50, "…")))
	}
	v := tea.NewView(body + "\n " + foot)
	v.AltScreen = true
	return v
}

// ---------------------------------------------------------------- actions

func (m *model) view(s *Session) tea.Cmd {
	md, err := s.Markdown()
	if err != nil {
		m.footer = "read: " + err.Error()
		return nil
	}
	f, err := os.CreateTemp("", "ccs-*.md")
	if err != nil {
		m.footer = err.Error()
		return nil
	}
	f.WriteString(md)
	f.Close()

	var c *exec.Cmd
	if _, err := exec.LookPath("glow"); err == nil {
		c = exec.Command("glow", "-p", f.Name())
	} else {
		c = exec.Command("less", "-R", f.Name())
	}
	return tea.ExecProcess(c, func(err error) tea.Msg {
		os.Remove(f.Name())
		return execDoneMsg{"viewer", err}
	})
}

// openInCode writes the markdown where ccview_md does and opens it in VS Code.
func (m *model) openInCode(s *Session) {
	md, err := s.Markdown()
	if err != nil {
		m.footer = "read: " + err.Error()
		return
	}
	out := filepath.Join(os.TempDir(), s.ID+".md")
	if err := os.WriteFile(out, []byte(md), 0o600); err != nil {
		m.footer = err.Error()
		return
	}
	if err := exec.Command("code", out).Start(); err != nil {
		m.footer = "wrote " + tildify(out) + " (could not start VS Code: " + err.Error() + ")"
		return
	}
	m.footer = "opened " + tildify(out) + " in VS Code"
}

func (m *model) deleteSession(s *Session) tea.Cmd {
	gone, err := s.Delete()
	if err != nil {
		m.footer = fmt.Sprintf("delete failed after %d paths: %v", len(gone), err)
		return nil
	}
	p := m.cur
	for i, x := range p.Sessions {
		if x == s {
			p.Sessions = append(p.Sessions[:i], p.Sessions[i+1:]...)
			break
		}
	}
	m.footer = fmt.Sprintf("deleted %s (%d paths)", s.ID[:8], len(gone))

	if len(p.Sessions) == 0 { // the folder stays (memory/ lives there); the row goes
		var kept []*Project
		for _, x := range m.projects {
			if x != p {
				kept = append(kept, x)
			}
		}
		m.screen, m.cur = projectsScreen, nil
		return m.setProjects(kept)
	}
	idx := m.sessList.Index()
	cmd := m.setSessions()
	m.sessList.Select(min(idx, len(m.sessList.VisibleItems())-1))
	return cmd
}
