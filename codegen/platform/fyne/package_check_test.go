package fyne

import (
	"testing"

	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/internal/checker"
	"git.duckfam.us/jonathan/sngl/internal/parser"
	"git.duckfam.us/jonathan/sngl/ir"
)

// lib/platform/fyne/fyne.sngl declares twelve intrinsics and thirty-four
// overrides written against them, and no Go code in this package parses that
// file. Importing the package is what loads and checks it, so this is the only
// thing that keeps it type-correct.
func TestFynePackageTypeChecks(t *testing.T) {
	src := `
import . "sngl:ui"
import "sngl:platform/fyne"
output { go { fyne() } }
window {
    text(value="hi")
}
`
	doc, err := parser.Parse("t.sngl", []byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	var plats []ir.Platform
	if p := codegen.LookupPlatform("fyne"); p != nil {
		plats = append(plats, p)
	}
	var langs []ir.Language
	if l := codegen.LookupLang("go"); l != nil {
		langs = append(langs, l)
	}
	_, diags := checker.Check(doc, &checker.Config{IsMain: true, Platforms: plats, Languages: langs, Targets: []ir.StaticTarget{{Platform: "fyne", Language: "go"}}})
	for _, d := range diags {
		if d.Severity == ir.Error {
			t.Errorf("sngl:platform/fyne: %s: %s", d.Pos, d.Msg)
		}
	}
}
