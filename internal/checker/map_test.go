package checker_test

import (
	"testing"

	"git.duckfam.us/jonathan/sngl/internal/checker"
	"git.duckfam.us/jonathan/sngl/internal/parser"
	"git.duckfam.us/jonathan/sngl/ir"
)

func TestMapTypeResolves(t *testing.T) {
	src := `var m map<string, int>`
	doc, err := parser.Parse("test.sngl", []byte(withStd(src)))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	_, diags := checker.Check(doc, &checker.Config{IsMain: true})
	for _, d := range diags {
		if d.Severity == ir.Error {
			t.Errorf("unexpected error: %s", d.Error())
		}
	}
}

func TestMapWrongArity(t *testing.T) {
	src := `var m map<string>`
	doc, err := parser.Parse("test.sngl", []byte(withStd(src)))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	_, diags := checker.Check(doc, &checker.Config{IsMain: true})
	found := false
	for _, d := range diags {
		if d.Severity == ir.Error {
			found = true
		}
	}
	if !found {
		t.Error("expected error for map<string> (1 type arg)")
	}
}

func TestMapIncomparableKey(t *testing.T) {
	src := `var m map<list<int>, string>`
	doc, err := parser.Parse("test.sngl", []byte(withStd(src)))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	_, diags := checker.Check(doc, &checker.Config{IsMain: true})
	found := false
	for _, d := range diags {
		if d.Severity == ir.Error {
			found = true
		}
	}
	if !found {
		t.Error("expected error for map<list<int>, string> (list key not comparable)")
	}
}

func TestMapLiteralInfersTypes(t *testing.T) {
	// String-literal keys → MapLit at parse; expected type provided via annotation.
	src := `var m map<string, int> = {"a" = 1, "b" = 2}`
	doc, err := parser.Parse("test.sngl", []byte(withStd(src)))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	_, diags := checker.Check(doc, &checker.Config{IsMain: true})
	for _, d := range diags {
		if d.Severity == ir.Error {
			t.Errorf("unexpected error: %s", d.Error())
		}
	}
}

func TestMapIndexReturnsValueType(t *testing.T) {
	src := `var m map<string, int> = {"a" = 1, "b" = 2}; var x int = m["a"]`
	doc, err := parser.Parse("test.sngl", []byte(withStd(src)))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	_, diags := checker.Check(doc, &checker.Config{IsMain: true})
	for _, d := range diags {
		if d.Severity == ir.Error {
			t.Errorf("unexpected: %s", d.Error())
		}
	}
}

func TestMapIndexWrongKeyTypeError(t *testing.T) {
	src := `var m map<string, int> = {"a" = 1}; var x = m[42]`
	doc, err := parser.Parse("test.sngl", []byte(withStd(src)))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	_, diags := checker.Check(doc, &checker.Config{IsMain: true})
	found := false
	for _, d := range diags {
		if d.Severity == ir.Error {
			found = true
		}
	}
	if !found {
		t.Error("expected error for int key on string-keyed map")
	}
}

func TestMapLiteralMixedKeyTypesError(t *testing.T) {
	// Mixed key types: "a" (string) and 2 (int) — both non-ident → MapLit.
	// No expected type context → error (no expected type for map literal).
	src := `var m = {"a" = 1, 2 = 3}`
	doc, err := parser.Parse("test.sngl", []byte(withStd(src)))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	_, diags := checker.Check(doc, &checker.Config{IsMain: true})
	found := false
	for _, d := range diags {
		if d.Severity == ir.Error {
			found = true
		}
	}
	if !found {
		t.Error("expected error for mixed key types (or missing expected type)")
	}
}

func TestMapLiteralRequiresExpectedType(t *testing.T) {
	// With map type annotation: ok.
	src := `var m map<string, int> = {a = 1, b = 2}`
	doc, err := parser.Parse("t.sngl", []byte(withStd(src)))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	_, diags := checker.Check(doc, &checker.Config{IsMain: true})
	for _, d := range diags {
		if d.Severity == ir.Error {
			t.Errorf("unexpected: %s", d.Error())
		}
	}
}

func TestMapLiteralWithoutContextErrors(t *testing.T) {
	src := `var m = {1 = "one"}`
	doc, err := parser.Parse("t.sngl", []byte(withStd(src)))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	_, diags := checker.Check(doc, &checker.Config{IsMain: true})
	found := false
	for _, d := range diags {
		if d.Severity == ir.Error {
			found = true
		}
	}
	if !found {
		t.Error("expected error: map literal without expected type")
	}
}

func TestStructLiteralReinterpretedAsMap(t *testing.T) {
	// Ident keys + string-K map → ident names become string keys.
	src := `var m map<string, int> = {apple = 1, pear = 2}`
	doc, err := parser.Parse("t.sngl", []byte(withStd(src)))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	_, diags := checker.Check(doc, &checker.Config{IsMain: true})
	for _, d := range diags {
		if d.Severity == ir.Error {
			t.Errorf("unexpected: %s", d.Error())
		}
	}
}

func TestStructLiteralIntoNonStringMapErrors(t *testing.T) {
	// Ident keys can't satisfy map<int, V>.
	src := `var m map<int, string> = {a = "x"}`
	doc, err := parser.Parse("t.sngl", []byte(withStd(src)))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	_, diags := checker.Check(doc, &checker.Config{IsMain: true})
	found := false
	for _, d := range diags {
		if d.Severity == ir.Error {
			found = true
		}
	}
	if !found {
		t.Error("expected error: ident-keyed literal into non-string-keyed map")
	}
}

func TestMapIntKeysWithExpectedType(t *testing.T) {
	src := `var m map<int, string> = {1 = "one", 2 = "two"}`
	doc, err := parser.Parse("t.sngl", []byte(withStd(src)))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	_, diags := checker.Check(doc, &checker.Config{IsMain: true})
	for _, d := range diags {
		if d.Severity == ir.Error {
			t.Errorf("unexpected: %s", d.Error())
		}
	}
}

func TestMapLengthMethod(t *testing.T) {
	src := `var m map<string, int> = {a = 1}; var n = m.length()`
	doc, err := parser.Parse("test.sngl", []byte(withStd(src)))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	_, diags := checker.Check(doc, &checker.Config{IsMain: true})
	for _, d := range diags {
		if d.Severity == ir.Error {
			t.Errorf("unexpected: %s", d.Error())
		}
	}
}
