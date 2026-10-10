package sngl_test

import (
	"strings"
	"testing"

	"duckfam.us/sngl/internal/checker"
	"duckfam.us/sngl/internal/lower"
	"duckfam.us/sngl/internal/parser"
	"duckfam.us/sngl/ir"
)

// TestReactiveLoweredRoundTrip pins that reactive-lowered output — whose
// updater statements reference visual nodes by their synthesized `#__nN` tag
// (e.g. `__n0.value = ...`) — reparses and re-type-checks cleanly. The bare
// `__n0` reference resolves because a node's `#id` tag declares a
// component-scoped binding. Unlike FuzzLoweredDocument (NoReactivity +
// NoDeclarative, which turns every node into a declared `var __nN`), this uses
// NoReactivity alone so the named-node-tag + bare-reference shape is exercised.
func TestReactiveLoweredRoundTrip(t *testing.T) {
	const src = `component main node {
    var n int = 0
    text(value=string(n))
    button(text="+", @click { n = n + 1 })
}`
	doc, err := parser.Parse("rt.sngl", []byte(withStdSrc(src)))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	pkg, diags := checker.Check(doc, &checker.Config{IsMain: true})
	if hasError(diags) {
		t.Fatalf("initial check: %s", joinDiags(diags))
	}
	// Everything claimed but reactivity, so passReactivity is the one pass
	// under test and nothing else rewrites the tree out from under it.
	feats := lower.NoLowering()
	feats.Reactivity = false
	if err := lower.Lower(pkg, feats, lower.Options{}); err != nil {
		t.Fatalf("lower: %v", err)
	}

	out := parser.Format(ir.Convert(pkg))
	if !strings.Contains(out, "__n0.value") {
		t.Fatalf("expected a bare __n0 node reference in lowered output, got:\n%s", out)
	}

	reparsed, err := parser.Parse("rt.lowered.sngl", []byte(withStdSrc(out)))
	if err != nil {
		t.Fatalf("reparse lowered output failed: %v\n--- output ---\n%s", err, out)
	}
	if _, diags := checker.Check(reparsed, &checker.Config{IsMain: true, Lowered: true}); hasError(diags) {
		t.Fatalf("recheck lowered output failed: %s\n--- output ---\n%s", joinDiags(diags), out)
	}
}
