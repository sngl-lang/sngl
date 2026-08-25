package optimize

import (
	"testing"

	"git.duckfam.us/jonathan/sngl/internal/checker"
	"git.duckfam.us/jonathan/sngl/internal/parser"
	"git.duckfam.us/jonathan/sngl/ir"
)

// TestOptimize_EnumMemberKeepsIdent is the other half of
// testdata/optimize_enum_fold.sngl. Folding an enum member is what lets a
// comparison over one decide a branch, but the member has no literal form:
// it evaluates to its name, and rebuilding that as a string literal would
// hand codegen a string where the enum type is required. So a member left in
// value position must come out of the optimizer as the Ident it went in as.
func TestOptimize_EnumMemberKeepsIdent(t *testing.T) {
	const src = `import . "sngl://std"

enum Color { red, green, blue }

const picked = Color.green

component keeps {
    var current Color = picked
    text(value="x")
}
`
	doc, err := parser.Parse("keeps.sngl", []byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	pkg, diags := checker.Check(doc, &checker.Config{IsMain: true})
	for _, d := range diags {
		if d.Severity == ir.Error {
			t.Fatalf("check: %s", d.Error())
		}
	}
	if err := Optimize(pkg, &Config{Platform: "html", Language: "js"}); err != nil {
		t.Fatalf("optimize: %v", err)
	}

	var current *ir.Var
	for _, c := range pkg.Components {
		for _, v := range c.Vars {
			if v.Name == "current" {
				current = v
			}
		}
	}
	if current == nil {
		t.Fatal("var current not found after optimization")
	}
	if lit, ok := current.Init.(*ir.Literal); ok {
		t.Fatalf("enum member folded to a %s literal %q; it must stay an Ident",
			lit.Type, lit.Raw)
	}
	if _, ok := current.Init.(*ir.Ident); !ok {
		t.Fatalf("want *ir.Ident, got %T", current.Init)
	}

	// The const the var refers to holds the member itself, so that is where a
	// string-literal rewrite would show up.
	var picked *ir.Var
	for _, c := range pkg.Consts {
		if c.Name == "picked" {
			picked = c
		}
	}
	if picked == nil {
		t.Fatal("const picked not found after optimization")
	}
	if lit, ok := picked.Init.(*ir.Literal); ok {
		t.Fatalf("enum member folded to a %s literal %q; it must stay an Ident",
			lit.Type, lit.Raw)
	}
	id, ok := picked.Init.(*ir.Ident)
	if !ok {
		t.Fatalf("const picked: want *ir.Ident, got %T", picked.Init)
	}
	if id.Member != "green" {
		t.Errorf("Member = %q, want %q", id.Member, "green")
	}
	if id.Type == nil || id.Type.Kind != ir.TypeEnum {
		t.Errorf("Type = %v, want an enum type", id.Type)
	}
}
