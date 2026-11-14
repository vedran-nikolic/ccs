// Copyright 2026 Proxi projekt d.o.o.
// SPDX-License-Identifier: GPL-3.0-only

package main

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"sync"
	"unicode/utf8"
)

// Hit is a session whose conversation mentions the search text.
type Hit struct {
	ID      string
	Count   int    // messages that mention it
	Who     string // who wrote the first of them: "user" or "assistant"
	Context string // the text around the first mention
}

type message struct {
	Who  string // "user" or "assistant"
	Text string
}

// messageOf returns the conversation text of a transcript line, the same
// selection Markdown shows: no tool calls, tool results or meta records.
func messageOf(line []byte) (message, bool) {
	var r record
	if json.Unmarshal(line, &r) != nil || r.IsMeta || (r.Type != "user" && r.Type != "assistant") {
		return message{}, false
	}
	t := r.text()
	return message{r.Type, t}, t != ""
}

// searchAll looks for query, ignoring case, in the text of every transcript.
// paths maps session id to transcript.
func searchAll(paths map[string]string, query string) []Hit {
	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		sem  = make(chan struct{}, 8)
		hits []Hit
	)
	q := strings.ToLower(query)
	for id, path := range paths {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			if h := searchSession(path, q); h.Count > 0 {
				h.ID = id
				mu.Lock()
				hits = append(hits, h)
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	return hits
}

func searchSession(path, q string) Hit {
	var h Hit
	// Most of a transcript is tool output. Text is stored as it was typed
	// unless it needs JSON escaping, so a line that does not contain the query
	// cannot hold a message that does, and is skipped without parsing.
	raw := []byte(q)
	prefilter := !strings.ContainsAny(q, "\"\\\n\t")
	_ = eachLine(path, func(line []byte) bool {
		if prefilter && !containsFold(line, raw) {
			return true
		}
		m, ok := messageOf(line)
		if !ok {
			return true
		}
		i := strings.Index(strings.ToLower(m.Text), q)
		if i < 0 {
			return true
		}
		if h.Count++; h.Count == 1 {
			h.Who, h.Context = m.Who, around(m.Text, i, len(q))
		}
		return true
	})
	return h
}

// containsFold is bytes.Contains ignoring case, for a lower-case needle. It
// only lowers a line when a cheap check says the needle might be there.
func containsFold(line, lower []byte) bool {
	if len(lower) == 0 {
		return true
	}
	if c := lower[0]; c < utf8.RuneSelf {
		up := c
		if 'a' <= c && c <= 'z' {
			up = c - 32
		}
		if bytes.IndexByte(line, c) < 0 && bytes.IndexByte(line, up) < 0 {
			return false
		}
	}
	return bytes.Contains(bytes.ToLower(line), lower)
}

// around cuts the text near a match: a little before it, more after.
func around(text string, at, n int) string {
	at = min(at, len(text)) // lower-casing can change the length of rare letters
	from, to := max(at-240, 0), min(at+n+600, len(text))
	for from > 0 && !utf8.RuneStart(text[from]) {
		from--
	}
	for to < len(text) && !utf8.RuneStart(text[to]) {
		to++
	}
	out := text[from:to]
	if from > 0 {
		out = "…" + out
	}
	if to < len(text) {
		out += "…"
	}
	return out
}

// tail returns the last messages of a transcript without reading all of it.
func tail(path string, want int) []message {
	for _, span := range []int64{256 << 10, 4 << 20} { // lines can be megabytes long
		f, err := os.Open(path)
		if err != nil {
			return nil
		}
		st, _ := f.Stat()
		from := max(st.Size()-span, 0)
		buf := make([]byte, st.Size()-from)
		n, _ := f.ReadAt(buf, from) // short if the file shrank meanwhile
		f.Close()
		buf = buf[:n]
		lines := bytes.Split(buf, []byte("\n"))
		if from > 0 {
			lines = lines[1:] // the first one starts mid-line
		}
		var msgs []message
		for i := len(lines) - 1; i >= 0 && len(msgs) < want; i-- {
			if m, ok := messageOf(lines[i]); ok {
				msgs = append([]message{m}, msgs...)
			}
		}
		if len(msgs) > 0 || from == 0 {
			return msgs
		}
	}
	return nil
}
