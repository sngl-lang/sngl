package js

import (
	"testing"
	"testing/fstest"

	"duckfam.us/sngl/ir"
)

// purityOf imports src as a js: module and reports the purity of each
// exported function, the way the compiler reads it.
func purityOf(t *testing.T, src string) map[string]ir.Purity {
	t.Helper()
	fsys := fstest.MapFS{"lib/index.ts": {Data: []byte(src)}}
	ni, err := (&JSImporter{}).ResolveFS("./lib", fsys, "")
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]ir.Purity{}
	for _, f := range ni.Funcs {
		out[f.Name] = f.Purity
	}
	return out
}

func check(t *testing.T, src string, want map[string]ir.Purity) {
	t.Helper()
	got := purityOf(t, src)
	for name, w := range want {
		if _, seen := got[name]; !seen {
			t.Errorf("%s was not imported at all", name)
			continue
		}
		if got[name] != w {
			t.Errorf("%s: purity %v, want %v", name, got[name], w)
		}
	}
}

// Every accepted marker, in the comment form its own ecosystem writes it in.
func TestAcceptedMarkers(t *testing.T) {
	check(t, `
/** @sngl-pure */
export function ours(s: string): string { return s; }

// @sngl-pure
export function lineComment(s: string): string { return s; }

/**
 * Shouts s.
 * @sngl-pure
 */
export function jsdocBlock(s: string): string { return s; }

/** @__NO_SIDE_EFFECTS__ */
export function rollupAt(s: string): string { return s; }

/*#__NO_SIDE_EFFECTS__*/
export function rollupHash(s: string): string { return s; }

/** @nosideeffects */
export function closure(s: string): string { return s; }

/** @__PURE__ */
export function pureAt(s: string): string { return s; }

/*#__PURE__*/
export function pureHash(s: string): string { return s; }
`, map[string]ir.Purity{
		"ours":        ir.PurityPure,
		"lineComment": ir.PurityPure,
		"jsdocBlock":  ir.PurityPure,
		"rollupAt":    ir.PurityPure,
		"rollupHash":  ir.PurityPure,
		"closure":     ir.PurityPure,
		"pureAt":      ir.PurityPure,
		"pureHash":    ir.PurityPure,
	})
}

// Silence means no, and so does anything that only looks like a marker. A
// marker counts as its own token on its own line; a longer word that contains
// one, a different casing of one, and a mention of one in prose all leave the
// function alone.
func TestRejectedMarkers(t *testing.T) {
	check(t, `
export function bare(s: string): string { return s; }

/** Does nothing surprising. */
export function described(s: string): string { return s; }

/** @sngl-pure-ish */
export function suffixed(s: string): string { return s; }

/** @not-sngl-pure */
export function prefixed(s: string): string { return s; }

/** @SNGL-PURE */
export function upper(s: string): string { return s; }

/** @NoSideEffects */
export function mixedCase(s: string): string { return s; }

/** This is not @__PURE__ in the Rollup sense, so do not fold it. */
export function prose(s: string): string { return s; }

/** See @sngl-pure for the marker that would make this foldable. */
export function referenced(s: string): string { return s; }
`, map[string]ir.Purity{
		"bare":       ir.PurityUnknown,
		"described":  ir.PurityUnknown,
		"suffixed":   ir.PurityUnknown,
		"prefixed":   ir.PurityUnknown,
		"upper":      ir.PurityUnknown,
		"mixedCase":  ir.PurityUnknown,
		"prose":      ir.PurityUnknown,
		"referenced": ir.PurityUnknown,
	})
}

// A marker in a string is not a marker: only comments are read.
func TestMarkerInStringIsNotAMarker(t *testing.T) {
	check(t, `
export function fromString(s: string): string { return "@sngl-pure"; }
`, map[string]ir.Purity{"fromString": ir.PurityUnknown})
}

// A TypeScript declaration's Pos() is its *full* start, so the first
// declaration in a file owns every byte from offset zero — the file header
// comment included. Only the blank-line rule keeps that header from annotating
// it.
func TestFileHeaderDoesNotAnnotate(t *testing.T) {
	check(t, `// A module of string helpers.
// @sngl-pure

export function first(s: string): string { return s; }
`, map[string]ir.Purity{"first": ir.PurityUnknown})
}

// The trivia before a declaration runs back to the end of the previous one, so
// a marker belonging to an earlier declaration must not carry forward, and a
// declaration whose own comment sits behind a blank-line-separated block must
// still be read.
func TestTriviaBelongsToOneDeclaration(t *testing.T) {
	check(t, `
/** @sngl-pure */
export function annotated(s: string): string { return s; }

export function next(s: string): string { return s; }

// A note about the section below.

/** @sngl-pure */
export function afterANote(s: string): string { return s; }
`, map[string]ir.Purity{
		"annotated":  ir.PurityPure,
		"next":       ir.PurityUnknown,
		"afterANote": ir.PurityPure,
	})
}
