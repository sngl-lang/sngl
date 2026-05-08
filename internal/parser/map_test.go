package parser_test

import (
	"testing"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/internal/parser"
)

func TestParseMapLiteral(t *testing.T) {
	src := `var m = {"one": 1, "two": 2}`
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
	// Verify keys and values.
	key0, ok0 := ml.Entries[0].Key.(*ast.LiteralExpr)
	if !ok0 || key0.Raw != "one" {
		t.Errorf("entry[0].Key: got %T %v, want LiteralExpr{Raw:\"one\"}", ml.Entries[0].Key, ml.Entries[0].Key)
	}
	val0, ok0v := ml.Entries[0].Value.(*ast.LiteralExpr)
	if !ok0v || val0.Raw != "1" {
		t.Errorf("entry[0].Value: got %T %v, want LiteralExpr{Raw:\"1\"}", ml.Entries[0].Value, ml.Entries[0].Value)
	}
}

func TestParseMapLiteralIntKeys(t *testing.T) {
	src := `var m = {1: "one", 2: "two"}`
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

func TestParseEmptyMapVsEmptyStruct(t *testing.T) {
	// An empty {} should produce an empty StructExpr (no colon seen).
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

func TestParseStructLiteralStillWorks(t *testing.T) {
	src := `var c = color{r=255, g=0, b=0}`
	_, err := parser.Parse("test.sngl", []byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
}

func TestParseMapLiteralFormatRoundtrip(t *testing.T) {
	src := `var m = {"one": 1, "two": 2}`
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
