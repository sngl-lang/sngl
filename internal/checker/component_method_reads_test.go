package checker

import (
	"testing"

	"duckfam.us/sngl/internal/parser"
	"duckfam.us/sngl/ir"
)

// A component method that reads a component var (referenced bare) must be
// analysed against a scope that includes that var: its purity must reflect the
// read (PurityReadonly, not PurityPure) and Reads must list the var. Computing
// these against package-only vars marked such methods Pure with empty Reads,
// which let the optimizer const-fold calls to them and blinded reactivity to
// their state dependency.
func TestComponentMethodSeesComponentVars(t *testing.T) {
	src := `
component main node {
    var name = "world"
    func isLong() => name.length > 3
    text(value="{isLong}")
}
`
	doc, err := parser.Parse("test.sngl", []byte(withStd(src)))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	pkg, diags := Check(doc, &Config{IsMain: true})
	for _, d := range diags {
		if d.Severity == ir.Error {
			t.Fatalf("unexpected diag: %s", d.Msg)
		}
	}
	var comp *ir.Component
	for _, c := range pkg.Components {
		if c.Name == "main" {
			comp = c
		}
	}
	if comp == nil {
		t.Fatal("no main component")
	}
	var isLong *ir.Func
	for _, f := range comp.Funcs {
		if f.Name == "isLong" {
			isLong = f
		}
	}
	if isLong == nil {
		t.Fatal("no isLong func")
	}
	if isLong.Purity == ir.PurityPure {
		t.Errorf("isLong reads component var `name` but was typed PurityPure (would be const-foldable)")
	}
	var readsName bool
	for _, v := range isLong.Reads {
		if v.Name == "name" {
			readsName = true
		}
	}
	if !readsName {
		t.Errorf("isLong.Reads does not include `name`; got %v", isLong.Reads)
	}
}

// A `var` written in a block of a component's view is that component's state
// until passHoistState moves it there, as one in the package body's view is
// the package's: a method writing it is impure and writes it. Analysed against
// the component's top-level vars alone, it came out pure, which is
// const-foldable and settles nothing.
func TestComponentMethodSeesViewBlockVars(t *testing.T) {
	src := `
component counter node {
    vbox {
        var n = 0
        func bump() {
            n = n + 1
        }
        button(text="+", @click {
            bump()
        })
    }
}
`
	doc, err := parser.Parse("test.sngl", []byte(withStd(src)))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	pkg, diags := Check(doc, &Config{IsMain: true})
	for _, d := range diags {
		if d.Severity == ir.Error {
			t.Fatalf("unexpected diag: %s", d.Msg)
		}
	}
	var bump *ir.Func
	for _, c := range pkg.Components {
		for _, f := range c.Funcs {
			if c.Name == "counter" && f.Name == "bump" {
				bump = f
			}
		}
	}
	if bump == nil {
		t.Fatal("no bump func")
	}
	if bump.Purity == ir.PurityPure {
		t.Errorf("bump writes the view block's `n` but was typed PurityPure")
	}
	if len(bump.Writes) == 0 {
		t.Errorf("bump writes nothing, want n")
	}
}
