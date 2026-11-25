// Copyright 2026 Proxi projekt d.o.o.
// SPDX-License-Identifier: GPL-3.0-only

package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

var (
	dimStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	liveStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	busyStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
	waitStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("5"))
	idStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("6"))
	renamedStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
	cursorStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("5")).Bold(true)
	boldStyle    = lipgloss.NewStyle().Bold(true)
	plainStyle   = lipgloss.NewStyle()
	warnStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("1")).Bold(true)
	noteStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
	matchStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("0")).Background(lipgloss.Color("3"))
	youStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("5")).Bold(true)
	claudeStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("4")).Bold(true)

	// The key hints at the bottom. bubbles defaults to fixed dark greys that
	// almost vanish on a dark background; the terminal's own palette stays
	// readable on any theme: keys in the normal text colour, the rest in the
	// grey used for dates.
	helpStyles = help.Styles{
		ShortKey:       plainStyle,
		ShortDesc:      dimStyle,
		ShortSeparator: dimStyle,
		Ellipsis:       dimStyle,
		FullKey:        plainStyle,
		FullDesc:       dimStyle,
		FullSeparator:  dimStyle,
	}
)

// ---------------------------------------------------------------- list rows

type projectItem struct{ p *Project }
type sessionItem struct{ s *Session }

// hitItem is a search result: a session and where it mentions the query.
type hitItem struct {
	s     *Session
	p     *Project
	hit   Hit
	query string
}

func (i projectItem) FilterValue() string { return i.p.Name() }
func (i sessionItem) FilterValue() string {
	return i.s.Title + " " + i.s.FirstPrompt + " " + i.s.ID
}
func (i hitItem) FilterValue() string {
	return i.s.Label() + " " + i.p.Name() + " " + i.s.ID
}

// liveDot marks a session open in a running Claude Code: green when it is
// idle (your turn), yellow while it works, magenta when it waits on a prompt.
func liveDot(live bool, status string) string {
	switch {
	case !live:
		return " "
	case status == "busy":
		return busyStyle.Render("●")
	case status == "waiting":
		return waitStyle.Render("●")
	}
	return liveStyle.Render("●")
}

func sessionTitle(s *Session, sel bool) string {
	switch s.Kind {
	case Renamed:
		return renamedStyle.Bold(sel).Render("✎ " + s.Title)
	case AITitle:
		return plainStyle.Bold(sel).Render("  " + s.Title)
	}
	return dimStyle.Render("  " + s.Label())
}

type rowDelegate struct{ height int }

func (d rowDelegate) Height() int                       { return d.height }
func (rowDelegate) Spacing() int                        { return 0 }
func (rowDelegate) Update(tea.Msg, *list.Model) tea.Cmd { return nil }

func (rowDelegate) Render(w io.Writer, m list.Model, index int, item list.Item) {
	sel := index == m.Index()
	prefix := "  "
	if sel {
		prefix = cursorStyle.Render("▌") + " "
	}
	width := max(m.Width()-2, 0)

	var line string
	switch it := item.(type) {
	case projectItem:
		p := it.p
		live, status := p.Live()
		line = fmt.Sprintf("%s %s  %s  %s", liveDot(live, status),
			idStyle.Render(fmt.Sprintf("%3d", len(p.Sessions))),
			dimStyle.Render(fmtTime(p.Modified())), plainStyle.Bold(sel).Render(p.Name()))
	case sessionItem:
		s := it.s
		line = fmt.Sprintf("%s %s  %s  %s  %s", liveDot(s.LivePID != 0, s.LiveStatus), idStyle.Render(s.ID),
			dimStyle.Render(fmtTime(s.Modified)), dimStyle.Render(fmt.Sprintf("%6s", fmtSize(s.Size))),
			sessionTitle(s, sel))
	case hitItem: // two lines: the session, then where it mentions the query
		s := it.s
		line = fmt.Sprintf("%s %s  %s  %s  %s", liveDot(s.LivePID != 0, s.LiveStatus),
			dimStyle.Render(fmtTime(s.Modified)), idStyle.Render(fmt.Sprintf("%3d×", it.hit.Count)),
			sessionTitle(s, sel), dimStyle.Render("· "+it.p.Name()))
		fmt.Fprintln(w, prefix+ansi.Truncate(line, width, "…"))
		prefix, line = "  ", "    "+snippet(it.hit.Context, it.query, max(width-4, 0))
	}
	fmt.Fprint(w, prefix+ansi.Truncate(line, width, "…"))
}

// snippet is one line of the text around a match, with the match marked.
func snippet(context, query string, width int) string {
	text := []rune(strings.Join(strings.Fields(context), " "))
	q := []rune(strings.Join(strings.Fields(query), " "))
	at := max(indexFold(text, q), 0)
	if lead := width / 3; at > lead { // keep the match in the first third
		text = append([]rune("…"), text[at-lead:]...)
	}
	return highlight(ansi.Truncate(string(text), width, "…"), string(q), dimStyle)
}

// indexFold is strings.Index ignoring case, counted in runes.
func indexFold(text, q []rune) int {
	if len(q) == 0 {
		return -1
	}
	for i := 0; i+len(q) <= len(text); i++ {
		if strings.EqualFold(string(text[i:i+len(q)]), string(q)) {
			return i
		}
	}
	return -1
}

// highlight renders text in base with every mention of query marked.
func highlight(text, query string, base lipgloss.Style) string {
	re, err := regexp.Compile("(?i)" + regexp.QuoteMeta(query))
	if query == "" || err != nil {
		return base.Render(text)
	}
	var b strings.Builder
	last := 0
	for _, loc := range re.FindAllStringIndex(text, -1) {
		b.WriteString(base.Render(text[last:loc[0]]))
		b.WriteString(matchStyle.Render(text[loc[0]:loc[1]]))
		last = loc[1]
	}
	b.WriteString(base.Render(text[last:]))
	return b.String()
}

func newList(title string, rows int, short, full []key.Binding) list.Model {
	l := list.New(nil, rowDelegate{rows}, 0, 0)
	l.Title = title
	l.DisableQuitKeybindings() // q, Esc and ←/→ are handled in Update
	l.Help.Styles = helpStyles
	l.KeyMap.PrevPage = key.NewBinding(key.WithKeys("pgup", "b"), key.WithHelp("pgup", "prev page"))
	l.KeyMap.NextPage = key.NewBinding(key.WithKeys("pgdown", "f"), key.WithHelp("pgdn", "next page"))
	l.AdditionalShortHelpKeys = func() []key.Binding { return short }
	l.AdditionalFullHelpKeys = func() []key.Binding { return full }
	return l
}

var (
	keyOpen    = key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "open"))
	keyView    = key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "view"))
	keyResume  = key.NewBinding(key.WithKeys("c"), key.WithHelp("c", "resume"))
	keySearch  = key.NewBinding(key.WithKeys("s"), key.WithHelp("s", "search all"))
	keyPreview = key.NewBinding(key.WithKeys("p"), key.WithHelp("p", "preview"))
	keySide    = key.NewBinding(key.WithKeys("P"), key.WithHelp("P", "move preview"))
	keyCode    = key.NewBinding(key.WithKeys("o"), key.WithHelp("o", "VS Code"))
	keyDelete  = key.NewBinding(key.WithKeys("D"), key.WithHelp("D", "delete"))
	keyTag     = key.NewBinding(key.WithKeys("t"), key.WithHelp("t", "tag "+OpenTag))
	keyName    = key.NewBinding(key.WithKeys("n"), key.WithHelp("n", "rename"))
	keyBack    = key.NewBinding(key.WithKeys("left"), key.WithHelp("←", "back"))
	keyRescan  = key.NewBinding(key.WithKeys("r"), key.WithHelp("r", "rescan"))
	keyQuit    = key.NewBinding(key.WithKeys("q"), key.WithHelp("q", "quit"))
)

// ---------------------------------------------------------------- model

type screen int

const (
	projectsScreen screen = iota
	sessionsScreen
	resultsScreen // sessions that mention the search text
)

// placement is where the preview pane goes.
type placement int

const (
	placeAuto   placement = iota // right in a wide window, otherwise below
	placeRight                   // beside the list
	placeBottom                  // under the list
	placeNone                    // not shown
)

// parsePlacement reads the value of -preview.
func parsePlacement(s string) (place placement, show, ok bool) {
	switch s {
	case "auto":
		return placeAuto, true, true
	case "right":
		return placeRight, true, true
	case "bottom":
		return placeBottom, true, true
	case "off":
		return placeAuto, false, true
	}
	return placeAuto, true, false
}

type model struct {
	projects []*Project
	cur      *Project // project whose sessions are shown
	screen   screen
	projList list.Model
	sessList list.Model
	hitList  list.Model
	confirm  *Session // pending delete, waiting for y/N
	naming   *Session // being renamed: its new title is typed into input
	footer   string
	gen      int // bumped by every delete, so a scan started before it is dropped

	asking    bool   // typing a search in the bottom line
	input     string // what has been typed
	searching bool
	query     string // the search the results are for
	from      screen // where the search was started, for going back

	width, height int
	preview       bool      // the preview pane is wanted
	place         placement // where: placeAuto until P or -preview picks a side
	pvKey         string    // what pvLines was built for
	pvLines       []string  // its content
}

// The lists follow what other terminals do: every refreshEvery the session
// folders are checked again. That is a stat per transcript plus a read of
// whatever was appended (see scanSession), so it stays cheap with thousands
// of sessions. Polling, not file watching: on macOS a watcher holds a file
// descriptor per transcript, and it would fire constantly for a session that
// is being written.
const refreshEvery = 2 * time.Second

type tickMsg struct{}

func tick() tea.Cmd {
	return tea.Tick(refreshEvery, func(time.Time) tea.Msg { return tickMsg{} })
}

type execDoneMsg struct {
	what string
	err  error
}

type rescannedMsg struct {
	projects []*Project
	err      error
	gen      int
	manual   bool // asked for with r, not the periodic refresh
}

type searchDoneMsg struct {
	query string
	hits  []Hit
	total int // sessions looked at
	took  time.Duration
}

// scan reads the sessions in the background. The periodic one reuses what is
// already known; r reads every transcript from the start.
func (m *model) scan(manual bool) tea.Cmd {
	var known map[string]Session
	if !manual {
		known = knownSessions(m.projects)
	}
	gen := m.gen
	return func() tea.Msg {
		p, err := scanAll(known)
		return rescannedMsg{p, err, gen, manual}
	}
}

func (m *model) search(query string) tea.Cmd {
	paths := map[string]string{}
	for _, p := range m.projects {
		for _, s := range p.Sessions {
			paths[s.ID] = s.Path
		}
	}
	return func() tea.Msg {
		start := time.Now()
		hits := searchAll(paths, query)
		return searchDoneMsg{query, hits, len(paths), time.Since(start)}
	}
}

func newModel(projects []*Project, startIn string) model {
	keys := func(k ...key.Binding) []key.Binding { return k }
	m := model{
		projList: newList("Claude Code sessions", 1,
			keys(keyOpen, keySearch, keyQuit),
			keys(keyOpen, keySearch, keyPreview, keySide, keyRescan, keyQuit)),
		sessList: newList("", 1,
			keys(keyView, keyResume, keySearch, keyTag, keyDelete, keyBack),
			keys(keyView, keyResume, keyCode, keyTag, keyName, keyDelete, keySearch, keyPreview, keySide, keyBack, keyRescan, keyQuit)),
		hitList: newList("", 2,
			keys(keyView, keyResume, keySearch, keyBack),
			keys(keyView, keyResume, keyCode, keyTag, keyName, keyDelete, keySearch, keyPreview, keySide, keyBack, keyQuit)),
		preview: true,
	}
	m.projList.SetStatusBarItemName("project", "projects")
	m.sessList.SetStatusBarItemName("session", "sessions")
	m.hitList.SetStatusBarItemName("session", "sessions")
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

// setHits shows search results, newest session first.
func (m *model) setHits(hits []Hit) tea.Cmd {
	byID := map[string]Hit{}
	for _, h := range hits {
		byID[h.ID] = h
	}
	var items []list.Item
	for _, p := range m.projects {
		for _, s := range p.Sessions {
			if h, ok := byID[s.ID]; ok {
				items = append(items, hitItem{s, p, h, m.query})
			}
		}
	}
	sort.SliceStable(items, func(i, j int) bool {
		return items[i].(hitItem).s.Modified.After(items[j].(hitItem).s.Modified)
	})
	return m.hitList.SetItems(items)
}

// back leaves the current screen: results to where the search began, a
// project to the project list.
func (m *model) back() {
	switch m.screen {
	case resultsScreen:
		m.screen = m.from
		if m.screen == sessionsScreen && m.cur == nil {
			m.screen = projectsScreen
		}
	case sessionsScreen:
		m.screen = projectsScreen
		for i, p := range m.projects { // keep the cursor on the project we came from
			if p == m.cur && !m.projList.IsFiltered() {
				m.projList.Select(i)
			}
		}
	}
}

func (m *model) active() *list.Model {
	switch m.screen {
	case sessionsScreen:
		return &m.sessList
	case resultsScreen:
		return &m.hitList
	}
	return &m.projList
}

// selectedSession is the session under the cursor, on either screen that
// lists sessions.
func (m *model) selectedSession() *Session {
	switch it := m.active().SelectedItem().(type) {
	case sessionItem:
		return it.s
	case hitItem:
		return it.s
	}
	return nil
}

// refresh shows the result of a scan without disturbing what the user is
// doing: the cursor stays on the same project and session, and a filter or a
// pending delete prompt stays up.
func (m *model) refresh(projects []*Project) tea.Cmd {
	if sameShape(m.projects, projects) {
		// Nothing was added, removed or reordered: update the rows in place.
		// The lists keep their items, so nothing flickers.
		for i, p := range projects {
			for j, s := range p.Sessions {
				*m.projects[i].Sessions[j] = *s
			}
		}
		return nil
	}

	var selProj, selSess, selHit, curDir, confirmID, namingID string
	if it, ok := m.projList.SelectedItem().(projectItem); ok {
		selProj = it.p.Dir
	}
	if it, ok := m.sessList.SelectedItem().(sessionItem); ok {
		selSess = it.s.ID
	}
	if it, ok := m.hitList.SelectedItem().(hitItem); ok {
		selHit = it.s.ID
	}
	if m.cur != nil {
		curDir = m.cur.Dir
	}
	if m.confirm != nil {
		confirmID = m.confirm.ID
	}
	if m.naming != nil {
		namingID = m.naming.ID
	}

	// search results keep their hits and point at the new sessions
	var hits []Hit
	for _, it := range m.hitList.Items() {
		hits = append(hits, it.(hitItem).hit)
	}

	cmd := m.setProjects(projects)
	m.cur, m.confirm = nil, nil
	naming := m.naming != nil
	m.naming = nil
	for i, p := range projects {
		if p.Dir == selProj && !m.projList.IsFiltered() {
			m.projList.Select(i)
		}
		if p.Dir == curDir {
			m.cur = p
		}
		for _, s := range p.Sessions {
			if s.ID == confirmID {
				m.confirm = s
			}
			if s.ID == namingID {
				m.naming = s
			}
		}
	}
	if naming && m.naming == nil {
		m.footer = "not renamed: the session is gone"
	}
	clampCursor(&m.projList)

	cmd = tea.Batch(cmd, m.setHits(hits))
	for i, it := range m.hitList.Items() {
		if it.(hitItem).s.ID == selHit && !m.hitList.IsFiltered() {
			m.hitList.Select(i)
		}
	}
	clampCursor(&m.hitList)
	if m.screen == resultsScreen && len(m.hitList.Items()) == 0 {
		m.back()
	}

	if m.cur == nil { // the open project lost its last session
		if m.screen == sessionsScreen {
			m.screen = projectsScreen
		}
		return cmd
	}
	cmd = tea.Batch(cmd, m.setSessions())
	for i, s := range m.cur.Sessions {
		if s.ID == selSess && !m.sessList.IsFiltered() {
			m.sessList.Select(i)
		}
	}
	clampCursor(&m.sessList)
	return cmd
}

// sameShape reports whether two scans list the same sessions in the same order.
func sameShape(a, b []*Project) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Dir != b[i].Dir || len(a[i].Sessions) != len(b[i].Sessions) {
			return false
		}
		for j := range a[i].Sessions {
			if a[i].Sessions[j].ID != b[i].Sessions[j].ID {
				return false
			}
		}
	}
	return true
}

func clampCursor(l *list.Model) {
	if n := len(l.VisibleItems()); n > 0 && l.Index() >= n {
		l.Select(n - 1)
	}
}

// livePID checks right now whether a Claude Code process has the session
// open, not as of the last scan.
func livePID(s *Session) int {
	if l := liveSessions()[s.ID]; l.PID != 0 {
		return l.PID
	}
	return s.LivePID
}

func (m model) Init() tea.Cmd { return tick() }

// Update is update followed by a look at whether the preview is still right.
func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	m, cmd := m.update(msg)
	m.layout()
	m.syncPreview()
	return m, cmd
}

func (m model) update(msg tea.Msg) (model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil

	case execDoneMsg:
		if msg.err != nil {
			m.footer = fmt.Sprintf("%s: %v", msg.what, msg.err)
		}
		return m, nil

	case tickMsg:
		return m, m.scan(false)

	case rescannedMsg:
		var next tea.Cmd
		if !msg.manual {
			next = tick() // the next check starts only once this one is done
		}
		switch {
		case msg.err != nil:
			if msg.manual {
				m.footer = "rescan: " + msg.err.Error()
			}
		case msg.gen != m.gen:
			// a session was deleted while this scan ran; it may still list it
		default:
			next = tea.Batch(next, m.refresh(msg.projects))
			if msg.manual {
				m.footer = "rescanned"
			}
		}
		return m, next

	case searchDoneMsg:
		m.searching = false
		if len(msg.hits) == 0 {
			m.footer = fmt.Sprintf("no session mentions “%s”", msg.query)
			return m, nil
		}
		if m.screen != resultsScreen {
			m.from = m.screen
		}
		m.query, m.screen = msg.query, resultsScreen
		m.hitList.Title = "Search: " + msg.query
		m.hitList.ResetFilter()
		cmd := m.setHits(msg.hits)
		m.hitList.Select(0)
		m.footer = fmt.Sprintf("searched %d sessions in %d ms", msg.total, msg.took.Milliseconds())
		return m, cmd

	case tea.PasteMsg:
		if m.asking || m.naming != nil {
			m.input += strings.Join(strings.Fields(msg.Content), " ")
			return m, nil
		}

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
		if m.asking {
			return m.typeSearch(msg)
		}
		if m.naming != nil {
			return m.typeName(msg)
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
			return m, m.scan(true)
		case "s":
			if !m.searching {
				m.asking, m.input = true, ""
			}
			return m, nil
		case "p":
			m.preview = !m.preview
			if m.preview && m.paneAt() == placeNone {
				m.footer = m.noRoom()
			}
			return m, nil
		case "P": // the other side, and it stays there
			was := m.fit(m.place)
			m.preview, m.place = true, placeRight
			if was == placeRight {
				m.place = placeBottom
			}
			if m.paneAt() == placeNone {
				m.footer = m.noRoom()
			}
			return m, nil
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
			m.back()
			return m, nil
		case "esc":
			if l.IsFiltered() {
				break // let the list clear its filter
			}
			m.back()
			return m, nil
		case "D", "d", "delete":
			if s := m.selectedSession(); s != nil {
				if pid := livePID(s); pid != 0 {
					m.footer = fmt.Sprintf("%s is open in a running Claude Code (pid %d); exit it first", s.ID[:8], pid)
				} else {
					m.confirm = s
				}
			}
			return m, nil
		case "c":
			if s := m.selectedSession(); s != nil {
				return m, m.resume(s)
			}
			return m, nil
		case "o":
			if s := m.selectedSession(); s != nil {
				m.openInCode(s)
			}
			return m, nil
		case "t":
			if s := m.selectedSession(); s != nil && m.canRename(s) {
				m.rename(s, s.ToggledOpen())
			}
			return m, nil
		case "n":
			if s := m.selectedSession(); s != nil && m.canRename(s) {
				m.naming, m.input = s, s.Title
			}
			return m, nil
		}
	}

	var cmd tea.Cmd
	l := m.active()
	*l, cmd = l.Update(msg)
	return m, cmd
}

// typeSearch handles a key while the search text is being typed.
func (m model) typeSearch(msg tea.KeyPressMsg) (model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.asking = false
	case "enter":
		m.asking = false
		if q := strings.TrimSpace(m.input); q != "" {
			m.searching = true
			return m, m.search(q)
		}
	default:
		m.input = editLine(m.input, msg)
	}
	return m, nil
}

// typeName handles a key while a new title is being typed.
func (m model) typeName(msg tea.KeyPressMsg) (model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.naming = nil
		m.footer = "not renamed"
	case "enter":
		s := m.naming
		m.naming = nil
		switch title := strings.Join(strings.Fields(m.input), " "); {
		case title == "":
			m.footer = "not renamed: the title is empty"
		case title == s.Title:
			m.footer = "not renamed: the title is the same"
		default:
			m.rename(s, title)
		}
	default:
		m.input = editLine(m.input, msg)
	}
	return m, nil
}

// editLine applies a key to the text typed on the bottom line.
func editLine(input string, msg tea.KeyPressMsg) string {
	switch msg.String() {
	case "backspace":
		if r := []rune(input); len(r) > 0 {
			return string(r[:len(r)-1])
		}
		return input
	case "ctrl+u":
		return ""
	case "ctrl+w": // the last word
		t := strings.TrimRight(input, " ")
		return t[:strings.LastIndex(t, " ")+1]
	}
	return input + msg.Text
}

// ---------------------------------------------------------------- layout

// A window this wide gets the preview beside the list unless told otherwise:
// the list keeps 90 columns, enough for a session row with its title, and the
// pane still has 59.
const autoSideWidth = 150

// fit is where the preview goes in this window when want is asked for:
// placeNone when the window is too small for it.
func (m *model) fit(want placement) placement {
	right := m.width >= 80 && m.height >= 8
	switch {
	case want == placeRight && right, want == placeAuto && right && m.width >= autoSideWidth:
		return placeRight
	case want != placeRight && m.height >= 20:
		return placeBottom
	}
	return placeNone
}

// paneAt is where the preview is right now.
func (m *model) paneAt() placement {
	if !m.preview {
		return placeNone
	}
	return m.fit(m.place)
}

// noRoom says why the preview is wanted but not shown.
func (m *model) noRoom() string {
	if m.place == placeRight {
		return "the window is too narrow for the preview on the right"
	}
	return "the window is too short for the preview"
}

// listWidth is what the list keeps with the preview beside it: three fifths
// of the window up to 100 columns, and whatever a 120 column pane leaves over.
func (m *model) listWidth() int {
	return max(min(100, m.width*3/5), m.width-121)
}

// bottomHeight is how many lines the preview under the list gets, rule
// included: two fifths of the window.
func (m *model) bottomHeight() int {
	return max(m.height*2/5, 7)
}

// paneSize is the room for preview text: columns and lines.
func (m *model) paneSize() (w, h int) {
	switch m.paneAt() {
	case placeRight: // a column for the divider, one of padding on each side
		return m.width - m.listWidth() - 3, m.height - 2
	case placeBottom:
		return max(m.width-4, 10), m.bottomHeight() - 1
	}
	return 0, 0
}

func (m *model) layout() {
	w, h := m.width, m.height-1 // one line for the footer
	switch m.paneAt() {
	case placeRight:
		w = m.listWidth()
	case placeBottom:
		h -= m.bottomHeight()
	}
	for _, l := range []*list.Model{&m.projList, &m.sessList, &m.hitList} {
		if l.Width() != w || l.Height() != h {
			l.SetSize(w, h)
		}
	}
}

// syncPreview rebuilds the preview when the row under the cursor, its
// transcript or the window changed.
func (m *model) syncPreview() {
	w, h := m.paneSize()
	if h <= 0 {
		m.pvKey, m.pvLines = "", nil
		return
	}
	item := m.active().SelectedItem()
	key := fmt.Sprint(m.screen, m.paneAt(), w, h)
	switch it := item.(type) {
	case projectItem:
		key += fmt.Sprint(it.p.Dir, len(it.p.Sessions), it.p.Modified().UnixNano())
		for _, s := range it.p.Sessions[:min(h, len(it.p.Sessions))] {
			key += fmt.Sprint(s.ID, s.Title, s.LivePID, s.LiveStatus)
		}
	case sessionItem:
		key += fmt.Sprint(it.s.ID, it.s.Size)
	case hitItem:
		key += fmt.Sprint(it.s.ID, it.query)
	}
	if key == m.pvKey {
		return
	}
	m.pvKey = key

	var lines []string
	switch it := item.(type) {
	case projectItem: // its latest sessions
		for _, s := range it.p.Sessions[:min(h, len(it.p.Sessions))] {
			lines = append(lines, fmt.Sprintf("%s %s  %s", liveDot(s.LivePID != 0, s.LiveStatus),
				dimStyle.Render(fmtTime(s.Modified)), sessionTitle(s, false)))
		}
	case sessionItem: // how the conversation ended
		lines = previewTail(tail(it.s.Path, max(12, h)), w, h)
	case hitItem: // the message that mentions the search text
		lines = previewMessage(message{it.hit.Who, it.hit.Context}, it.query, w, h)
	}
	for i, l := range lines {
		lines[i] = ansi.Truncate(l, w, "…")
	}
	m.pvLines = lines
}

func speaker(who string) string {
	if who == "assistant" {
		return claudeStyle.Render("Claude")
	}
	return youStyle.Render("You")
}

// wrapText turns a message into display lines: no control characters, no
// blank lines, wrapped to w.
func wrapText(text string, w int) []string {
	var out []string
	for _, l := range strings.Split(ansi.Strip(text), "\n") {
		l = strings.TrimRight(strings.Map(func(r rune) rune {
			if r == '\t' {
				return ' '
			}
			if r < ' ' || r == 0x7f {
				return -1
			}
			return r
		}, l), " ")
		if strings.TrimSpace(l) == "" {
			continue
		}
		out = append(out, strings.Split(ansi.Wrap(l, w, ""), "\n")...)
	}
	return out
}

// previewTail fits the end of a conversation into h lines: the latest
// messages, each cut to a share of the pane so more than one is visible.
func previewTail(msgs []message, w, h int) []string {
	perMessage := max(5, h/3)
	var blocks [][]string
	used := 0
	for i := len(msgs) - 1; i >= 0 && used < h; i-- {
		if strings.HasPrefix(strings.TrimSpace(msgs[i].Text), "<") {
			continue // command output and the like, not something anyone wrote
		}
		body := wrapText(msgs[i].Text, w-2)
		if len(body) > perMessage {
			body = append(body[:perMessage-1], "…")
		}
		block := []string{speaker(msgs[i].Who)}
		for _, l := range body {
			block = append(block, "  "+l)
		}
		if room := h - used; len(block) > room { // the oldest one shown gets what is left
			if room < 3 {
				break
			}
			block = append(block[:room-1], "  …")
		}
		used += len(block)
		blocks = append(blocks, block)
	}
	if len(blocks) == 0 {
		return []string{dimStyle.Render("(no conversation text)")}
	}
	var lines []string
	for i := len(blocks) - 1; i >= 0; i-- {
		lines = append(lines, blocks[i]...)
	}
	return lines
}

// previewMessage shows the text around a search match, the match marked.
func previewMessage(msg message, query string, w, h int) []string {
	lines := []string{speaker(msg.Who)}
	body := wrapText(msg.Text, w-2)
	// start a little above the first line that has the match
	first := 0
	for i, l := range body {
		if strings.Contains(strings.ToLower(l), strings.ToLower(query)) {
			first = max(i-1, 0)
			break
		}
	}
	first = max(min(first, len(body)-(h-1)), 0)
	for _, l := range body[first:min(first+h-1, len(body))] {
		lines = append(lines, "  "+highlight(l, query, plainStyle))
	}
	return lines
}

// pvRule is the line that heads the preview: what it is, or what the running
// session under the cursor is doing, then a rule out to w columns.
func (m model) pvRule(w int) string {
	label := "── preview · p hides · P moves "
	if s := m.selectedSession(); s != nil && s.LivePID != 0 {
		state := s.LiveStatus
		if s.LiveStatus == "waiting" && s.LiveWaiting != "" {
			state = "waiting for " + s.LiveWaiting
		}
		if state == "" {
			state = "open"
		}
		label = fmt.Sprintf("── running: %s (pid %d) · p hides · P moves ", state, s.LivePID)
	}
	rule := label + strings.Repeat("─", max(w-ansi.StringWidth(label), 0))
	return dimStyle.Render(ansi.Truncate(rule, max(w, 0), ""))
}

func (m model) View() tea.View {
	body := m.active().View()
	switch m.paneAt() {
	case placeBottom:
		body += "\n  " + m.pvRule(m.width-2)
		for i := 0; i < m.bottomHeight()-1; i++ {
			body += "\n"
			if i < len(m.pvLines) {
				body += "  " + m.pvLines[i]
			}
		}
	case placeRight: // the list, a divider, the preview
		left := strings.Split(body, "\n")
		lw := m.listWidth()
		rows := make([]string, m.height-1)
		for i := range rows {
			l := ""
			if i < len(left) {
				l = ansi.Truncate(left[i], lw, "")
			}
			rows[i] = l + strings.Repeat(" ", max(lw-ansi.StringWidth(l), 0))
			switch {
			case i == 0:
				rows[i] += dimStyle.Render("├") + m.pvRule(m.width-lw-1)
			case i-1 < len(m.pvLines):
				rows[i] += dimStyle.Render("│") + " " + m.pvLines[i-1]
			default:
				rows[i] += dimStyle.Render("│")
			}
		}
		body = strings.Join(rows, "\n")
	}

	foot := noteStyle.Render(m.footer)
	switch {
	case m.confirm != nil:
		foot = warnStyle.Render(fmt.Sprintf("Delete %s “%s”? This cannot be undone. [y/N] ",
			m.confirm.ID[:8], ansi.Truncate(m.confirm.Label(), 50, "…")))
	case m.asking:
		foot = boldStyle.Render("Search all sessions: ") + m.input + cursorStyle.Render("▌") +
			dimStyle.Render("  enter searches · esc cancels")
	case m.naming != nil:
		foot = boldStyle.Render("Rename "+m.naming.ID[:8]+": ") + m.input + cursorStyle.Render("▌") +
			dimStyle.Render("  enter renames · esc cancels · ctrl+u clears")
	case m.searching:
		foot = noteStyle.Render("searching…")
	}
	v := tea.NewView(body + "\n " + foot)
	v.AltScreen = true
	return v
}

// ---------------------------------------------------------------- actions

// view opens the conversation in glow, or less. From the search results it
// opens at the first mention of the search text.
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
	if m.screen == resultsScreen && m.query != "" {
		// less takes the jump from $LESS, also when glow is the one starting
		// it. -R so the search sees the text and not glow's colour codes; a $
		// would end the option, so it becomes "any character".
		pattern := strings.ReplaceAll(regexp.QuoteMeta(m.query), "$", ".")
		c.Env = append(os.Environ(), "PAGER=less -R", "LESS=-i +/"+pattern)
	}
	return tea.ExecProcess(c, func(err error) tea.Msg {
		os.Remove(f.Name())
		return execDoneMsg{"viewer", err}
	})
}

// resume hands the terminal to `claude --resume` in the session's own
// directory; ccs comes back when Claude Code exits.
func (m *model) resume(s *Session) tea.Cmd {
	if pid := livePID(s); pid != 0 {
		m.footer = fmt.Sprintf("%s is already open in a running Claude Code (pid %d)", s.ID[:8], pid)
		return nil
	}
	if _, err := os.Stat(s.Cwd); s.Cwd == "" || err != nil {
		m.footer = "cannot resume: its directory " + tildify(s.Cwd) + " is gone"
		return nil
	}
	bin, err := exec.LookPath("claude")
	if err != nil {
		m.footer = "cannot resume: claude is not on PATH"
		return nil
	}
	c := exec.Command(bin, "--resume", s.ID)
	c.Dir = s.Cwd
	return tea.ExecProcess(c, func(err error) tea.Msg { return execDoneMsg{"claude", err} })
}

// openInCode writes the markdown to the temp dir and opens it in VS Code.
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

// canRename refuses a session open in a running Claude Code: that process
// writes its own idea of the title back into the transcript on every turn.
func (m *model) canRename(s *Session) bool {
	if pid := livePID(s); pid != 0 {
		m.footer = fmt.Sprintf("%s is open in a running Claude Code (pid %d); use /rename there", s.ID[:8], pid)
		return false
	}
	return true
}

// rename writes the new title and shows it at once, without waiting for the
// next scan.
func (m *model) rename(s *Session, title string) {
	if !m.canRename(s) { // checked again: it may have been resumed meanwhile
		return
	}
	wasOpen := IsOpen(s.Title)
	if err := s.Rename(title); err != nil {
		m.footer = "rename: " + err.Error()
		return
	}
	if fresh, err := scanSession(s.Path, s); err == nil {
		*s = *fresh
	}
	switch {
	case IsOpen(s.Title) && !wasOpen:
		m.footer = fmt.Sprintf("%s marked open: “%s”", s.ID[:8], s.Title)
	case wasOpen && !IsOpen(s.Title):
		m.footer = fmt.Sprintf("%s no longer open: “%s”", s.ID[:8], s.Title)
	default:
		m.footer = fmt.Sprintf("%s renamed “%s”", s.ID[:8], s.Title)
	}
}

func (m *model) deleteSession(s *Session) tea.Cmd {
	// Checked again here: Claude Code may have opened it while the prompt was up.
	if pid := livePID(s); pid != 0 {
		m.footer = fmt.Sprintf("%s is open in a running Claude Code (pid %d); not deleted", s.ID[:8], pid)
		return nil
	}
	m.gen++
	gone, err := s.Delete()
	if err != nil {
		m.footer = fmt.Sprintf("delete failed after %d paths: %v", len(gone), err)
		return nil
	}
	m.footer = fmt.Sprintf("deleted %s (%d paths)", s.ID[:8], len(gone))

	// Take it out of its project, and the project out of the list when that
	// was its last session. The folder stays: memory/ lives there.
	var kept []*Project
	for _, p := range m.projects {
		for i, x := range p.Sessions {
			if x == s {
				p.Sessions = append(p.Sessions[:i], p.Sessions[i+1:]...)
				break
			}
		}
		if len(p.Sessions) > 0 {
			kept = append(kept, p)
		} else if p == m.cur {
			m.cur = nil
		}
	}
	var cmds []tea.Cmd
	if len(kept) != len(m.projects) {
		cmds = append(cmds, m.setProjects(kept))
		clampCursor(&m.projList)
	}

	// the search results lose it too
	var hits []list.Item
	for _, it := range m.hitList.Items() {
		if it.(hitItem).s != s {
			hits = append(hits, it)
		}
	}
	if len(hits) != len(m.hitList.Items()) {
		idx := m.hitList.Index()
		cmds = append(cmds, m.hitList.SetItems(hits))
		m.hitList.Select(max(min(idx, len(m.hitList.VisibleItems())-1), 0))
	}
	if m.cur != nil {
		idx := m.sessList.Index()
		cmds = append(cmds, m.setSessions())
		m.sessList.Select(max(min(idx, len(m.sessList.VisibleItems())-1), 0))
	}

	if m.screen == resultsScreen && len(hits) == 0 {
		m.back()
	}
	if m.screen == sessionsScreen && m.cur == nil {
		m.screen = projectsScreen
	}
	return tea.Batch(cmds...)
}
