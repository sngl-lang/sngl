// Package docbrowser drives `sngl doc --tui` and `sngl doc --http`.
// The bubbletea Model and html Handler are generated from docbrowser.sngl;
// this file only provides Run entry points and workspace wiring.
package docbrowser

import (
	"git.duckfam.us/jonathan/sngl/docs"

	tea "charm.land/bubbletea/v2"
)

// Run launches the doc browser against the current working directory.
func Run() error { return RunWithDir(".") }

// RunWithDir launches the doc browser rooted at dir. The workspace is
// threaded through docs.SetWorkspaceDir so lookup.Index() picks up the
// cwd's package and its aliased imports.
func RunWithDir(dir string) error {
	if dir != "" {
		docs.SetWorkspaceDir(dir)
	}
	p := tea.NewProgram(New())
	_, err := p.Run()
	return err
}
