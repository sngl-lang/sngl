package checker

import (
	"testing"

	"git.duckfam.us/jonathan/sngl/internal/parser"
	"git.duckfam.us/jonathan/sngl/ir"
)

func checkNoErrors(t *testing.T, src string) *ir.Package {
	t.Helper()
	doc, err := parser.Parse("test.sngl", []byte(withStd(src)))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	pkg, diags := Check(doc, &Config{IsMain: true})
	for _, d := range diags {
		if d.Severity == ir.Error {
			t.Errorf("unexpected error diag: %s", d.Msg)
		}
	}
	return pkg
}

// A struct field may reference a type declared later in the file (forward
// reference). buildStructDef resolved field types before the referent's shell
// was registered, so this failed with `unknown type`.
func TestStructForwardReference(t *testing.T) {
	checkNoErrors(t, `
struct Tree { root option<Branch> }
struct Branch { value int }
`)
}

// Mutually recursive struct types (through option<> indirection) must check
// regardless of declaration order.
func TestStructMutualRecursion(t *testing.T) {
	checkNoErrors(t, `
struct A { b option<B> }
struct B { a option<A> }
`)
}

// A self-recursive struct (through option<> indirection) must check even
// though the type is not yet registered while its own fields resolve.
func TestStructSelfRecursion(t *testing.T) {
	checkNoErrors(t, `
struct Node { next option<Node> }
`)
}

// A const initializer may use a bare enum member of its declared enum type.
func TestConstBareEnumMember(t *testing.T) {
	checkNoErrors(t, `
enum Color { red, green, blue }
const c Color = red
`)
}

// A const may reference another const declared later in the file.
func TestConstForwardReference(t *testing.T) {
	checkNoErrors(t, `
const a = b
const b = 5
`)
}

// A top-level var (or const) that bare-references an un-annotated const must
// infer that const's concrete type, not dyn. Regression: deferring all const
// value checks to the end of pass1 left the const shell typed dyn while
// registerVars ran, so `var count = MAX` inferred dyn.
func TestConstTypePropagatesToVar(t *testing.T) {
	pkg := checkNoErrors(t, `
const MAX = 10
var count = MAX
component main node { text(value="{count}") }
`)
	var v *ir.Var
	for _, x := range pkg.Vars {
		if x.Name == "count" {
			v = x
		}
	}
	if v == nil {
		t.Fatal("var count not found")
	}
	if v.Type == nil || v.Type.Kind != ir.TypeInt {
		t.Fatalf("count type = %v, want int (regression: dyn)", v.Type)
	}
}
