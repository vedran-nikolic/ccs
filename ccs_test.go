// Copyright 2026 Proxi projekt d.o.o.
// SPDX-License-Identifier: GPL-3.0-only

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

const (
	idRenamed = "11111111-1111-1111-1111-111111111111"
	idAI      = "22222222-2222-2222-2222-222222222222"
	idNone    = "33333333-3333-3333-3333-333333333333"
	idOther   = "44444444-4444-4444-4444-444444444444"
)

// fixture builds a fake ~/.claude with two projects and points ccs at it.
func fixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", root)
	write := func(rel, body string) {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	user := func(cwd, text string) string {
		return `{"type":"user","cwd":"` + cwd + `","message":{"role":"user","content":"` + text + `"}}` + "\n"
	}
	asst := `{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Bash"},{"type":"text","text":"Done, see above."}]}}` + "\n"
	meta := `{"type":"user","isMeta":true,"message":{"content":"<local-command-caveat>x</local-command-caveat>"}}` + "\n"

	app := "projects/-work-app/"
	write(app+idRenamed+".jsonl", meta+user("/work/app", "fix the login bug")+asst+
		`{"type":"ai-title","aiTitle":"Login bug fix"}`+"\n"+
		`{"type":"custom-title","customTitle":"Old name"}`+"\n"+
		`{"type":"custom-title","customTitle":"Auth rework"}`+"\n")
	write(app+idRenamed+"/tool-results/a.txt", "x")
	write("file-history/"+idRenamed+"/v1", "x")
	write(app+idAI+".jsonl", user("/work/app", "add dark mode")+`{"type":"ai-title","aiTitle":"Dark mode toggle"}`+"\n")
	write(app+idNone+".jsonl", user("/work/app", `what does   this\nscript do`)+asst)
	write(app+"memory/MEMORY.md", "keep me")
	write("projects/-work-lib/"+idOther+".jsonl", user("/work/lib", "hello"))
	write("projects/-work-empty/memory/MEMORY.md", "no sessions here")
	return root
}

func TestScan(t *testing.T) {
	fixture(t)
	projects, err := scanAll(nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(projects) != 2 {
		t.Fatalf("want 2 projects (empty one skipped), got %d", len(projects))
	}
	var app *Project
	for _, p := range projects {
		if p.Name() == "/work/app" {
			app = p
		}
	}
	if app == nil || len(app.Sessions) != 3 {
		t.Fatalf("want /work/app with 3 sessions, got %+v", app)
	}
	byID := map[string]*Session{}
	for _, s := range app.Sessions {
		byID[s.ID] = s
	}
	if s := byID[idRenamed]; s.Title != "Auth rework" || s.Kind != Renamed || s.FirstPrompt != "fix the login bug" {
		t.Errorf("renamed: last rename should win over AI title, meta skipped: %+v", s)
	}
	if s := byID[idAI]; s.Title != "Dark mode toggle" || s.Kind != AITitle {
		t.Errorf("ai: %+v", s)
	}
	if s := byID[idNone]; s.Title != "" || s.Label() != "what does this script do" {
		t.Errorf("none: label should be the whitespace-collapsed first prompt: %q", s.Label())
	}
}

func TestMarkdown(t *testing.T) {
	fixture(t)
	s, _ := scanSession(filepath.Join(claudeDir(), "projects/-work-app", idRenamed+".jsonl"), nil)
	md, err := s.Markdown()
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"# Auth rework", "## 🧑 You\n\nfix the login bug", "## 🤖 Claude\n\nDone, see above."} {
		if !strings.Contains(md, want) {
			t.Errorf("markdown missing %q:\n%s", want, md)
		}
	}
	if strings.Contains(md, "local-command-caveat") || strings.Contains(md, "tool_use") {
		t.Errorf("markdown should drop meta records and tool calls:\n%s", md)
	}
}

// --- UI, driven through Update and rendered with View

func press(s string) tea.KeyPressMsg {
	switch s {
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "esc":
		return tea.KeyPressMsg{Code: tea.KeyEscape}
	case "left":
		return tea.KeyPressMsg{Code: tea.KeyLeft}
	case "down":
		return tea.KeyPressMsg{Code: tea.KeyDown}
	}
	r := []rune(s)[0]
	return tea.KeyPressMsg{Code: r, Text: s}
}

func send(t *testing.T, m model, msgs ...tea.Msg) model {
	t.Helper()
	for _, msg := range msgs {
		next, cmd := m.Update(msg)
		m = next.(model)
		// Filtering is async in bubbles: feed back only the filter results.
		// Timers and spinner ticks would block or loop forever.
		for _, out := range drain(cmd) {
			next, _ := m.Update(out)
			m = next.(model)
		}
	}
	return m
}

// drain runs cmd (and nested batches) with a short timeout per command and
// returns the list.FilterMatchesMsg results.
func drain(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	ch := make(chan tea.Msg, 1)
	go func() { ch <- cmd() }()
	select {
	case out := <-ch:
		switch out := out.(type) {
		case tea.BatchMsg:
			var all []tea.Msg
			for _, c := range out {
				all = append(all, drain(c)...)
			}
			return all
		case list.FilterMatchesMsg, searchDoneMsg:
			return []tea.Msg{out}
		}
	case <-time.After(100 * time.Millisecond):
	}
	return nil
}

func screenText(m model) string { return ansi.Strip(m.View().Content) }

func TestUI(t *testing.T) {
	root := fixture(t)
	projects, _ := scanAll(nil)
	m := newModel(projects, "")
	m = send(t, m, tea.WindowSizeMsg{Width: 120, Height: 20})

	if s := screenText(m); !strings.Contains(s, "/work/app") || !strings.Contains(s, "/work/lib") {
		t.Fatalf("projects screen:\n%s", s)
	}

	// open /work/app (find its row first; order is by mtime)
	for range 5 {
		if it, ok := m.projList.SelectedItem().(projectItem); ok && it.p.Name() == "/work/app" {
			break
		}
		m = send(t, m, press("down"))
	}
	m = send(t, m, press("enter"))
	s := screenText(m)
	t.Logf("sessions screen:\n%s", s)
	for _, want := range []string{idRenamed, "✎ Auth rework", "Dark mode toggle", "what does this script do"} {
		if !strings.Contains(s, want) {
			t.Errorf("sessions screen missing %q", want)
		}
	}

	// filter down to the renamed one, then delete it
	m = send(t, m, press("/"))
	for _, r := range "auth" {
		m = send(t, m, press(string(r)))
	}
	m = send(t, m, press("enter"))
	if sel := m.selectedSession(); sel == nil || sel.ID != idRenamed {
		t.Fatalf("filter should select the renamed session, got %+v", sel)
	}
	m = send(t, m, press("D"))
	if !strings.Contains(screenText(m), "Delete 11111111") {
		t.Fatalf("expected confirm prompt:\n%s", screenText(m))
	}
	m = send(t, m, press("n"))
	if _, err := os.Stat(filepath.Join(root, "projects/-work-app", idRenamed+".jsonl")); err != nil {
		t.Fatal("n must keep the session")
	}
	m = send(t, m, press("D"), press("y"))
	t.Logf("after delete:\n%s", screenText(m))
	for _, gone := range []string{
		"projects/-work-app/" + idRenamed + ".jsonl",
		"projects/-work-app/" + idRenamed,
		"file-history/" + idRenamed,
	} {
		if _, err := os.Stat(filepath.Join(root, gone)); !os.IsNotExist(err) {
			t.Errorf("%s should be deleted", gone)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "projects/-work-app/memory/MEMORY.md")); err != nil {
		t.Error("memory/ must survive a delete")
	}
	if len(m.cur.Sessions) != 2 || strings.Contains(screenText(m), "Auth rework") {
		t.Errorf("deleted session should be gone from the list:\n%s", screenText(m))
	}

	// a running session cannot be deleted
	m = send(t, m, press("esc")) // clear filter
	m.cur.Sessions[0].LivePID = os.Getpid()
	m.sessList.Select(0)
	m = send(t, m, press("D"))
	if m.confirm != nil || !strings.Contains(screenText(m), "running Claude Code") {
		t.Errorf("live session must be refused:\n%s", screenText(m))
	}

	// back to projects
	m = send(t, m, press("left"))
	if m.screen != projectsScreen {
		t.Error("left should go back to projects")
	}
}

func appendTo(t *testing.T, path, text string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(text); err != nil {
		t.Fatal(err)
	}
}

// A second scan reads only what was appended, and still sees a new title.
func TestIncrementalScan(t *testing.T) {
	root := fixture(t)
	path := filepath.Join(root, "projects/-work-app", idAI+".jsonl")
	first, err := scanSession(path, nil)
	if err != nil || first.Title != "Dark mode toggle" {
		t.Fatalf("first scan: %+v %v", first, err)
	}
	if again, _ := scanSession(path, first); again.scanned != first.scanned || again.Title != first.Title {
		t.Errorf("unchanged file should be returned as it was: %+v", again)
	}

	// a rename arrives, followed by a line that is still being written
	appendTo(t, path, `{"type":"custom-title","customTitle":"Theme work"}`+"\n"+`{"type":"custom-title","customTi`)
	second, _ := scanSession(path, first)
	if second.Title != "Theme work" || second.Kind != Renamed || second.FirstPrompt != "add dark mode" {
		t.Errorf("appended rename not picked up, or earlier fields lost: %+v", second)
	}
	if st, _ := os.Stat(path); second.scanned >= st.Size() || second.scanned <= first.scanned {
		t.Errorf("offset should stop before the half-written line: %d of %d", second.scanned, st.Size())
	}
	appendTo(t, path, `tle":"Final name"}`+"\n")
	if third, _ := scanSession(path, second); third.Title != "Final name" {
		t.Errorf("line completed later should be read whole: %+v", third)
	}

	// a file that shrank was rewritten: nothing from before may survive
	os.WriteFile(path, []byte(`{"type":"ai-title","aiTitle":"New"}`+"\n"), 0o644)
	if fresh, _ := scanSession(path, second); fresh.Title != "New" || fresh.FirstPrompt != "" {
		t.Errorf("rewritten file should be scanned afresh: %+v", fresh)
	}
}

// Sessions started elsewhere show up on refresh without moving the cursor.
func TestRefresh(t *testing.T) {
	root := fixture(t)
	projects, _ := scanAll(nil)
	startIn := filepath.Join(root, "projects/-work-app")
	m := newModel(projects, startIn)
	m = send(t, m, tea.WindowSizeMsg{Width: 120, Height: 20}, press("down"))
	sel := m.selectedSession().ID

	// in place: a rename in another terminal, same sessions in the same order
	old := time.Now().Add(-time.Hour)
	for _, s := range m.cur.Sessions {
		os.Chtimes(s.Path, old, old)
	}
	m = send(t, m, m.scan(false)())
	itemsBefore := m.sessList.Items()
	path := filepath.Join(startIn, sel+".jsonl")
	appendTo(t, path, `{"type":"custom-title","customTitle":"Renamed elsewhere"}`+"\n")
	os.Chtimes(path, old, old)
	m = send(t, m, m.scan(false)())
	if !strings.Contains(screenText(m), "Renamed elsewhere") {
		t.Errorf("rename should show after a refresh:\n%s", screenText(m))
	}
	if &itemsBefore[0] != &m.sessList.Items()[0] {
		t.Error("an unchanged set of sessions should be updated in place")
	}

	// a new session appears on top; the cursor stays on the same session
	const idNew = "55555555-5555-5555-5555-555555555555"
	os.WriteFile(filepath.Join(startIn, idNew+".jsonl"),
		[]byte(`{"type":"user","cwd":"/work/app","message":{"content":"started in another terminal"}}`+"\n"), 0o644)
	m = send(t, m, m.scan(false)())
	if !strings.Contains(screenText(m), "started in another terminal") {
		t.Errorf("new session should show after a refresh:\n%s", screenText(m))
	}
	if got := m.selectedSession(); got == nil || got.ID != sel {
		t.Errorf("cursor should stay on %s, got %+v", sel, got)
	}

	// a result from before a delete is dropped
	stale := m.scan(false)()
	m.sessList.Select(0)
	m = send(t, m, press("D"), press("y"), stale)
	if strings.Contains(screenText(m), "started in another terminal") {
		t.Errorf("a scan started before the delete must not bring the session back:\n%s", screenText(m))
	}

	// the open project disappears: back to the project list
	for _, s := range m.cur.Sessions {
		os.Remove(s.Path)
	}
	m = send(t, m, m.scan(false)())
	if m.screen != projectsScreen || m.cur != nil || strings.Contains(screenText(m), "/work/app") {
		t.Errorf("a project with no sessions left should close:\n%s", screenText(m))
	}
}

// Claude Code opening the session while the y/N prompt is up stops the delete.
func TestDeleteChecksLivenessAgain(t *testing.T) {
	root := fixture(t)
	projects, _ := scanAll(nil)
	m := newModel(projects, filepath.Join(root, "projects/-work-lib"))
	m = send(t, m, tea.WindowSizeMsg{Width: 120, Height: 20}, press("D"))
	if m.confirm == nil {
		t.Fatal("expected the delete prompt")
	}
	os.MkdirAll(filepath.Join(root, "sessions"), 0o755)
	os.WriteFile(filepath.Join(root, "sessions/1.json"),
		[]byte(`{"pid":`+strconv.Itoa(os.Getpid())+`,"sessionId":"`+idOther+`"}`), 0o644)
	m = send(t, m, press("y"))
	if _, err := os.Stat(filepath.Join(root, "projects/-work-lib", idOther+".jsonl")); err != nil {
		t.Error("a session opened during the prompt must not be deleted")
	}
	if !strings.Contains(screenText(m), "not deleted") {
		t.Errorf("expected a refusal:\n%s", screenText(m))
	}

	// and it cannot be resumed a second time either
	m = send(t, m, press("c"))
	if !strings.Contains(screenText(m), "already open") {
		t.Errorf("resume of a running session should be refused:\n%s", screenText(m))
	}
}

func TestLocale(t *testing.T) {
	for locale, want := range map[string]string{
		"hr_HR.UTF-8":     "HR",
		"en_US@rg=hrzzzz": "HR", // macOS: English with Croatian formats
		"en_US.UTF-8":     "US",
		"sr_Latn_RS":      "RS",
		"de-CH":           "CH",
		"C":               "",
		"C.UTF-8":         "",
		"POSIX":           "",
		"en":              "",
		"":                "",
	} {
		if got := regionOf(locale); got != want {
			t.Errorf("regionOf(%q) = %q, want %q", locale, got, want)
		}
	}
	at := time.Date(2026, 11, 14, 15, 4, 0, 0, time.UTC)
	for region, want := range map[string]string{
		"HR": "14.11.2026 15:04",
		"US": "11/14/2026 03:04 PM",
		"GB": "14/11/2026 15:04",
		"SE": "2026-11-14 15:04",
		"":   "2026-11-14 15:04",
		"XX": "2026-11-14 15:04",
	} {
		if got := at.Format(layoutFor(region)); got != want {
			t.Errorf("layoutFor(%q) gives %q, want %q", region, got, want)
		}
	}
}

func TestSearch(t *testing.T) {
	root := fixture(t)
	os.WriteFile(filepath.Join(root, "projects/-work-lib", idOther+".jsonl"),
		[]byte(`{"type":"user","cwd":"/work/lib","message":{"content":"Čekaj, što radi ovaj \"upit\"?"}}`+"\n"), 0o644)
	projects, _ := scanAll(nil)
	paths := map[string]string{}
	for _, p := range projects {
		for _, s := range p.Sessions {
			paths[s.ID] = s.Path
		}
	}
	ids := func(q string) string {
		var got []string
		for _, h := range searchAll(paths, q) {
			got = append(got, h.ID[:1])
		}
		sort.Strings(got)
		return strings.Join(got, "")
	}
	for q, want := range map[string]string{
		"DARK mode":     "2",  // case is ignored
		"done, see":     "13", // what Claude said counts too
		"Bash":          "",   // tool calls do not
		"local-command": "",   // nor do meta records
		"čekaj":         "4",  // beyond ASCII
		`"upit"`:        "4",  // text that is escaped in the file
		"nowhere":       "",
	} {
		if got := ids(q); got != want {
			t.Errorf("search %q found sessions %q, want %q", q, got, want)
		}
	}
	h := searchAll(paths, "login")
	if len(h) != 1 || h[0].Count != 1 || h[0].Who != "user" || h[0].Context != "fix the login bug" {
		t.Errorf("hit should carry who said it and the text around it: %+v", h)
	}
}

// s searches every session; the results can be previewed, deleted and left.
func TestSearchUI(t *testing.T) {
	root := fixture(t)
	projects, _ := scanAll(nil)
	m := newModel(projects, filepath.Join(root, "projects/-work-lib"))
	m = send(t, m, tea.WindowSizeMsg{Width: 120, Height: 30}, press("s"))
	for _, r := range "done" {
		m = send(t, m, press(string(r)))
	}
	if !strings.Contains(screenText(m), "Search all sessions: done") {
		t.Fatalf("expected the search prompt:\n%s", screenText(m))
	}
	m = send(t, m, press("enter"))
	s := screenText(m)
	t.Logf("results:\n%s", s)
	for _, want := range []string{"Search: done", "2 sessions", "Auth rework", "what does this script do", "/work/app", "preview", "Done, see above."} {
		if !strings.Contains(s, want) {
			t.Errorf("results screen missing %q", want)
		}
	}

	m = send(t, m, press("p"))
	if strings.Contains(screenText(m), "preview") {
		t.Errorf("p should hide the preview:\n%s", screenText(m))
	}
	m = send(t, m, press("p"))

	// a refresh that reorders the sessions keeps the results
	touched := filepath.Join(root, "projects/-work-app", idAI+".jsonl")
	later := time.Now().Add(time.Hour)
	os.Chtimes(touched, later, later)
	m = send(t, m, m.scan(false)())
	if m.screen != resultsScreen || len(m.hitList.Items()) != 2 {
		t.Errorf("results should survive a refresh:\n%s", screenText(m))
	}

	// delete one from the results; with the last one gone the screen closes
	first := m.selectedSession()
	m = send(t, m, press("D"), press("y"))
	if _, err := os.Stat(first.Path); !os.IsNotExist(err) || len(m.hitList.Items()) != 1 {
		t.Errorf("delete from the results should remove the session and its row:\n%s", screenText(m))
	}
	m = send(t, m, press("D"), press("y"))
	if m.screen != sessionsScreen || m.cur == nil || m.cur.Name() != "/work/lib" {
		t.Errorf("with no results left, back to where the search began:\n%s", screenText(m))
	}

	m = send(t, m, press("s"), press("x"), press("q"), press("z"), press("enter"))
	if !strings.Contains(screenText(m), "no session mentions") {
		t.Errorf("expected a note that nothing was found:\n%s", screenText(m))
	}
}

// The preview shows how a conversation ended; short windows go without.
func TestPreviewAndStatus(t *testing.T) {
	root := fixture(t)
	os.MkdirAll(filepath.Join(root, "sessions"), 0o755)
	os.WriteFile(filepath.Join(root, "sessions/1.json"),
		[]byte(`{"pid":`+strconv.Itoa(os.Getpid())+`,"sessionId":"`+idNone+`","status":"waiting","waitingFor":"permission"}`), 0o644)
	projects, _ := scanAll(nil)
	m := newModel(projects, filepath.Join(root, "projects/-work-app"))
	m = send(t, m, tea.WindowSizeMsg{Width: 120, Height: 30})
	for range 5 {
		if s := m.selectedSession(); s != nil && s.ID == idNone {
			break
		}
		m = send(t, m, press("down"))
	}
	s := screenText(m)
	for _, want := range []string{"running: waiting for permission", "You", "what does this", "Claude", "Done, see above."} {
		if !strings.Contains(s, want) {
			t.Errorf("preview missing %q:\n%s", want, s)
		}
	}
	if live, status := m.cur.Live(); !live || status != "waiting" {
		t.Errorf("project should report its waiting session: %v %q", live, status)
	}
	if lines := strings.Count(s, "\n") + 1; lines != 30 {
		t.Errorf("the screen should fill the window exactly, got %d lines", lines)
	}
	m = send(t, m, tea.WindowSizeMsg{Width: 120, Height: 15})
	if s := screenText(m); strings.Contains(s, "Done, see above.") || strings.Count(s, "\n")+1 != 15 {
		t.Errorf("a short window should drop the preview:\n%s", s)
	}
}

// A wide window puts the preview beside the list; P moves it and pins it.
func TestPreviewPlacement(t *testing.T) {
	root := fixture(t)
	projects, _ := scanAll(nil)
	m := newModel(projects, filepath.Join(root, "projects/-work-app"))

	check := func(what string, want placement, w, h int) {
		t.Helper()
		s := screenText(m)
		if got := m.paneAt(); got != want {
			t.Errorf("%s: preview at %d, want %d:\n%s", what, got, want, s)
		}
		lines := strings.Split(s, "\n")
		if len(lines) != h {
			t.Errorf("%s: %d lines, want %d:\n%s", what, len(lines), h, s)
		}
		for _, l := range lines {
			// only the side layout is composed here; a bare list is bubbles' business
			if want == placeRight && ansi.StringWidth(l) > w {
				t.Errorf("%s: line wider than the window: %q", what, l)
			}
		}
		if (want != placeNone) != strings.Contains(s, "preview · p hides") {
			t.Errorf("%s: preview heading shown or missing wrongly:\n%s", what, s)
		}
		if side := strings.Contains(s, "│"); side != (want == placeRight) {
			t.Errorf("%s: divider shown or missing wrongly:\n%s", what, s)
		}
	}

	m = send(t, m, tea.WindowSizeMsg{Width: 200, Height: 50})
	check("wide", placeRight, 200, 50)
	t.Logf("wide:\n%s", screenText(m))
	if m.sessList.Width() != 100 || m.sessList.Height() != 49 {
		t.Errorf("list beside the preview is %dx%d, want 100x49", m.sessList.Width(), m.sessList.Height())
	}
	m = send(t, m, tea.WindowSizeMsg{Width: 200, Height: 12})
	check("wide and short", placeRight, 200, 12)
	m = send(t, m, tea.WindowSizeMsg{Width: 120, Height: 40})
	check("narrow", placeBottom, 120, 40)

	m = send(t, m, press("P")) // pinned right, though auto would put it below
	check("P", placeRight, 120, 40)
	m = send(t, m, tea.WindowSizeMsg{Width: 70, Height: 40})
	check("pinned right, too narrow", placeNone, 70, 40)
	m = send(t, m, press("p"), press("p"))
	if !strings.Contains(screenText(m), "too narrow") {
		t.Errorf("expected a note that the preview does not fit:\n%s", screenText(m))
	}
	m = send(t, m, tea.WindowSizeMsg{Width: 200, Height: 50}, press("P")) // pinned below in a wide window
	check("P again", placeBottom, 200, 50)
	m = send(t, m, press("p"))
	check("hidden", placeNone, 200, 50)
	m = send(t, m, press("P")) // P shows it again, on the other side
	check("P while hidden", placeRight, 200, 50)

	for in, want := range map[string]placement{"auto": placeAuto, "right": placeRight, "bottom": placeBottom, "off": placeAuto} {
		if got, show, ok := parsePlacement(in); got != want || show != (in != "off") || !ok {
			t.Errorf("parsePlacement(%q) = %d, %v, %v", in, got, show, ok)
		}
	}
	if _, _, ok := parsePlacement("left"); ok {
		t.Error("parsePlacement should refuse what it does not know")
	}
}

// A tall pane shows more of each message and more messages.
func TestPreviewTailScales(t *testing.T) {
	var msgs []message
	for i := range 20 {
		msgs = append(msgs, message{"user", strings.Repeat(fmt.Sprintf("line %d\n", i), 30)})
	}
	short, tall := previewTail(msgs, 40, 11), previewTail(msgs, 40, 48)
	if len(short) != 11 || len(tall) != 48 {
		t.Errorf("preview should fill its pane without overflowing: %d of 11, %d of 48", len(short), len(tall))
	}
	if n := strings.Count(strings.Join(tall, "\n"), "line 19"); n != 15 {
		t.Errorf("a 48 line pane should show 15 lines of the last message, got %d", n)
	}
}

// Rename writes what /rename writes, and t adds and removes the open tag.
func TestRename(t *testing.T) {
	root := fixture(t)
	dir := filepath.Join(root, "projects/-work-app")
	path := filepath.Join(dir, idAI+".jsonl")
	old := time.Now().Add(-time.Hour).Truncate(time.Second)
	os.Chtimes(path, old, old)
	s, _ := scanSession(path, nil)

	if got := s.ToggledOpen(); got != "Dark mode toggle [OPEN]" {
		t.Fatalf("toggle on: %q", got)
	}
	if err := s.Rename(s.ToggledOpen()); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	if want := `{"type":"custom-title","customTitle":"Dark mode toggle [OPEN]","sessionId":"` + idAI + `"}` + "\n"; !strings.HasSuffix(string(b), want) {
		t.Errorf("transcript should end with the record Claude Code writes:\n%s", b)
	}
	if side, _ := os.ReadFile(filepath.Join(dir, idAI, "custom-title.json")); string(side) != `{"customTitle":"Dark mode toggle [OPEN]"}` {
		t.Errorf("custom-title.json: %s", side)
	}
	if st, _ := os.Stat(path); !st.ModTime().Equal(old) {
		t.Errorf("modification time should be kept: %v, was %v", st.ModTime(), old)
	}
	s, _ = scanSession(path, s)
	if s.Title != "Dark mode toggle [OPEN]" || s.Kind != Renamed || !IsOpen(s.Title) {
		t.Errorf("after tagging: %+v", s)
	}

	// off again: back to the AI title, which shows as no rename at all
	if got := s.ToggledOpen(); got != "Dark mode toggle" {
		t.Fatalf("toggle off: %q", got)
	}
	s.Rename(s.ToggledOpen())
	if s, _ = scanSession(path, s); s.Title != "Dark mode toggle" || s.Kind != AITitle {
		t.Errorf("a custom title equal to the AI title is not a rename: %+v", s)
	}

	// a session without a title starts from its first prompt; any case counts
	none, _ := scanSession(filepath.Join(dir, idNone+".jsonl"), nil)
	if got := none.ToggledOpen(); got != "what does this script do [OPEN]" {
		t.Errorf("untitled: %q", got)
	}
	none.Title = "Half done [open] "
	if got := none.ToggledOpen(); got != "Half done" {
		t.Errorf("lower-case tag should come off: %q", got)
	}
	none.Title = OpenTag
	if got := none.ToggledOpen(); got != "what does this script do" {
		t.Errorf("a title that was only the tag falls back to the first prompt: %q", got)
	}
	if got := shorten(strings.Repeat("word ", 30), 60); len([]rune(got)) > 61 || !strings.HasSuffix(got, "…") {
		t.Errorf("long first prompt should be cut: %q", got)
	}

	// a transcript cut off mid-line, and characters JSON likes to escape
	appendTo(t, path, `{"type":"user","cut off`)
	s.Rename("  <A & B>  ")
	if s, _ = scanSession(path, s); s.Title != "<A & B>" {
		t.Errorf("record after a half line should still be read: %+v", s)
	}
	if b, _ := os.ReadFile(path); !strings.Contains(string(b), `"customTitle":"<A & B>"`) {
		t.Errorf("<, > and & should be written as they are:\n%s", b)
	}
	if err := s.Rename("   "); err == nil {
		t.Error("an empty title must be refused")
	}
}

func TestRenameUI(t *testing.T) {
	root := fixture(t)
	projects, _ := scanAll(nil)
	m := newModel(projects, filepath.Join(root, "projects/-work-app"))
	m = send(t, m, tea.WindowSizeMsg{Width: 120, Height: 20})
	selectID(&m, idAI)

	m = send(t, m, press("t"))
	if s := screenText(m); !strings.Contains(s, "✎ Dark mode toggle [OPEN]") || !strings.Contains(s, "marked open") {
		t.Errorf("t should tag the session at once:\n%s", s)
	}
	// the / filter finds the open ones
	m = send(t, m, press("/"))
	for _, r := range "[open]" {
		m = send(t, m, press(string(r)))
	}
	m = send(t, m, press("enter"))
	if n := len(m.sessList.VisibleItems()); n != 1 {
		t.Errorf("/[open] should leave just the tagged session, got %d:\n%s", n, screenText(m))
	}
	m = send(t, m, press("esc"))
	selectID(&m, idAI) // clearing a filter puts the cursor on top

	m = send(t, m, press("t"))
	if s := screenText(m); strings.Contains(s, "toggle [OPEN]") || !strings.Contains(s, "no longer open") {
		t.Errorf("t again should take the tag off:\n%s", s)
	}

	// n starts from the current title; ctrl+u clears it
	m = send(t, m, press("n"))
	if s := screenText(m); !strings.Contains(s, "Rename 22222222: Dark mode toggle") {
		t.Fatalf("expected the rename prompt with the title in it:\n%s", s)
	}
	m = send(t, m, tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl})
	for _, r := range "Theme work" {
		m = send(t, m, press(string(r)))
	}
	m = send(t, m, press("enter"))
	if s := screenText(m); !strings.Contains(s, "✎ Theme work") || !strings.Contains(s, "renamed") {
		t.Errorf("n should rename:\n%s", s)
	}
	m = send(t, m, press("n"), press("x"), press("esc"))
	if s := screenText(m); !strings.Contains(s, "✎ Theme work") || !strings.Contains(s, "not renamed") {
		t.Errorf("esc should keep the title:\n%s", s)
	}

	// a running session is left to its own /rename
	m.selectedSession().LivePID = os.Getpid()
	m = send(t, m, press("t"))
	if s := screenText(m); strings.Contains(s, "work [OPEN]") || !strings.Contains(s, "use /rename there") {
		t.Errorf("a live session must not be renamed:\n%s", s)
	}
	m = send(t, m, press("n"))
	if m.naming != nil {
		t.Error("no rename prompt for a live session")
	}
}

func selectID(m *model, id string) {
	for i, it := range m.sessList.Items() {
		if it.(sessionItem).s.ID == id {
			m.sessList.Select(i)
		}
	}
}
