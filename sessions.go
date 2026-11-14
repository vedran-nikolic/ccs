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
	LivePID     int // non-zero while a Claude Code process has it open
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

func (p *Project) Live() bool {
	for _, s := range p.Sessions {
		if s.LivePID != 0 {
			return true
		}
	}
	return false
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
	markUser   = []byte(`"type":"user"`)
)

func scanSession(path string) (*Session, error) {
	st, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	s := &Session{
		ID:       strings.TrimSuffix(filepath.Base(path), ".jsonl"),
		Path:     path,
		Modified: st.ModTime(),
		Size:     st.Size(),
	}
	var custom, ai string
	err = eachLine(path, func(line []byte) bool {
		// cheap byte checks first; only parse lines that can matter
		if !bytes.Contains(line, markCustom) && !bytes.Contains(line, markAI) &&
			(s.FirstPrompt != "" || !bytes.Contains(line, markUser)) {
			return true
		}
		var r record
		if json.Unmarshal(line, &r) != nil {
			return true
		}
		switch r.Type {
		case "custom-title":
			if r.CustomTitle != "" {
				custom = r.CustomTitle // the last rename wins
			}
		case "ai-title":
			if r.AITitle != "" {
				ai = r.AITitle
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
		return true
	})
	switch {
	case custom != "":
		s.Title, s.Kind = custom, Renamed
	case ai != "":
		s.Title, s.Kind = ai, AITitle
	}
	return s, err
}

// liveSessions maps session id to pid for Claude Code processes still running,
// from the registry Claude Code keeps in ~/.claude/sessions/<pid>.json.
func liveSessions() map[string]int {
	live := map[string]int{}
	files, _ := filepath.Glob(filepath.Join(claudeDir(), "sessions", "*.json"))
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		var d struct {
			PID       int    `json:"pid"`
			SessionID string `json:"sessionId"`
		}
		if json.Unmarshal(b, &d) != nil || d.PID <= 0 || d.SessionID == "" {
			continue
		}
		if err := syscall.Kill(d.PID, 0); err == nil || errors.Is(err, syscall.EPERM) {
			live[d.SessionID] = d.PID
		}
	}
	return live
}

// scanAll reads every project, newest activity first, skipping empty ones.
func scanAll() ([]*Project, error) {
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
				s, err := scanSession(f)
				if s == nil || (err != nil && s.Size == 0) {
					return
				}
				s.LivePID = live[s.ID]
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
		sort.Slice(p.Sessions, func(i, j int) bool {
			return p.Sessions[i].Modified.After(p.Sessions[j].Modified)
		})
		kept = append(kept, p)
	}
	sort.Slice(kept, func(i, j int) bool { return kept[i].Modified().After(kept[j].Modified()) })
	return kept, nil
}

// Markdown renders the conversation the same way ccview does: one section per
// message, text only.
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

func fmtTime(t time.Time) string { return t.Format("02.01.2006 15:04") }

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
