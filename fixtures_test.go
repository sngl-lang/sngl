package sngl_test

import (
	"testing"

	"duckfam.us/sngl/internal/fixtures"

	// Register every language and platform, so a fixture checked here is
	// checked against the targets a build would offer it, and every import
	// scheme, so one that imports a `go:` or `c:` package resolves the way it
	// does under the CLI.
	_ "duckfam.us/sngl/codegen/scheme"
	_ "duckfam.us/sngl/internal/testtargets"
)

// TestFixtures runs every testdata/*.sngl fixture through every phase its own
// directives ask for — parse, format, check, fold, LSP markers — reading,
// parsing and checking each exactly once.
//
// It lives beside TestGolden rather than in the packages whose phases it
// exercises, which is the point: those packages each globbed testdata and did
// the shared work over again, and two of the walks had drifted into asserting
// different things about the same 553 programs. internal/fixtures carries the
// reasoning.
func TestFixtures(t *testing.T) {
	fixtures.Run(t)
}
