package parser_test

import (
	"testing"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/internal/parser"
)

func TestParseMapLiteralNonIdentKeys(t *testing.T) {
	// Non-ident keys force MapLit at parse time.
	src := `var m = {1 = "one", 2 = "two"}`
	doc, err := parser.Parse("test.sngl", []byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	vd, ok := doc.Stmts[0].(*ast.VarDecl)
	if !ok {
		t.Fatalf("expected VarDecl, got %T", doc.Stmts[0])
	}
	ml, ok := vd.Specs[0].Default.(*ast.MapLit)
	if !ok {
		t.Fatalf("expected MapLit, got %T", vd.Specs[0].Default)
	}
	if len(ml.Entries) != 2 {
		t.Errorf("entries: got %d, want 2", len(ml.Entries))
	}
}

func TestParseStructLiteralStillWorks(t *testing.T) {
	src := `var c = color{r=255, g=0, b=0}`
	_, err := parser.Parse("test.sngl", []byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
}

func TestParseIdentKeyedLiteralIsStructExpr(t *testing.T) {
	// All-ident keys → StructExpr at parse time (checker will disambiguate).
	src := `var m = {apple = 1, pear = 2}`
	doc, err := parser.Parse("test.sngl", []byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	vd, ok := doc.Stmts[0].(*ast.VarDecl)
	if !ok {
		t.Fatalf("expected VarDecl, got %T", doc.Stmts[0])
	}
	_, ok = vd.Specs[0].Default.(*ast.StructExpr)
	if !ok {
		t.Fatalf("expected StructExpr for ident-keyed literal, got %T", vd.Specs[0].Default)
	}
}

func TestParseEmptyLiteralIsStructExpr(t *testing.T) {
	// Empty {} → StructExpr (checker reinterprets if expected type is map).
	src := `var s = {}`
	doc, err := parser.Parse("test.sngl", []byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	vd, ok := doc.Stmts[0].(*ast.VarDecl)
	if !ok {
		t.Fatalf("expected VarDecl, got %T", doc.Stmts[0])
	}
	_, ok = vd.Specs[0].Default.(*ast.StructExpr)
	if !ok {
		t.Fatalf("expected StructExpr for empty {}, got %T", vd.Specs[0].Default)
	}
}

func TestParseMapLiteralStringKeyIsMapLit(t *testing.T) {
	// String-literal keys are non-ident → MapLit at parse time.
	src := `var m = {"one" = 1, "two" = 2}`
	doc, err := parser.Parse("test.sngl", []byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	vd, ok := doc.Stmts[0].(*ast.VarDecl)
	if !ok {
		t.Fatalf("expected VarDecl, got %T", doc.Stmts[0])
	}
	ml, ok := vd.Specs[0].Default.(*ast.MapLit)
	if !ok {
		t.Fatalf("expected MapLit for string-keyed literal, got %T", vd.Specs[0].Default)
	}
	if len(ml.Entries) != 2 {
		t.Errorf("entries: got %d, want 2", len(ml.Entries))
	}
}

func TestParseMapLiteralFormatRoundtrip(t *testing.T) {
	// Non-ident keys → MapLit; formatter emits "="; re-parse also gives MapLit.
	src := `var m = {"one" = 1, "two" = 2}`
	doc, err := parser.Parse("test.sngl", []byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	formatted := parser.Format(doc)
	doc2, err2 := parser.Parse("test.sngl", []byte(formatted))
	if err2 != nil {
		t.Fatalf("re-parse formatted: %v\nformatted: %s", err2, formatted)
	}
	_, ok := doc2.Stmts[0].(*ast.VarDecl)
	if !ok {
		t.Fatalf("expected VarDecl after round-trip, got %T", doc2.Stmts[0])
	}
}
