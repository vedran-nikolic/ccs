# ccs

Browse Claude Code sessions in the terminal, grouped by project. View one as
markdown, open it in VS Code, or delete it.

![ccs demo: browse projects, read a session in glow, filter, delete](demo/demo.gif)

```sh
go build -o ccs .      # or: go install
ccs                    # inside a project dir: opens on its sessions
ccs -a                 # always start at the project list
```

Needs Go 1.27.4 or newer to build, and runs on macOS and Linux. Optional: `glow` for
rendered markdown (otherwise `less -R`), and VS Code's `code` on `PATH` for `o`.

"Inside a project dir" means the exact directory Claude Code was started in. A
subdirectory starts at the project list.

## Keys

| Key | Projects | Sessions |
|---|---|---|
| ↑ ↓ / j k | move | move |
| PgUp PgDn / b f | page | page |
| `/` | fuzzy filter by path | fuzzy filter by title, first prompt or id |
| Enter / → / l | open the project | view in `glow -p` (falls back to `less -R`) |
| `o` | | write `$TMPDIR/<id>.md` and open it in VS Code |
| `D` / `d` / Delete | | delete, after a y/N prompt |
| ← / h / Backspace / Esc | | back (Esc clears a filter first) |
| `r` | rescan | rescan, staying in the project |
| `?` | all keys | all keys |
| `q` / Ctrl+C | quit | quit |

Results and errors (`deleted 0c9b7e24 (3 paths)`, `opened … in VS Code`,
`kept`) show on the bottom line.

## What it reads

Sessions are `~/.claude/projects/<project>/<uuid>.jsonl` (or under
`$CLAUDE_CONFIG_DIR`). Projects are sorted by their newest session, and sessions
newest first. Projects with no transcripts are hidden.

A project row shows the number of sessions, the last modified time and the
working directory. A session row shows the full id, last modified time, file
size and title:

- `✎ yellow`: renamed with `/rename` (the last `custom-title` record wins)
- plain: the AI-generated title (`ai-title` record)
- dimmed: neither, so the first prompt is shown instead

A green `●` marks a session a running Claude Code process has open, and a
project containing one. It comes from `~/.claude/sessions/<pid>.json`, with the
pid checked for liveness.

The markdown has a heading per message with user and assistant text only. Tool
calls, tool results and meta records are left out.

## What `D` deletes

- the transcript `<uuid>.jsonl`
- its sibling folder `<uuid>/` (tool results, subagent transcripts, title)
- `~/.claude/file-history/<uuid>` and `~/.claude/session-env/<uuid>`

It refuses running sessions and never touches the project folder, so
`memory/` survives even when the last session goes. The project then drops out
of the list. Prompts typed in the session stay in `~/.claude/history.jsonl`
(the prompt history).

## Tests

`go test ./...` builds a fake `~/.claude` in a temp dir, checks scanning, title
precedence and markdown, then drives the UI through filter, cancel, delete and
the running-session guard.

## Demo

`demo/demo.gif` is rendered from `demo/demo.cast` (play it with
`asciinema play demo/demo.cast`). Every project and session in it is invented.
To re-record after a UI change:

```sh
brew install asciinema agg glow
demo/record.sh
```

`record.sh` builds ccs, writes a fake config dir with `demo/fixture.py` (one
session marked as running), and records `demo/drive.py` typing keys into ccs in
a pty.

## Licence

Copyright © 2026 Proxi projekt d.o.o.

GPL-3.0 only, see [LICENSE](LICENSE). You may use, change and share ccs, also
commercially. If you distribute it or anything built from it, you must keep
the copyright notice and release your source under the same licence.

Bug reports and ideas are welcome as issues. Pull requests are not accepted at
this time.
