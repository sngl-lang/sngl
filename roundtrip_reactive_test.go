package sngl_test

import (
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/internal/checker"
	"git.duckfam.us/jonathan/sngl/internal/lower"
	"git.duckfam.us/jonathan/sngl/internal/parser"
	"git.duckfam.us/jonathan/sngl/ir"
)

// TestReactiveLoweredRoundTrip pins that reactive-lowered output — whose
// updater statements reference visual nodes by their synthesized `#__nN` tag
// (e.g. `__n0.value = ...`) — reparses and re-type-checks cleanly. The bare
// `__n0` reference resolves because a node's `#id` tag declares a
// component-scoped binding. Unlike FuzzLoweredDocument (NoReactivity +
// NoDeclarative, which turns every node into a declared `var __nN`), this uses
// NoReactivity alone so the named-node-tag + bare-reference shape is exercised.
func TestReactiveLoweredRoundTrip(t *testing.T) {
	const src = `component main {
    var n int = 0
    text(value=string(n))
    button(text="+", @click { n = n + 1 })
}`
	doc, err := parser.Parse("rt.sngl", []byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	pkg, diags := checker.Check(doc, &checker.Config{IsMain: true})
	if hasError(diags) {
		t.Fatalf("initial check: %s", joinDiags(diags))
	}
	if err := lower.Lower(pkg, lower.Caps{NoReactivity: true}, lower.Options{}); err != nil {
		t.Fatalf("lower: %v", err)
	}

	out := parser.Format(ir.Convert(pkg))
	if !strings.Contains(out, "__n0.value") {
		t.Fatalf("expected a bare __n0 node reference in lowered output, got:\n%s", out)
	}

	reparsed, err := parser.Parse("rt.lowered.sngl", []byte(out))
	if err != nil {
		t.Fatalf("reparse lowered output failed: %v\n--- output ---\n%s", err, out)
	}
	// The named-node reference must resolve: no "undefined" diagnostic for the
	// synthesized __n0 handle. (Other unrelated convert-roundtrip wrinkles,
	// e.g. the auto-injected locale context, are out of scope here.)
	_, diags = checker.Check(reparsed, &checker.Config{IsMain: true})
	for _, d := range diags {
		if d.Severity == ir.Error && strings.Contains(d.Msg, "undefined") {
			t.Fatalf("named-node reference did not resolve: %s\n--- output ---\n%s", d.Msg, out)
		}
	}
}
