#!/usr/bin/env python3
# Copyright 2026 Proxi projekt d.o.o.
# SPDX-License-Identifier: GPL-3.0-only
"""Drives `ccs -a` in a pty for the recording. Run by record.sh, not directly.

Not expect: the macOS one garbles 4-byte UTF-8 such as the emoji in the
markdown headings.
"""
import os
import pty
import struct
import sys
import termios
import threading
import time
import fcntl

ROWS, COLS = 26, 108
out = sys.stdout.buffer

# What a real terminal would answer. glow waits seconds for each otherwise.
REPLIES = {
    b"\x1b]10;?": b"\x1b]10;rgb:e6e6/eded/f3f3\x1b\\",  # foreground colour
    b"\x1b]11;?": b"\x1b]11;rgb:0d0d/1111/1717\x1b\\",  # background colour
    b"\x1b[6n": b"\x1b[1;1R",                            # cursor position
}


def prompt(cmd):
    out.write(b"\x1b[1;32m$\x1b[0m ")
    out.flush()
    time.sleep(0.6)
    for c in cmd:
        out.write(c.encode())
        out.flush()
        time.sleep(0.12)
    time.sleep(0.4)
    out.write(b"\r\n")
    out.flush()


def pump(fd):
    while True:
        try:
            data = os.read(fd, 65536)
        except OSError:
            return
        if not data:
            return
        for q, a in REPLIES.items():
            if q in data:
                os.write(fd, a)
        # glow pads inline code with no-break spaces, which agg draws as boxes
        out.write(data.replace(b"\xc2\xa0", b" "))
        out.flush()


prompt("ccs -a")
pid, fd = pty.fork()
if pid == 0:
    fcntl.ioctl(0, termios.TIOCSWINSZ, struct.pack("HHHH", ROWS, COLS, 0, 0))
    os.execvp("ccs", ["ccs", "-a"])

reader = threading.Thread(target=pump, args=(fd,), daemon=True)
reader.start()


def key(k, pause=0.6):
    os.write(fd, k.encode())
    time.sleep(pause)


def type_(s):
    for c in s:
        key(c, 0.14)


DOWN, ENTER, ESC = "j", "\r", "\x1b"

time.sleep(2.2)

# projects, newest first; ● = a session open in a running Claude Code
key(DOWN, 0.5); key(DOWN, 0.5); key("k", 0.5); key("k", 0.8)

# open ~/code/webshop
key(ENTER, 2.5)

# read the top session as markdown in glow, then come back
key(ENTER, 2.6)
for _ in range(5):
    key(DOWN, 0.25)
time.sleep(1.2)
key("q", 1.5)

# fuzzy filter by title, first prompt or id
key("/", 0.5)
type_("stripe")
time.sleep(1.2)
key(ENTER, 1.6)
key(ESC, 1.4)

# the running session cannot be deleted
key("k", 0.6)
key("D", 2.8)

# delete the untitled one: n keeps it, y removes it
key(DOWN, 0.4); key(DOWN, 0.4); key(DOWN, 0.8)
key("D", 2.0)
key("n", 1.4)
key("D", 1.6)
key("y", 2.8)

# back to the projects
key(ESC, 1.8)
key("q", 0.5)

os.waitpid(pid, 0)
reader.join(timeout=1)
