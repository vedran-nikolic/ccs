package main

import (
	"os"
	"path/filepath"
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
	projects, err := scanAll()
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
	s, _ := scanSession(filepath.Join(claudeDir(), "projects/-work-app", idRenamed+".jsonl"))
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
		case list.FilterMatchesMsg:
			return []tea.Msg{out}
		}
	case <-time.After(100 * time.Millisecond):
	}
	return nil
}

func screenText(m model) string { return ansi.Strip(m.View().Content) }

func TestUI(t *testing.T) {
	root := fixture(t)
	projects, _ := scanAll()
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
