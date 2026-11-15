// Copyright 2026 Proxi projekt d.o.o.
// SPDX-License-Identifier: GPL-3.0-only

// ccs browses Claude Code sessions by project: view one as markdown, resume
// it, open it in VS Code, or delete it.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"

	tea "charm.land/bubbletea/v2"
)

// version is set by release builds; go install leaves it to the module version.
var version = ""

func main() {
	all := flag.Bool("a", false, "start at the project list even inside a project")
	showVersion := flag.Bool("v", false, "print the version and exit")
	where := flag.String("preview", "auto", "where the preview pane goes: auto, right, bottom or off (P moves it, p hides it)")
	noPreview := flag.Bool("no-preview", false, "the same as -preview=off")
	flag.Parse()
	place, show, ok := parsePlacement(*where)
	if !ok {
		fmt.Fprintf(os.Stderr, "ccs: -preview=%s: want auto, right, bottom or off\n", *where)
		os.Exit(2)
	}

	if *showVersion {
		v := version
		if bi, ok := debug.ReadBuildInfo(); ok && v == "" {
			v = bi.Main.Version
		}
		fmt.Println("ccs", v)
		return
	}
	timeLayout = layoutFor(userRegion())

	projects, err := scanAll(nil)
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
		for _, p := range projects { // the cwd its sessions recorded is the surer match
			for _, s := range p.Sessions {
				if s.Cwd == wd {
					startIn = p.Dir
				}
			}
		}
	}

	m := newModel(projects, startIn)
	m.place, m.preview = place, show && !*noPreview
	if _, err := tea.NewProgram(m).Run(); err != nil {
		fmt.Fprintln(os.Stderr, "ccs:", err)
		os.Exit(1)
	}
}
