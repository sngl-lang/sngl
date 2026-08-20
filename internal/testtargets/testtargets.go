// Package testtargets supplies the registered languages and platforms to
// tests that type-check fixtures.
//
// It exists as its own package rather than as part of internal/testutil
// because the platform packages' own tests are internal test packages
// (`package html`, `package bubbletea`, …) and import testutil. Pulling
// codegen/platform into testutil would close that loop into an import cycle.
package testtargets

import (
	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/ir"

	// Register every language and platform, exactly as cmd/sngl does.
	_ "git.duckfam.us/jonathan/sngl/codegen/lang"
	_ "git.duckfam.us/jonathan/sngl/codegen/platform"
)

// Targets returns every registered language and platform, mirroring the set
// the real CLI checks against (cmd/sngl.collectTargets). A checker.Config
// built without these silently skips platform element resolution and never
// checks `component sngl.X` platform-extension bodies.
func Targets() ([]ir.Language, []ir.Platform) {
	var langs []ir.Language
	for _, name := range codegen.Langs() {
		if l := codegen.LookupLang(name); l != nil {
			langs = append(langs, l)
		}
	}
	var plats []ir.Platform
	for _, name := range codegen.Platforms() {
		if p := codegen.LookupPlatform(name); p != nil {
			plats = append(plats, p)
		}
	}
	return langs, plats
}
