package checker_test

import (
	"slices"
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/internal/checker"
	"git.duckfam.us/jonathan/sngl/internal/lower"
	"git.duckfam.us/jonathan/sngl/internal/parser"
	"git.duckfam.us/jonathan/sngl/ir"
)

// extStubPlatform is a minimal ir.Platform used by extension-merge tests. It
// ships a single new-form `component sngl.text { ... }`
// extension so the merge pass has something to splice into the stdlib's
// abstract `text` component. There is no lib/platforms/extstub directory, so
// the source is handed to the checker through Config.LibSources.
type extStubPlatform struct{}

func (extStubPlatform) PlatformIdentifier() string { return "extstub" }
func (extStubPlatform) Description() string        { return "extension-merge test stub" }
func (extStubPlatform) Resolve(string) ir.Symbol   { return nil }

// extStubConfig builds a Config registering the stub platform with source as
// its sngl:platform/extstub package.
func extStubConfig(t *testing.T, source string) *checker.Config {
	t.Helper()
	doc, err := parser.Parse("extstub.sngl", []byte(withStd(source)))
	if err != nil {
		t.Fatalf("extstub parse: %v", err)
	}
	return &checker.Config{
		IsMain:     true,
		Platforms:  []ir.Platform{extStubPlatform{}},
		LibSources: map[string][]*ast.Document{"platform/extstub": {doc}},
	}
}

// TestExtensionMergeBasic exercises the platform-agnostic checker collection
// + lower-time swap. An extension platform ships a new-form
// `component sngl.text { ... }` declaration. The checker
// stashes the checked IR body under stdText.PlatformOverrides["extstub"]; the
// stdlib `text` component's own Body stays empty after Check (the checker
// does not know which platform is active). After running Lower with
// Options.Platform="extstub", the swap pass moves the platform body into
// stdText.Body so subsequent passes (and any inlining) see a body-bearing
// stdlib component. User code uses bare `text(...)` and must type-check.
func TestExtensionMergeBasic(t *testing.T) {
	const extSource = `
import sngl "sngl:ui"

component sngl.text[extstub.platform] {
    image(src=value)
}
`
	const userSource = `
component main {
    text(value="hi")
}
`
	doc, err := parser.Parse("main.sngl", []byte(withStd(userSource)))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	pkg, diags := checker.Check(doc, extStubConfig(t, extSource))
	for _, d := range diags {
		if d.Severity == ir.Error {
			t.Errorf("unexpected error: %s", d.Error())
		}
	}
	if pkg == nil {
		t.Fatal("nil pkg")
	}

	sym, _ := pkg.Symbols.LookupRootComponent("text")
	stdText, ok := sym.(*ir.Component)
	if !ok || stdText == nil {
		t.Fatal("stdlib text component missing from symbol table")
	}

	// Checker contract: PlatformOverrides has the extstub entry; the live
	// Component.Body stays empty until the lowering swap runs.
	if stdText.PlatformOverrides == nil {
		t.Fatal("expected PlatformOverrides populated, got nil")
	}
	body, ok := stdText.PlatformOverrides["extstub"]
	if !ok {
		t.Fatalf("expected PlatformOverrides[\"extstub\"], have keys %v", keys(stdText.PlatformOverrides))
	}
	if len(body.Stmts) == 0 {
		t.Errorf("expected stashed extstub body to be non-empty, got %d stmts", len(body.Stmts))
	}
	if len(stdText.Body) != 0 {
		t.Errorf("expected stdlib text Body still empty pre-lower, got %d stmts", len(stdText.Body))
	}

	// Lower with the matching active platform — swap pass should move the
	// stashed body into Component.Body.
	if err := lower.Lower(pkg, lower.Caps{}, lower.Options{Platform: "extstub"}); err != nil {
		t.Fatalf("lower: %v", err)
	}
	if len(stdText.Body) == 0 {
		t.Errorf("expected stdlib text Body to be swapped in by lower, got 0 stmts")
	}
}

func keys(m map[string]ir.Body) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// namedStubPlatform is extStubPlatform with a choosable identifier, so a test
// can register two stub platforms and watch what each one's extension body
// contributes.
type namedStubPlatform struct{ name string }

func (p namedStubPlatform) PlatformIdentifier() string { return p.name }
func (p namedStubPlatform) Description() string        { return "extension-merge test stub " + p.name }
func (namedStubPlatform) Resolve(string) ir.Symbol     { return nil }

// TestExtensionBodyVars covers a `var` declared inside the `platform` block of
// a `component sngl.X` extension: it must bind while the body is checked, be
// stashed per platform, and reach codegen as ordinary per-instance component
// state.
//
// Two stub platforms each override `sngl.text` with a var of their own. The
// test lowers for one of them, which is what pins the per-platform split: a
// var declared by a platform that is not the build target must not survive.
func TestExtensionBodyVars(t *testing.T) {
	const extA = `
import sngl "sngl:ui"

component sngl.text[stubA.platform] {
    const label string = "L"
    var flip bool = false
    vbox {
        image(src="{label}{flip}")
    }
}
`
	const extB = `
import sngl "sngl:ui"

component sngl.text[stubB.platform] {
    var other int = 7
    vbox {
        image(src="{other}")
    }
}
`
	const userSource = `
component main {
    vbox {
        text(value="one")
        text(value="two")
    }
}
`
	parse := func(name, src string) *ast.Document {
		doc, err := parser.Parse(name, []byte(withStd(src)))
		if err != nil {
			t.Fatalf("%s parse: %v", name, err)
		}
		return doc
	}
	cfg := &checker.Config{
		IsMain:    true,
		Platforms: []ir.Platform{namedStubPlatform{"stubA"}, namedStubPlatform{"stubB"}},
		LibSources: map[string][]*ast.Document{
			"platform/stubA": {parse("stubA.sngl", extA)},
			"platform/stubB": {parse("stubB.sngl", extB)},
		},
	}
	pkg, diags := checker.Check(parse("main.sngl", userSource), cfg)
	for _, d := range diags {
		if d.Severity == ir.Error {
			// The bug this test covers reported `undefined: flip` here.
			t.Errorf("unexpected error: %s", d.Error())
		}
	}
	if pkg == nil {
		t.Fatal("nil pkg")
	}

	sym, _ := pkg.Symbols.LookupRootComponent("text")
	stdText, ok := sym.(*ir.Component)
	if !ok || stdText == nil {
		t.Fatal("stdlib text component missing from symbol table")
	}

	// Checker contract: each platform's vars are stashed under its own key,
	// typed and initialized, and the live Vars slot stays empty until lower.
	for _, want := range []struct {
		platform string
		names    []string
	}{{"stubA", []string{"label", "flip"}}, {"stubB", []string{"other"}}} {
		over, ok := stdText.PlatformOverrides[want.platform]
		if !ok {
			t.Fatalf("expected PlatformOverrides[%q], have %v", want.platform, stdText.PlatformOverrides)
		}
		vars := over.Vars
		if !slices.Equal(varNames(vars), want.names) {
			t.Fatalf("PlatformOverrides[%q] = %v, want %v", want.platform, varNames(vars), want.names)
		}
		for _, v := range vars {
			if v.Init == nil {
				t.Errorf("PlatformOverrides[%q] %q has nil Init: the initializer was never checked", want.platform, v.Name)
			}
		}
	}
	// A `const` in the extension body travels the same road: pass1 records it
	// as a Var with IsConst, and nothing here distinguishes the two.
	if v := stdText.PlatformOverrides["stubA"].Vars[0]; !v.IsConst {
		t.Errorf("expected %q to be a const", v.Name)
	}
	if len(stdText.Vars) != 0 {
		t.Errorf("expected stdlib text Vars still empty pre-lower, got %v", varNames(stdText.Vars))
	}

	// Lower for stubA only. NoInlineComponents is the capability android and
	// bubbletea build with; it hoists component state into main with a
	// per-instance rename, which is where an override's var has to land.
	caps := lower.Caps{NoInlineComponents: true}
	if err := lower.Lower(pkg, caps, lower.Options{Platform: "stubA"}); err != nil {
		t.Fatalf("lower: %v", err)
	}
	var main *ir.Component
	for _, c := range pkg.Components {
		if c.Name == "main" {
			main = c
		}
	}
	if main == nil {
		t.Fatal("main component missing after lower")
	}
	got := varNames(main.Vars)
	want := []string{"label__inst0", "flip__inst0", "label__inst1", "flip__inst1"}
	if !slices.Equal(got, want) {
		t.Errorf("main.Vars = %v, want %v (two instances, independent state)", got, want)
	}
	for _, v := range main.Vars {
		if strings.HasPrefix(v.Name, "other") {
			t.Errorf("var %q from platform stubB leaked into a stubA build", v.Name)
		}
	}
}

func varNames(vars []*ir.Var) []string {
	out := make([]string, 0, len(vars))
	for _, v := range vars {
		out = append(out, v.Name)
	}
	return out
}
