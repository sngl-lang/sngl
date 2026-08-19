package checker_test

import (
	"testing"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/internal/checker"
	"git.duckfam.us/jonathan/sngl/internal/parser"
	"git.duckfam.us/jonathan/sngl/ir"
)

func noErrors(t *testing.T, diags []ir.Diagnostic) {
	t.Helper()
	for _, d := range diags {
		if d.Severity == ir.Error {
			t.Errorf("unexpected error: %s", d.Error())
		}
	}
}

// TestStdlibListHasTypeParam verifies that list<int> type-checks without errors
// and that the stdlib list StructDef carries TypeParams = ["T"].
func TestStdlibListHasTypeParam(t *testing.T) {
	src := `var xs list<int> = [1, 2, 3]`
	doc, err := parser.Parse("test.sngl", []byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	_, diags := checker.Check(doc, &checker.Config{IsMain: true})
	noErrors(t, diags)

	// Verify that the stdlib AST decl for list has TypeParams.
	listDecl := findStdlibStruct(t, "list")
	if len(listDecl.TypeParams) != 1 || listDecl.TypeParams[0] != "T" {
		t.Errorf("stdlib list TypeParams = %v, want [T]", listDecl.TypeParams)
	}
}

// TestStdlibMapHasTypeParams verifies that map<string, int> type-checks and
// that the stdlib map StructDef carries TypeParams = ["K", "V"].
func TestStdlibMapHasTypeParams(t *testing.T) {
	src := `var m map<string, int> = {a = 1, b = 2}`
	doc, err := parser.Parse("test.sngl", []byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	_, diags := checker.Check(doc, &checker.Config{IsMain: true})
	noErrors(t, diags)

	mapDecl := findStdlibStruct(t, "map")
	if len(mapDecl.TypeParams) != 2 || mapDecl.TypeParams[0] != "K" || mapDecl.TypeParams[1] != "V" {
		t.Errorf("stdlib map TypeParams = %v, want [K V]", mapDecl.TypeParams)
	}
}

// TestUserDefinedGenericStruct verifies that a user-defined generic struct can
// be declared and instantiated with a concrete type argument.
func TestUserDefinedGenericStruct(t *testing.T) {
	src := `
struct Box<T> {
    value T
}
var b Box<int>
`
	doc, err := parser.Parse("test.sngl", []byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	_, diags := checker.Check(doc, &checker.Config{IsMain: true})
	noErrors(t, diags)
}

// TestBuiltinGenericShadowable verifies that a user declaration of a built-in
// generic name (here `list`) shadows the built-in: `list<int>` resolves to the
// user struct, so its declared field is accessible. Under the old hardcoded
// switch this errored (built-in list has no fields).
func TestBuiltinGenericShadowable(t *testing.T) {
	src := `struct list<T> { first T }
var xs list<int>
var y int = xs.first`
	doc, err := parser.Parse("test.sngl", []byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	_, diags := checker.Check(doc, &checker.Config{IsMain: true})
	noErrors(t, diags)
}

// TestUserDefinedGenericStructTwoParams verifies a two-parameter generic struct.
func TestUserDefinedGenericStructTwoParams(t *testing.T) {
	src := `
struct Pair<A, B> {
    first A
    second B
}
var p Pair<int, string>
`
	doc, err := parser.Parse("test.sngl", []byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	_, diags := checker.Check(doc, &checker.Config{IsMain: true})
	noErrors(t, diags)
}

// TestUserDefinedGenericStructMissingArgs verifies that a generic struct used
// without type arguments produces a type error.
func TestUserDefinedGenericStructMissingArgs(t *testing.T) {
	src := `
struct Box<T> {
    value T
}
var b Box
`
	doc, err := parser.Parse("test.sngl", []byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	_, diags := checker.Check(doc, &checker.Config{IsMain: true})
	hasError := false
	for _, d := range diags {
		if d.Severity == ir.Error {
			hasError = true
		}
	}
	if !hasError {
		t.Error("expected an error for Box used without type args, got none")
	}
}

// findStdlibStruct searches the parsed stdlib docs for a StructDef by name.
func findStdlibStruct(t *testing.T, name string) *ast.StructDef {
	t.Helper()
	for _, doc := range checker.StdlibDocs() {
		for _, stmt := range doc.Stmts {
			if sd, ok := stmt.(*ast.StructDef); ok && sd.Name == name {
				return sd
			}
		}
	}
	t.Fatalf("stdlib struct %q not found", name)
	return nil
}
