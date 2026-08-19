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
	// No import: #[builtin.*] macros resolve ambiently.
	src := `#[builtin.stringrepr("color")]
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
	if sd.Builtin != ast.BuiltinColor {
		t.Errorf("Builtin = %q, want %q", sd.Builtin, ast.BuiltinColor)
	}
}

func TestStringReprMacroRejectsUnknownKind(t *testing.T) {
	src := `#[builtin.stringrepr("bogus")]
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
	src := `#[builtin.stringrepr("color")]
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

func TestPrimitiveMacro(t *testing.T) {
	src := `#[builtin.primitive("int")]
struct int {}`
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
	if sd.Builtin != ast.BuiltinInt {
		t.Errorf("Builtin = %q, want %q", sd.Builtin, ast.BuiltinInt)
	}
}

func TestGenericMacro(t *testing.T) {
	src := `#[builtin.generic("list")]
struct list<T> {}`
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
	if sd.Builtin != ast.BuiltinList {
		t.Errorf("Builtin = %q, want %q", sd.Builtin, ast.BuiltinList)
	}
}

func TestGenericMacroRejectsBareIdent(t *testing.T) {
	// A bare identifier is a name reference, not a constant — must be rejected
	// in favor of a string literal.
	src := `#[builtin.generic(list)]
struct list<T> {}`
	_, diags := expandOne(t, src)
	found := false
	for _, d := range diags {
		if d.Severity == ir.Error {
			found = true
		}
	}
	if !found {
		t.Errorf("expected an error diagnostic for a bare-ident macro argument")
	}
}
