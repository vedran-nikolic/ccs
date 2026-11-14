// Copyright 2026 Proxi projekt d.o.o.
// SPDX-License-Identifier: GPL-3.0-only

// ccs browses Claude Code sessions by project: view one as markdown, open it
// in VS Code, or delete it.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"
)

func main() {
	all := flag.Bool("a", false, "start at the project list even inside a project")
	flag.Parse()

	projects, err := scanAll()
	if err != nil {
		fmt.Fprintln(os.Stderr, "ccs:", err)
		os.Exit(1)
	}
	if len(projects) == 0 {
		fmt.Println("ccs: no sessions found")
		return
	}

	// Started inside a project, ccs opens straight on its sessions.
	startIn := ""
	if wd, err := os.Getwd(); err == nil && !*all {
		enc := strings.NewReplacer("/", "-", ".", "-").Replace(wd)
		startIn = filepath.Join(claudeDir(), "projects", enc)
	}

	if _, err := tea.NewProgram(newModel(projects, startIn)).Run(); err != nil {
		fmt.Fprintln(os.Stderr, "ccs:", err)
		os.Exit(1)
	}
}
