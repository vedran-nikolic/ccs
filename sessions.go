// Copyright 2026 Proxi projekt d.o.o.
// SPDX-License-Identifier: GPL-3.0-only

package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Claude Code keeps one transcript per session at
// ~/.claude/projects/<project>/<uuid>.jsonl, where <project> is the session's
// cwd with "/" and "." replaced by "-". Anything else keyed by the session id
// (tool results, subagent transcripts, file history) sits next to it.

type TitleKind int

const (
	NoTitle TitleKind = iota
	AITitle
	Renamed
)

type Session struct {
	ID          string
	Path        string
	Modified    time.Time
	Size        int64
	Title       string
	Kind        TitleKind
	FirstPrompt string
	Cwd         string
	LivePID     int    // non-zero while a Claude Code process has it open
	LiveStatus  string // what that process is doing: "busy", "idle" or "waiting"
	LiveWaiting string // when waiting: for what, e.g. a permission prompt

	// Scan state, so a transcript that grew is read from where the last scan
	// stopped instead of from the start.
	scanned    int64 // bytes of complete lines already read
	custom, ai string
}

type Project struct {
	Dir      string
	Sessions []*Session
}

func claudeDir() string {
	if d := os.Getenv("CLAUDE_CONFIG_DIR"); d != "" {
		return d
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".claude")
}

// Name is the project's working directory, with $HOME shortened to ~.
func (p *Project) Name() string {
	cwd := ""
	for _, s := range p.Sessions {
		if s.Cwd != "" {
			cwd = s.Cwd
			break
		}
	}
	if cwd == "" { // the folder name is a lossy encoding, but better than nothing
		cwd = strings.ReplaceAll(filepath.Base(p.Dir), "-", "/")
	}
	return tildify(cwd)
}

func (p *Project) Modified() time.Time {
	var t time.Time
	for _, s := range p.Sessions {
		if s.Modified.After(t) {
			t = s.Modified
		}
	}
	return t
}

// Live reports whether any session is open in a running Claude Code, and the
// status of the one that most needs attention: waiting, then idle, then busy.
func (p *Project) Live() (live bool, status string) {
	rank := func(st string) int {
		switch st {
		case "waiting":
			return 3
		case "busy":
			return 1
		}
		return 2
	}
	for _, s := range p.Sessions {
		if s.LivePID != 0 && (!live || rank(s.LiveStatus) > rank(status)) {
			live, status = true, s.LiveStatus
		}
	}
	return live, status
}

// Label is what a session is called in lists and prompts.
func (s *Session) Label() string {
	switch {
	case s.Title != "":
		return s.Title
	case s.FirstPrompt != "":
		return s.FirstPrompt
	default:
		return "(empty)"
	}
}

type record struct {
	Type        string `json:"type"`
	CustomTitle string `json:"customTitle"`
	AITitle     string `json:"aiTitle"`
	Cwd         string `json:"cwd"`
	IsMeta      bool   `json:"isMeta"`
	Message     struct {
		Content json.RawMessage `json:"content"`
	} `json:"message"`
}

// text returns the conversation text of a message: the string itself, or the
// "text" parts of a content array. Tool calls and results are dropped.
func (r *record) text() string {
	c := r.Message.Content
	if len(c) == 0 {
		return ""
	}
	if c[0] == '"' {
		var s string
		_ = json.Unmarshal(c, &s)
		return s
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(c, &parts) != nil {
		return ""
	}
	var texts []string
	for _, p := range parts {
		if p.Type == "text" && p.Text != "" {
			texts = append(texts, p.Text)
		}
	}
	return strings.Join(texts, "\n")
}

// eachLine calls fn for every line of a JSONL file. Lines can be megabytes
// long (request blobs), so this avoids bufio.Scanner's token limit.
func eachLine(path string, fn func([]byte) bool) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	r := bufio.NewReaderSize(f, 1<<20)
	for {
		line, err := r.ReadBytes('\n')
		if len(line) > 0 && !fn(line) {
			return nil
		}
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

var (
	markCustom = []byte(`"custom-title"`)
	markAI     = []byte(`"ai-title"`)
	markUser   = []byte(`"user"`)
)

// scanSession reads a transcript's title, first prompt and cwd. Given the
// result of an earlier scan it only reads what was appended since (transcripts
// are append-only), which is what keeps the periodic refresh cheap.
func scanSession(path string, prev *Session) (*Session, error) {
	st, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	s := &Session{ID: strings.TrimSuffix(filepath.Base(path), ".jsonl"), Path: path}
	if prev != nil && st.Size() >= prev.scanned {
		*s = *prev // a smaller file was rewritten: start over
	}
	unchanged := prev != nil && st.Size() == prev.Size && st.ModTime().Equal(prev.Modified)
	s.Modified, s.Size = st.ModTime(), st.Size()
	s.LivePID, s.LiveStatus, s.LiveWaiting = 0, "", ""
	if unchanged {
		return s, nil
	}

	f, err := os.Open(path)
	if err != nil {
		return s, err
	}
	defer f.Close()
	if _, err := f.Seek(s.scanned, io.SeekStart); err != nil {
		return s, err
	}
	// Lines can be megabytes long, hence no bufio.Scanner (see eachLine).
	r := bufio.NewReaderSize(f, 1<<20)
	for {
		line, rerr := r.ReadBytes('\n')
		if len(line) > 0 {
			s.consume(line)
			if line[len(line)-1] == '\n' { // a half-written last line is read again next time
				s.scanned += int64(len(line))
			}
		}
		if rerr != nil {
			if rerr != io.EOF {
				err = rerr
			}
			break
		}
	}
	switch {
	// A rename back to the AI title (taking [OPEN] off one, say) is no rename.
	case s.custom != "" && s.custom != s.ai:
		s.Title, s.Kind = s.custom, Renamed
	case s.ai != "":
		s.Title, s.Kind = s.ai, AITitle
	}
	return s, err
}

// consume takes what the session list needs from one transcript line.
func (s *Session) consume(line []byte) {
	// cheap byte checks first; only parse lines that can matter
	if !bytes.Contains(line, markCustom) && !bytes.Contains(line, markAI) &&
		(s.FirstPrompt != "" || !bytes.Contains(line, markUser)) {
		return
	}
	var r record
	if json.Unmarshal(line, &r) != nil {
		return
	}
	switch r.Type {
	case "custom-title":
		if r.CustomTitle != "" {
			s.custom = r.CustomTitle // the last rename wins
		}
	case "ai-title":
		if r.AITitle != "" {
			s.ai = r.AITitle
		}
	case "user":
		if s.Cwd == "" {
			s.Cwd = r.Cwd
		}
		if t := strings.TrimSpace(r.text()); s.FirstPrompt == "" && t != "" &&
			!r.IsMeta && !strings.HasPrefix(t, "<") {
			s.FirstPrompt = strings.Join(strings.Fields(t), " ")
		}
	}
}

// Live is a Claude Code process that has a session open.
type Live struct {
	PID        int
	Status     string
	WaitingFor string
}

// liveSessions maps session id to the Claude Code process still running it,
// from the registry Claude Code keeps in ~/.claude/sessions/<pid>.json.
func liveSessions() map[string]Live {
	live := map[string]Live{}
	files, _ := filepath.Glob(filepath.Join(claudeDir(), "sessions", "*.json"))
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		var d struct {
			PID        int    `json:"pid"`
			SessionID  string `json:"sessionId"`
			Status     string `json:"status"`
			WaitingFor string `json:"waitingFor"`
		}
		if json.Unmarshal(b, &d) != nil || d.PID <= 0 || d.SessionID == "" {
			continue
		}
		if err := syscall.Kill(d.PID, 0); err == nil || errors.Is(err, syscall.EPERM) {
			live[d.SessionID] = Live{d.PID, d.Status, d.WaitingFor}
		}
	}
	return live
}

// scanAll reads every project, newest activity first, skipping empty ones.
// known holds earlier results by transcript path; nil reads everything afresh.
func scanAll(known map[string]Session) ([]*Project, error) {
	root := filepath.Join(claudeDir(), "projects")
	dirs, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	live := liveSessions()

	var (
		wg       sync.WaitGroup
		mu       sync.Mutex
		sem      = make(chan struct{}, 8)
		projects []*Project
	)
	for _, d := range dirs {
		if !d.IsDir() {
			continue
		}
		p := &Project{Dir: filepath.Join(root, d.Name())}
		files, _ := filepath.Glob(filepath.Join(p.Dir, "*.jsonl"))
		if len(files) == 0 {
			continue
		}
		projects = append(projects, p)
		for _, f := range files {
			wg.Add(1)
			go func() {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()
				var prev *Session
				if k, ok := known[f]; ok {
					prev = &k
				}
				s, err := scanSession(f, prev)
				if s == nil || (err != nil && s.Size == 0) {
					return
				}
				l := live[s.ID]
				s.LivePID, s.LiveStatus, s.LiveWaiting = l.PID, l.Status, l.WaitingFor
				mu.Lock()
				p.Sessions = append(p.Sessions, s)
				mu.Unlock()
			}()
		}
	}
	wg.Wait()

	kept := projects[:0]
	for _, p := range projects {
		if len(p.Sessions) == 0 {
			continue
		}
		// ties broken by name, so the order is the same on every scan
		sort.Slice(p.Sessions, func(i, j int) bool {
			a, b := p.Sessions[i], p.Sessions[j]
			if !a.Modified.Equal(b.Modified) {
				return a.Modified.After(b.Modified)
			}
			return a.ID < b.ID
		})
		kept = append(kept, p)
	}
	sort.Slice(kept, func(i, j int) bool {
		a, b := kept[i].Modified(), kept[j].Modified()
		if !a.Equal(b) {
			return a.After(b)
		}
		return kept[i].Dir < kept[j].Dir
	})
	return kept, nil
}

// knownSessions indexes projects for the next scanAll. It holds copies, so a
// scan running in the background never reads what the UI is changing.
func knownSessions(projects []*Project) map[string]Session {
	known := map[string]Session{}
	for _, p := range projects {
		for _, s := range p.Sessions {
			known[s.Path] = *s
		}
	}
	return known
}

// Markdown renders the conversation: one section per message, text only.
func (s *Session) Markdown() (string, error) {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n`%s` · %s · %s\n\n---\n", s.Label(), s.ID, tildify(s.Cwd), fmtTime(s.Modified))
	err := eachLine(s.Path, func(line []byte) bool {
		var r record
		if json.Unmarshal(line, &r) != nil || r.IsMeta || (r.Type != "user" && r.Type != "assistant") {
			return true
		}
		t := r.text()
		if t == "" {
			return true
		}
		who := "🧑 You"
		if r.Type == "assistant" {
			who = "🤖 Claude"
		}
		fmt.Fprintf(&b, "\n## %s\n\n%s\n\n---\n", who, t)
		return true
	})
	return b.String(), err
}

// OpenTag marks a session left unfinished, at the end of its title.
const OpenTag = "[OPEN]"

// IsOpen reports whether the title ends with OpenTag, in any case.
func IsOpen(title string) bool {
	t := strings.TrimSpace(title)
	return len(t) >= len(OpenTag) && strings.EqualFold(t[len(t)-len(OpenTag):], OpenTag)
}

// ToggledOpen is the title with OpenTag added at the end or, when it is
// there, taken off. A session without a title starts from its first prompt.
func (s *Session) ToggledOpen() string {
	t := strings.TrimSpace(s.Title)
	if IsOpen(t) {
		if t = strings.TrimSpace(t[:len(t)-len(OpenTag)]); t == "" {
			t = s.ai
		}
		if t == "" {
			t = shorten(s.FirstPrompt, 60)
		}
		return t
	}
	if t == "" {
		t = shorten(s.FirstPrompt, 60)
	}
	return strings.TrimSpace(t + " " + OpenTag)
}

// shorten cuts text to at most n runes, at a space when there is one.
func shorten(text string, n int) string {
	r := []rune(text)
	if len(r) <= n {
		return text
	}
	cut := string(r[:n])
	if i := strings.LastIndex(cut, " "); i > n/2 {
		cut = cut[:i]
	}
	return cut + "…"
}

// Rename gives the session a new title the way /rename in Claude Code does.
// Claude Code takes the title from the last custom-title record in the
// transcript when the session is resumed, so that record is what counts;
// <uuid>/custom-title.json is kept in step with it. The transcript keeps its
// modification time: a new title is not new activity, and the session stays
// where it was in the list.
func (s *Session) Rename(title string) error {
	title = strings.Join(strings.Fields(title), " ")
	if title == "" {
		return errors.New("the title is empty")
	}
	st, err := os.Stat(s.Path)
	if err != nil {
		return err
	}
	rec, err := compactJSON(struct {
		Type        string `json:"type"`
		CustomTitle string `json:"customTitle"`
		SessionID   string `json:"sessionId"`
	}{"custom-title", title, s.ID})
	if err != nil {
		return err
	}
	f, err := os.OpenFile(s.Path, os.O_RDWR|os.O_APPEND, 0)
	if err != nil {
		return err
	}
	// a transcript cut off mid-line would swallow the record
	if last := make([]byte, 1); st.Size() > 0 {
		if _, err := f.ReadAt(last, st.Size()-1); err == nil && last[0] != '\n' {
			rec = append([]byte("\n"), rec...)
		}
	}
	_, err = f.Write(append(rec, '\n'))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	os.Chtimes(s.Path, time.Now(), st.ModTime())

	side, err := compactJSON(struct {
		CustomTitle string `json:"customTitle"`
	}{title})
	if err != nil {
		return err
	}
	dir := strings.TrimSuffix(s.Path, ".jsonl")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".custom-title-*")
	if err != nil {
		return err
	}
	_, err = tmp.Write(side)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp.Name(), filepath.Join(dir, "custom-title.json"))
	}
	if err != nil {
		os.Remove(tmp.Name())
	}
	return err
}

// compactJSON is json.Marshal without escaping <, > and &, as Claude Code
// writes them.
func compactJSON(v any) ([]byte, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(b.Bytes(), []byte("\n")), nil
}

// Delete removes the transcript and everything keyed by its id, returning the
// paths that existed. The project folder itself stays: memory/ lives there.
func (s *Session) Delete() ([]string, error) {
	c := claudeDir()
	targets := []string{
		s.Path,
		strings.TrimSuffix(s.Path, ".jsonl"), // tool-results/, subagents/, custom-title.json
		filepath.Join(c, "file-history", s.ID),
		filepath.Join(c, "session-env", s.ID),
	}
	var gone []string
	for _, t := range targets {
		if _, err := os.Lstat(t); err != nil {
			continue
		}
		if err := os.RemoveAll(t); err != nil {
			return gone, err
		}
		gone = append(gone, tildify(t))
	}
	return gone, nil
}

func tildify(p string) string {
	home, _ := os.UserHomeDir()
	if home != "" && (p == home || strings.HasPrefix(p, home+"/")) {
		return "~" + p[len(home):]
	}
	return p
}

func fmtTime(t time.Time) string { return t.Format(timeLayout) }

func fmtSize(n int64) string {
	switch {
	case n < 1<<10:
		return fmt.Sprintf("%dB", n)
	case n < 1<<20:
		return fmt.Sprintf("%.1fK", float64(n)/(1<<10))
	case n < 1<<30:
		return fmt.Sprintf("%.1fM", float64(n)/(1<<20))
	default:
		return fmt.Sprintf("%.1fG", float64(n)/(1<<30))
	}
}
