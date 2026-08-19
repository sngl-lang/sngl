package builtin

import (
	"testing"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/internal/expand"
	"git.duckfam.us/jonathan/sngl/internal/parser"
	"git.duckfam.us/jonathan/sngl/ir"
)

// expandOne parses src, runs pre-check macro expansion, and returns the first
// top-level statement plus any diagnostics.
func expandOne(t *testing.T, src string) (ast.Stmt, []ir.Diagnostic) {
	t.Helper()
	doc, err := parser.Parse("t.sngl", []byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	diags := expand.ExpandPre([]*ast.Document{doc})
	if len(doc.Stmts) == 0 {
		t.Fatalf("no statements after expand")
	}
	return doc.Stmts[len(doc.Stmts)-1], diags
}

func TestStringReprMacro(t *testing.T) {
	src := `import "internal://builtin"
#[builtin.stringrepr(color)]
struct color { r int = 0 }`
	stmt, diags := expandOne(t, src)
	for _, d := range diags {
		if d.Severity == ir.Error {
			t.Fatalf("unexpected diagnostic: %s", d.Msg)
		}
	}
	sd, ok := stmt.(*ast.StructDef)
	if !ok {
		t.Fatalf("expected *ast.StructDef, got %T", stmt)
	}
	if sd.StringRepr != "color" {
		t.Errorf("StringRepr = %q, want %q", sd.StringRepr, "color")
	}
}

func TestStringReprMacroRejectsUnknownKind(t *testing.T) {
	src := `import "internal://builtin"
#[builtin.stringrepr(bogus)]
struct x {}`
	_, diags := expandOne(t, src)
	found := false
	for _, d := range diags {
		if d.Severity == ir.Error {
			found = true
		}
	}
	if !found {
		t.Errorf("expected an error diagnostic for unknown stringrepr kind")
	}
}

func TestStringReprMacroRejectsNonStruct(t *testing.T) {
	src := `import "internal://builtin"
#[builtin.stringrepr(color)]
component foo {}`
	_, diags := expandOne(t, src)
	found := false
	for _, d := range diags {
		if d.Severity == ir.Error {
			found = true
		}
	}
	if !found {
		t.Errorf("expected an error diagnostic when stringrepr targets a non-struct")
	}
}
