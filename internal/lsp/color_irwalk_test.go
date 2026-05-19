package lsp

import (
	"os"
	"path/filepath"
	"testing"

	"git.duckfam.us/jonathan/sngl"
	"git.duckfam.us/jonathan/sngl/internal/parser"
	"git.duckfam.us/jonathan/sngl/ir"
)

func TestWalkIRColorLiterals_FixtureCovers6Colors(t *testing.T) {
	src, err := os.ReadFile("../../testdata/lsp/colors.sngl")
	if err != nil {
		t.Fatal(err)
	}
	doc, perr := parser.Parse("colors.sngl", src)
	if perr != nil {
		t.Fatalf("parse: %v", perr)
	}
	dir, err := filepath.Abs("../../testdata/lsp")
	if err != nil {
		t.Fatal(err)
	}
	pkg, diags := sngl.Check(doc, dir)
	for _, d := range diags {
		if d.Severity == ir.Error {
			t.Fatalf("check: %s", d.Error())
		}
	}
	// Don't run Optimize here — the const folder doesn't yet fold color
	// calls (tracked in issue #76). Just verify the walker visits the
	// source-form #hex literals that the checker produces directly.

	var colors []*ir.StructLit
	walkIRColorLiterals(pkg, func(sl *ir.StructLit) {
		colors = append(colors, sl)
	})

	// Source has 4 #hex literals; post-T3 they're StructLits with
	// Def.Name == "color". The two color.rgb/rgba calls remain as
	// *ir.Call (not folded yet — that's T7).
	if len(colors) != 4 {
		t.Fatalf("got %d color literals, want 4: %+v", len(colors), colors)
	}
	for _, c := range colors {
		if c.AST == nil {
			t.Errorf("color StructLit has nil AST: %+v", c)
		}
		if c.Def == nil || c.Def.Name != "color" {
			t.Errorf("Def = %v, want color", c.Def)
		}
	}
}
