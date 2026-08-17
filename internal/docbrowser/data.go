// Package docbrowser drives `sngl doc --tui` and `sngl doc --http`.
// The bubbletea Model and html Handler are generated from docbrowser.sngl;
// this file only provides Run entry points and workspace wiring.
package docbrowser

import (
	tea "charm.land/bubbletea/v2"
)

// RunWithDir launches the doc browser rooted at dir.
func RunWithDir(dir string) error {
	p := tea.NewProgram(New())
	_, err := p.Run()
	return err
}
