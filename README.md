# ccs

Browse Claude Code sessions in the terminal, grouped by project. Search the
text of all of them, see which running ones are waiting for you, preview how a
conversation ended, then view it as markdown, resume it, open it in VS Code, or
delete it.

![ccs demo: browse projects with a preview, read a session, search all sessions, delete](demo/demo.gif)

## Install

A prebuilt binary for macOS or Linux (Intel and ARM), no Go needed:

```sh
os=$(uname -s | tr A-Z a-z); arch=$(uname -m | sed 's/x86_64/amd64/; s/aarch64/arm64/')
curl -fsSL "https://github.com/vedran-nikolic/ccs/releases/latest/download/ccs_${os}_${arch}.tar.gz" | tar -xz
sudo mv "ccs_${os}_${arch}/ccs" /usr/local/bin/
```

Or with Go 1.27.4 or newer:

```sh
go install github.com/vedran-nikolic/ccs@latest
```

The macOS binaries are not signed. Downloaded with `curl` as above they run
as they are; one downloaded with a browser is blocked by Gatekeeper until you
run `xattr -d com.apple.quarantine ccs`.

Optional: `glow` for rendered markdown (otherwise `less -R`), VS Code's `code`
on `PATH` for `o`, and `claude` on `PATH` for `c`.

## Use

```sh
ccs                    # inside a project dir: opens on its sessions
ccs -a                 # always start at the project list
ccs -preview=right     # preview beside the list; also bottom, off, auto (default)
ccs -v                 # print the version
```

"Inside a project dir" means the exact directory Claude Code was started in. A
subdirectory starts at the project list.

The lists stay current on their own: every two seconds ccs checks for sessions
started, renamed, closed or removed in other terminals. The cursor stays on the
session it was on, and a filter stays applied.

## Keys

| Key | Projects | Sessions and search results |
|---|---|---|
| ↑ ↓ / j k | move | move |
| PgUp PgDn / b f | page | page |
| Home End / g G | first, last | first, last |
| `/` | fuzzy filter by path | fuzzy filter by title, first prompt or id |
| Enter / → / l | open the project | view in `glow -p` (falls back to `less -R`); from a search, at the mention |
| `s` | search the text of all sessions | search the text of all sessions |
| `p` | show or hide the preview | show or hide the preview |
| `P` | move the preview: beside the list or under it | the same |
| `c` | | resume in Claude Code (`claude --resume <id>`, in the session's directory) |
| `o` | | write `$TMPDIR/<id>.md` and open it in VS Code |
| `t` | | add `[OPEN]` to the end of the title, or take it off |
| `n` | | rename: edit the title on the bottom line |
| `D` / `d` / Delete | | delete, after a y/N prompt |
| ← / h / Backspace / Esc | Esc clears a filter | back (Esc clears a filter first) |
| `r` | reread everything | reread everything |
| `?` | all keys | all keys |
| `q` / Ctrl+C | quit | quit |

`?` lists the keys that work where you are; the bottom of the window always
shows the most used ones.

While a `/` filter is being typed, every key goes into it: Enter keeps the
filter and goes back to the list, Esc drops it. In the search prompt that
`s` opens, Enter searches, Esc cancels, Backspace deletes a character, Ctrl+U
clears the line, and pasted text goes in as one line.

At the rename prompt that `n` opens, the current title is already typed in:
Enter renames, Esc cancels, Backspace deletes a character, Ctrl+W a word and
Ctrl+U the whole line.

At the delete prompt, `y` deletes and any other key keeps the session.

Results and errors (`deleted 0c9b7e24 (3 paths)`, `opened … in VS Code`,
`kept`, `searched 212 sessions in 38 ms`) show on the bottom line.

## Search

`/` narrows the list you are looking at by title. `s` looks through what was
said: type some text and ccs lists every session, in any project, where you or
Claude wrote it, ignoring case. Each result shows how many messages mention it
and the text around the first one. Enter opens the conversation at that
mention, and `c`, `o` and `D` work as in a session list. Esc goes back.

Tool calls and tool output are not searched, only the conversation itself.

## Open sessions and renaming

Mark the sessions you left unfinished with `t`: it adds `[OPEN]` to the end of
the title, and `t` again takes it off. A session without a title gets one
from its first prompt. `/[open]` then lists only the open ones.

`n` renames a session to anything you type.

Both rename the way `/rename` in Claude Code does: they add a `custom-title`
record to the transcript, which is where Claude Code takes the title from when
the session is resumed, and update `<uuid>/custom-title.json` to match. The
transcript keeps its modification time, so a renamed session stays where it
was in the list.

A session open in a running Claude Code cannot be renamed from ccs, since that
process writes its own title back into the transcript. Use `/rename` there.

Taking the tag off a session that only had an AI title leaves a rename equal
to that title. ccs shows it as the plain AI title again; Claude Code's own
`/resume` list counts it as renamed.

## Preview

The preview pane shows what is under the cursor: a project's latest sessions,
the last messages of a session, or the message a search found.

It goes where the window has room for it. From 150 columns up it stands beside
the list and runs the full height; in a narrower window it lies under the list
and takes two fifths of the height. `P` moves it to the other side and keeps it
there, and `-preview=right` or `-preview=bottom` starts that way.

The pane is filled from the newest message back: each message gets up to a
third of it, and the oldest one shown takes whatever room is left.

`p` hides and shows it, and `-preview=off` (or `-no-preview`) starts without
it. A window too small goes without: under 20 lines for the pane below, under
80 columns or 8 lines for the pane beside. A side picked with `P` or
`-preview` is kept even then: the pane comes back when the window grows,
instead of moving to the other side. Pressing `p` or `P` while there is no
room says so on the bottom line.

## What it reads

Sessions are `~/.claude/projects/<project>/<uuid>.jsonl` (or under
`$CLAUDE_CONFIG_DIR`). Projects are sorted by their newest session, and sessions
newest first. Projects with no transcripts are hidden.

Dates follow your region: `14.11.2026 15:04` in Croatia, `11/14/2026 03:04 PM`
in the US, ISO `2026-11-14 15:04` where the region is unknown. The region comes
from `LC_ALL`, then `LC_TIME`, then on macOS the region in System Settings,
then `LANG`.

A project row shows the number of sessions, the last modified time and the
working directory. A session row shows the full id, last modified time, file
size and title:

- `✎ yellow`: renamed with `/rename`, `t` or `n` (the last `custom-title`
  record wins; one equal to the AI title is shown as the AI title)
- plain: the AI-generated title (`ai-title` record)
- dimmed: neither, so the first prompt is shown instead

A `●` marks a session a running Claude Code process has open, and a project
containing one. Its colour says what that process is doing:

- green: idle, so it is your turn
- yellow: busy working
- magenta: waiting on a prompt, such as a permission request

The preview's top line says the same in words. This comes from
`~/.claude/sessions/<pid>.json`, with the pid checked for liveness. `c`, `D`, `t`
and `n` all refuse a running session.

`c` hands the terminal to Claude Code; ccs is back when you exit it.

The markdown has a heading per message with user and assistant text only. Tool
calls, tool results and meta records are left out.

## What `D` deletes

- the transcript `<uuid>.jsonl`
- its sibling folder `<uuid>/` (tool results, subagent transcripts, title)
- `~/.claude/file-history/<uuid>` and `~/.claude/session-env/<uuid>`

It refuses running sessions, checking again at the moment you confirm, and
never touches the project folder, so `memory/` survives even when the last
session goes. The project then drops out of the list. Prompts typed in the session stay in `~/.claude/history.jsonl`
(the prompt history).

## Tests

`go test ./...` builds a fake `~/.claude` in a temp dir, checks scanning, title
precedence, markdown, search and date formats, then drives the UI through
filter, cancel, delete, the running-session guard, the background refresh,
search, and the preview, including which side it takes at each window size.

Pushing a `v*` tag builds the release archives (`.github/workflows/release.yml`).

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
