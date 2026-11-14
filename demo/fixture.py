#!/usr/bin/env python3
# Copyright 2026 Proxi projekt d.o.o.
# SPDX-License-Identifier: GPL-3.0-only
"""Build a fake Claude Code config dir for the demo recording.

usage: fixture.py <claude-config-dir> <live-pid>

Every project, title and message here is invented, so the recording shows no
real sessions. Paths sit under $HOME so ccs prints them as ~/...
"""
import json
import os
import sys
import time

root, live_pid = sys.argv[1], int(sys.argv[2])
home = os.path.expanduser("~")
now = time.time()
H = 3600


def user(cwd, text):
    return {"type": "user", "cwd": cwd, "message": {"role": "user", "content": text}}


def asst(text):
    return {"type": "assistant", "message": {"role": "assistant", "content": [
        {"type": "tool_use", "name": "Bash", "input": {"command": "true"}},
        {"type": "text", "text": text}]}}


def tool_noise(n):
    # tool results make up most of a real transcript; ccs skips them
    return [{"type": "user", "message": {"content": [
        {"type": "tool_result", "content": "x" * 900}]}} for _ in range(n)]


CHECKOUT = [
    ("The checkout handler is 600 lines. Split it so payment, tax and shipping "
     "can be tested on their own.",
     "Split into three steps behind one interface:\n\n"
     "```go\ntype Step interface {\n\tApply(ctx context.Context, o *Order) error\n}\n```\n\n"
     "- `payment.go`: authorise, capture, refund\n"
     "- `tax.go`: rates per country, rounding\n"
     "- `shipping.go`: carrier quotes\n\n"
     "`go test ./checkout/...` passes, 41 tests, 2 new."),
    ("Does the refund path still work when capture failed?",
     "No. `Refund` assumed a captured charge and returned a 400 from the "
     "provider. It now voids the authorisation instead, with a test for "
     "both cases."),
    ("Ship it behind a flag.",
     "Added `checkout_v2` to `flags.yaml`, off by default. The old handler "
     "stays the fallback until the flag is removed."),
]

PROJECTS = {
    "code/webshop": [
        # (id, age in hours, title kind, title, conversation, tool noise)
        ("6f1c2a9e-3b7d-4e21-9a55-0c8e7d1b4f62", 0.1, "renamed", "Checkout rewrite", CHECKOUT, 400),
        ("b2d84f17-90ce-4a3b-8e6f-5d21c7a9e034", 3, "ai", "Fix flaky cart tests",
         [("cart_test.go fails one run in five on CI", "The tests share a package-level `clock`. "
           "Each test now gets its own `fakeClock`, and `-count=50` passes.")], 60),
        ("e7a03c55-1f2b-4d8e-b6c9-2a4f80d7e1b3", 26, "ai", "Add Stripe webhooks",
         [("handle stripe webhooks for refunds", "Added `/webhooks/stripe` with signature checks "
           "and idempotency keys stored in `webhook_events`.")], 150),
        ("0c9b7e24-6a1d-4f3c-a8b2-e5d71f09c4a6", 50, "none", "",
         [("why is the build 3x slower since yesterday?", "`go.sum` pulled in a new "
           "`protobuf` that disables the build cache for generated code.")], 20),
        ("4a6e1d93-c2b8-47f0-9d3e-81b5a0c6f72d", 98, "ai", "Dark mode for admin panel",
         [("admin panel needs a dark mode", "Colours are now CSS variables with a "
           "`prefers-color-scheme` override.")], 90),
    ],
    "code/infra": [
        ("9d2f6b18-7e4a-4c0d-b1a3-6f8e2c5d9a07", 6, "renamed", "Postgres 18 upgrade",
         [("plan the postgres 16 -> 18 upgrade", "pg_upgrade with `--link`, about four "
           "minutes of downtime on the replica first.")], 200),
        ("3e8a5c71-d4b9-4f26-a0e7-9c1b3d6f8e42", 30, "ai", "Terraform state migration",
         [("move terraform state to s3", "State is in S3 with a DynamoDB lock table.")], 80),
    ],
    "code/blog": [
        ("c5b1e8d2-4a7f-4e93-8c06-2d9f7a3b1e58", 20, "ai", "Draft post on Go iterators",
         [("help me outline a post about range-over-func", "1. The problem\n2. `iter.Seq`\n3. Pitfalls")], 10),
        ("7f4d2a96-b3e1-4c58-9f0a-e6c8b2d4a170", 140, "none", "",
         [("fix the RSS feed dates", "They were in local time; now RFC 3339 UTC.")], 5),
    ],
    "notes": [
        ("a1e9c3f5-8b2d-4a67-b4e0-7c3d9f1a5b86", 75, "ai", "Weekly review",
         [("summarise this week's notes", "Three themes: hiring, the checkout rewrite, on-call load.")], 4),
    ],
}

for rel, sessions in PROJECTS.items():
    cwd = os.path.join(home, rel)
    pdir = os.path.join(root, "projects", cwd.replace("/", "-").replace(".", "-"))
    os.makedirs(pdir, exist_ok=True)
    os.makedirs(os.path.join(pdir, "memory"), exist_ok=True)
    with open(os.path.join(pdir, "memory", "MEMORY.md"), "w") as f:
        f.write("demo\n")
    for sid, age, kind, title, convo, noise in sessions:
        recs = [{"type": "user", "isMeta": True, "message": {"content": "<local-command-caveat/>"}}]
        for q, a in convo:
            recs += [user(cwd, q), *tool_noise(noise // max(len(convo), 1)), asst(a)]
        if kind == "ai":
            recs.append({"type": "ai-title", "aiTitle": title})
        elif kind == "renamed":
            recs.append({"type": "ai-title", "aiTitle": "Refactor checkout handler"})
            recs.append({"type": "custom-title", "customTitle": title})
        path = os.path.join(pdir, sid + ".jsonl")
        with open(path, "w") as f:
            f.writelines(json.dumps(r, separators=(",", ":")) + "\n" for r in recs)
        t = now - age * H
        os.utime(path, (t, t))
        # things D removes along with the transcript
        os.makedirs(os.path.join(pdir, sid, "tool-results"), exist_ok=True)
        os.makedirs(os.path.join(root, "file-history", sid), exist_ok=True)

# Two sessions are "open" in a running Claude Code: one busy, one idle.
os.makedirs(os.path.join(root, "sessions"), exist_ok=True)
for name, status, sid in [("1", "busy", PROJECTS["code/webshop"][0][0]),
                          ("2", "idle", PROJECTS["code/infra"][0][0])]:
    with open(os.path.join(root, "sessions", name + ".json"), "w") as f:
        json.dump({"pid": live_pid, "sessionId": sid, "status": status}, f)
