# ccs

Browse Claude Code sessions in the terminal, grouped by project. View one as
markdown, open it in VS Code, or delete it.

```sh
go build -o ccs .      # or: go install
ccs                    # inside a project dir: opens on its sessions
ccs -a                 # always start at the project list
```

| Key | Projects | Sessions |
|---|---|---|
| ↑ ↓ / j k | move | move |
| `/` | filter by path | filter by title, first prompt or id |
| Enter / → | open the project | view in `glow -p` (falls back to `less`) |
| `o` | | write `$TMPDIR/<id>.md` and open it in VS Code |
| `D` | | delete, after a y/N prompt |
| ← / Esc | | back (Esc clears a filter first) |
| `r` | rescan | rescan |
| `q` | quit | quit |

## What it reads

Sessions are `~/.claude/projects/<project>/<uuid>.jsonl` (or under
`$CLAUDE_CONFIG_DIR`). Each row shows the full id, last modified time, file size
and title:

- `✎ yellow`: renamed with `/rename` (the last `custom-title` record wins)
- plain: the AI-generated title (`ai-title` record)
- dimmed: neither, so the first prompt is shown instead

A green `●` marks a session a running Claude Code process has open, taken from
`~/.claude/sessions/<pid>.json` with the pid checked for liveness.

The markdown matches the old `ccview` shell function: user and assistant text
only, no tool calls, tool results or meta records.

## What `D` deletes

- the transcript `<uuid>.jsonl`
- its sibling folder `<uuid>/` (tool results, subagent transcripts, title)
- `~/.claude/file-history/<uuid>` and `~/.claude/session-env/<uuid>`

It refuses running sessions and never touches the project folder, so
`memory/` survives even when the last session goes. Prompts typed in the
session stay in `~/.claude/history.jsonl` (the prompt history).

## Tests

`go test ./...` builds a fake `~/.claude` in a temp dir, checks scanning, title
precedence and markdown, then drives the UI through filter, cancel, delete and
the running-session guard.
