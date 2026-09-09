package checker

import (
	"testing"

	"git.duckfam.us/jonathan/sngl/internal/parser"
	"git.duckfam.us/jonathan/sngl/ir"
)

// A component method that reads a component var (referenced bare) must be
// analysed against a scope that includes that var: its purity must reflect the
// read (PurityReadonly, not PurityPure) and Reads must list the var. Computing
// these against package-only vars marked such methods Pure with empty Reads,
// which let the optimizer const-fold calls to them and blinded reactivity to
// their state dependency.
func TestComponentMethodSeesComponentVars(t *testing.T) {
	src := `
component main ui {
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
