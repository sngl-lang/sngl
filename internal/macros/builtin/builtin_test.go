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
	// No import: the #[builtin] macro resolves ambiently.
	src := `import . "sngl://internal/builtin"

#[builtin("color")]
struct color { r int = 0 }`
	stmt, diags := expandOne(t, src)
	if hasError(diags) {
		t.Fatalf("unexpected diagnostic: %s", firstError(diags))
	}
	sd, ok := stmt.(*ast.StructDef)
	if !ok {
		t.Fatalf("expected *ast.StructDef, got %T", stmt)
	}
	if sd.Builtin != ast.BuiltinColor {
		t.Errorf("Builtin = %q, want %q", sd.Builtin, ast.BuiltinColor)
	}
}

func TestPrimitiveMacro(t *testing.T) {
	src := `import . "sngl://internal/builtin"

#[builtin("int")]
struct int {}`
	stmt, diags := expandOne(t, src)
	if hasError(diags) {
		t.Fatalf("unexpected diagnostic: %s", firstError(diags))
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
	src := `import . "sngl://internal/builtin"

#[builtin("list")]
struct list<T> {}`
	stmt, diags := expandOne(t, src)
	if hasError(diags) {
		t.Fatalf("unexpected diagnostic: %s", firstError(diags))
	}
	sd, ok := stmt.(*ast.StructDef)
	if !ok {
		t.Fatalf("expected *ast.StructDef, got %T", stmt)
	}
	if sd.Builtin != ast.BuiltinList {
		t.Errorf("Builtin = %q, want %q", sd.Builtin, ast.BuiltinList)
	}
}

func TestNodeMacro(t *testing.T) {
	src := `import . "sngl://internal/builtin"

#[builtin("window")]
component window(title string) list<component> {}`
	stmt, diags := expandOne(t, src)
	if hasError(diags) {
		t.Fatalf("unexpected diagnostic: %s", firstError(diags))
	}
	comp, ok := stmt.(*ast.ComponentDecl)
	if !ok {
		t.Fatalf("expected *ast.ComponentDecl, got %T", stmt)
	}
	if comp.Builtin != ast.BuiltinWindow {
		t.Errorf("Builtin = %q, want %q", comp.Builtin, ast.BuiltinWindow)
	}
}

func TestMacroRejectsUnknownKind(t *testing.T) {
	src := `import . "sngl://internal/builtin"

#[builtin("bogus")]
struct x {}`
	_, diags := expandOne(t, src)
	if !hasError(diags) {
		t.Errorf("expected an error diagnostic for an unknown builtin kind")
	}
}

// A type kind annotates a struct; a component is a category error.
func TestMacroRejectsTypeKindOnComponent(t *testing.T) {
	src := `import . "sngl://internal/builtin"

#[builtin("color")]
component foo {}`
	_, diags := expandOne(t, src)
	if !hasError(diags) {
		t.Errorf("expected an error diagnostic for a type kind on a component")
	}
}

// A node kind annotates a component; a struct is a category error.
func TestMacroRejectsNodeKindOnStruct(t *testing.T) {
	src := `import . "sngl://internal/builtin"

#[builtin("window")]
struct window {}`
	_, diags := expandOne(t, src)
	if !hasError(diags) {
		t.Errorf("expected an error diagnostic for a node kind on a struct")
	}
}

func TestMacroRejectsBareIdent(t *testing.T) {
	// A bare identifier is a name reference, not a constant — must be rejected
	// in favor of a string literal.
	src := `import . "sngl://internal/builtin"

#[builtin(list)]
struct list<T> {}`
	_, diags := expandOne(t, src)
	if !hasError(diags) {
		t.Errorf("expected an error diagnostic for a bare-ident macro argument")
	}
}

func hasError(diags []ir.Diagnostic) bool {
	for _, d := range diags {
		if d.Severity == ir.Error {
			return true
		}
	}
	return false
}

func firstError(diags []ir.Diagnostic) string {
	for _, d := range diags {
		if d.Severity == ir.Error {
			return d.Msg
		}
	}
	return ""
}
