package checker_test

import (
	"testing"

	"git.duckfam.us/jonathan/sngl/internal/checker"
	"git.duckfam.us/jonathan/sngl/internal/parser"
	"git.duckfam.us/jonathan/sngl/ir"
)

// TestRecvTypeParamSubstitutionInReturn verifies that a method declared with
// RecvTypeParams has its return type substituted with the caller's concrete type.
func TestRecvTypeParamSubstitutionInReturn(t *testing.T) {
	src := `
struct Box<T> {
    value T
}
func Box<T>.identity() Box<T> { }
var b Box<int>
var b2 Box<int> = b.identity()
`
	doc, err := parser.Parse("test.sngl", []byte(src))
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

// TestRecvTypeParamFieldAccess verifies that a field access on the result of a
// generic method call resolves to the concrete (substituted) type.
func TestRecvTypeParamFieldAccess(t *testing.T) {
	src := `
struct Box<T> {
    value T
}
func Box<T>.identity() Box<T> { }
var b Box<int>
var n int = b.identity().value
`
	doc, err := parser.Parse("test.sngl", []byte(src))
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

// TestRecvTypeParamWrongReturnType verifies that assigning the result of a
// generic method to an incompatible type produces a type error.
func TestRecvTypeParamWrongReturnType(t *testing.T) {
	src := `
struct Box<T> {
    value T
}
func Box<T>.identity() Box<T> { }
var b Box<int>
var b2 Box<string> = b.identity()
`
	doc, err := parser.Parse("test.sngl", []byte(src))
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
		t.Error("expected type error: Box<int> result not assignable to Box<string>")
	}
}

// TestRecvTypeParamNonGenericReturn verifies that methods returning a
// non-generic type (e.g. int) work correctly regardless of T.
func TestRecvTypeParamNonGenericReturn(t *testing.T) {
	src := `
struct Box<T> {
    value T
}
func Box<T>.size() int { }
var b Box<string>
var n int = b.size()
`
	doc, err := parser.Parse("test.sngl", []byte(src))
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

// TestRecvTypeParamTwoTypeArgs verifies substitution works for a two-parameter
// generic struct.
func TestRecvTypeParamTwoTypeArgs(t *testing.T) {
	src := `
struct Pair<A, B> {
    first A
    second B
}
func Pair<A, B>.swap() Pair<B, A> { }
var p Pair<int, string>
var q Pair<string, int> = p.swap()
`
	doc, err := parser.Parse("test.sngl", []byte(src))
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

// TestListMapSameType verifies that list<T>.map<U> infers U=T when the lambda
// returns the same type as the input.
func TestListMapSameType(t *testing.T) {
	src := `struct list<T> {}
func list<T>.map<U>(f func(T) U) list<U> {}
var xs list<int> = [1, 2, 3]
var ys list<int> = xs.map(func(x int) => x * 2)`
	doc, err := parser.Parse("test.sngl", []byte(src))
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

// TestListMapDifferentType verifies that list<T>.map<U> correctly infers U
// when the lambda returns a different type than the input.
func TestListMapDifferentType(t *testing.T) {
	src := `struct list<T> {}
func list<T>.map<U>(f func(T) U) list<U> {}
var xs list<int> = [1, 2, 3]
var ys list<string> = xs.map(func(x int) => "{x}")`
	doc, err := parser.Parse("test.sngl", []byte(src))
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

// TestListMapTypeMismatch verifies that list<T>.map<U> produces a type error
// when the inferred result type doesn't match the declared variable type.
func TestListMapTypeMismatch(t *testing.T) {
	src := `struct list<T> {}
func list<T>.map<U>(f func(T) U) list<U> {}
var xs list<int> = [1, 2, 3]
var ys list<int> = xs.map(func(x int) => "{x}")`
	doc, err := parser.Parse("test.sngl", []byte(src))
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
		t.Error("expected error: list<string> not assignable to list<int>")
	}
}

// TestRecvTypeParamFieldDirectAccess verifies that accessing a field directly
// on a generic struct instance resolves to the concrete field type.
func TestRecvTypeParamFieldDirectAccess(t *testing.T) {
	src := `
struct Box<T> {
    value T
}
var b Box<int>
var n int = b.value
`
	doc, err := parser.Parse("test.sngl", []byte(src))
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
