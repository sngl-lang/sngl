package fyne

import (
	"sort"
	"testing"

	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/internal/checker"
	"git.duckfam.us/jonathan/sngl/internal/parser"
	"git.duckfam.us/jonathan/sngl/ir"
)

// StdlibComponents is every stdlib component this platform is
// responsible for, one per line of the source below.
//
// The list is written out rather than derived, so it catches an override that
// went missing and NOT a component added to sngl:ui -- adding one there with
// no override anywhere leaves every test here green. Making that a failure
// wants the bodyless-declaration syntax in #123, which turns "a target must
// implement this" into something the compiler can see rather than something
// each platform's test restates.
// TestEveryStdlibComponentHasAFyneBody, with one exception that is not a
// widget.
//
// This used to compare fyne.sngl against a list of names written beside it,
// which could only fail if someone edited one and not the other -- and a
// component added to sngl:ui and overridden nowhere was invisible to it. Asking
// the other way round is what makes it an assertion: every stdlib component
// must have a body here unless it is named below.
func TestEveryStdlibComponentHasAFyneBody(t *testing.T) {
	pkg := checkAllComponents(t)

	// No name is exempt. A built-in node kind renders through a compiler
	// construct rather than a widget and BareStdlibComponents leaves it out,
	// so anything still here is a widget with no body.
	for _, name := range BareStdlibComponents(pkg) {
		t.Errorf("stdlib component %q has no fyne body", name)
	}
	if n := len(OverriddenComponents(pkg)); n < 30 {
		t.Errorf("only %d components carry a fyne override; the walk is not finding them", n)
	}
}

// TestEveryFyneBodyLowersToADeclaredWidget closes the other half: an override
// body is only worth having if what it instantiates carries a Spec the Go
// emitter can build a widget from.
//
// The check runs over the *checked* tree, before inlining, so an override's
// body names a widget component (Label, VBox …) rather than the primitive that
// widget's own body instantiates. Following the chain to a primitive and
// decoding the Spec there is what proves the override reaches something
// buildable — and it is the same walk a user's own widget declaration would be
// followed by, since nothing here knows which of the two it is looking at.
func TestEveryFyneBodyLowersToADeclaredWidget(t *testing.T) {
	pkg := checkAllComponents(t)

	used := map[string]bool{}
	for _, comp := range OverriddenComponents(pkg) {
		prims := fynePrimitiveNodes(comp.PlatformOverrides["fyne"].Stmts)
		if len(prims) == 0 {
			t.Errorf("%s: fyne body reaches no fyne primitive", comp.Name)
			continue
		}
		for _, n := range prims {
			used[fynePrimitive(n.Component)] = true
			if _, err := specFromProps(n.Name, nodeProps(n)); err != nil {
				t.Errorf("%s: %v", comp.Name, err)
			}
		}
	}

	// The reverse: a primitive no override reaches is one nothing tests.
	var unused []string
	for _, id := range []string{"Widget", "Container", "Wrapper"} {
		if !used[id] {
			unused = append(unused, id)
		}
	}
	sort.Strings(unused)
	if len(unused) > 0 {
		t.Errorf("fyne primitives no override reaches: %v", unused)
	}
}

func checkAllComponents(t *testing.T) *ir.Package {
	t.Helper()
	doc, err := parser.Parse("t.sngl", []byte(WidgetProbeSource))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	var plats []ir.Platform
	if p := codegen.LookupPlatform("fyne"); p != nil {
		plats = append(plats, p)
	} else {
		t.Fatal("fyne platform not registered")
	}
	var langs []ir.Language
	if l := codegen.LookupLang("go"); l != nil {
		langs = append(langs, l)
	}
	pkg, diags := checker.Check(doc, &checker.Config{IsMain: true, Platforms: plats, Languages: langs, Targets: []ir.StaticTarget{{Platform: "fyne", Language: "go"}}})
	for _, d := range diags {
		if d.Severity == ir.Error {
			t.Fatalf("%s: %s", d.Pos, d.Msg)
		}
	}
	if pkg == nil || len(pkg.Components) == 0 {
		t.Fatal("checker returned no components")
	}
	return pkg
}
