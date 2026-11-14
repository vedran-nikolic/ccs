#!/bin/sh
# Copyright 2026 Proxi projekt d.o.o.
# SPDX-License-Identifier: GPL-3.0-only
# Re-records demo/demo.cast and demo/demo.gif against invented sessions.
# Needs: go, python3, asciinema 3, agg, glow (brew install asciinema agg glow)
set -eu
cd "$(dirname "$0")"

work=$(mktemp -d)
trap 'kill "$live" 2>/dev/null; rm -rf "$work"' EXIT

go build -o "$work/bin/ccs" ..
sleep 3600 & live=$!   # stands in for a running Claude Code
python3 fixture.py "$work/claude" "$live"

PATH="$work/bin:$PATH" CLAUDE_CONFIG_DIR="$work/claude" TERM=xterm-256color LANG=en_GB.UTF-8 LC_ALL=en_GB.UTF-8 \
  asciinema rec --overwrite --quiet --headless --window-size 108x30 \
    --idle-time-limit 3 --title "ccs: browse Claude Code sessions" \
    --command "python3 drive.py" demo.cast

agg --theme github-dark --font-size 15 --last-frame-duration 2 demo.cast demo.gif
ls -lh demo.cast demo.gif
