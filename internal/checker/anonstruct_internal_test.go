package checker

import (
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/ir"
)

// A program's own declarations record no package, so two packages of one
// program may each declare `Style` -- the shape a directory import takes, and
// what the txtar covers end to end. The intern signature has to tell them
// apart, or the two anonymous structs holding them collapse into one
// declaration and whichever field type was seen first wins.
func TestAnonSignatureSeparatesSameNamedPackagelessDecls(t *testing.T) {
	a := &ir.StructDef{Name: "Style", Fields: []*ir.StructField{{Name: "x", Type: TypInt}}}
	b := &ir.StructDef{Name: "Style", Fields: []*ir.StructField{{Name: "y", Type: TypInt}}}

	fieldsOf := func(sd *ir.StructDef) []*ir.StructField {
		return []*ir.StructField{{Name: "s", Type: sd.SymType()}}
	}
	sigA, keyA := anonSignature(fieldsOf(a))
	sigB, keyB := anonSignature(fieldsOf(b))

	if keyA == keyB {
		t.Errorf("intern keys collide for two Style declarations: %q", keyA)
	}
	// The generated name is hashed from the stable spelling, so that one must
	// carry no address. Equal here is the point: it is why a name can collide
	// and why anonStructName counts.
	if sigA != sigB {
		t.Errorf("stable signatures differ (%q vs %q); the generated name would depend on an address", sigA, sigB)
	}
	if strings.Contains(sigA, "@0x") {
		t.Errorf("stable signature carries an address: %q", sigA)
	}
}

// The two therefore spell one generated name, and the second must not be a
// second `type` of that name in the output.
func TestInternAnonStructUniquifiesAName(t *testing.T) {
	a := &ir.StructDef{Name: "Style", Fields: []*ir.StructField{{Name: "x", Type: TypInt}}}
	b := &ir.StructDef{Name: "Style", Fields: []*ir.StructField{{Name: "y", Type: TypInt}}}
	c := &checker{pkg: &ir.Package{}}

	first := c.internAnonStruct([]*ir.StructField{{Name: "s", Type: a.SymType()}})
	second := c.internAnonStruct([]*ir.StructField{{Name: "s", Type: b.SymType()}})

	if first == second {
		t.Fatal("two Style declarations interned to one anonymous struct")
	}
	if first.Name == second.Name {
		t.Errorf("both interned declarations are named %q", first.Name)
	}
	if again := c.internAnonStruct([]*ir.StructField{{Name: "s", Type: a.SymType()}}); again != first {
		t.Error("the same field set interned twice")
	}
	if got := len(c.pkg.Structs); got != 2 {
		t.Errorf("registered %d declarations, want 2", got)
	}
}

// A library declaration is told apart by its package, with no address in
// either spelling.
func TestAnonSignatureSeparatesByPackage(t *testing.T) {
	mine := &ir.StructDef{Name: "Style"}
	theirs := &ir.StructDef{Name: "Style", Pkg: "sngl:ui"}
	fieldsOf := func(sd *ir.StructDef) []*ir.StructField {
		return []*ir.StructField{{Name: "s", Type: sd.SymType()}}
	}
	sigMine, _ := anonSignature(fieldsOf(mine))
	sigTheirs, _ := anonSignature(fieldsOf(theirs))
	if sigMine == sigTheirs {
		t.Errorf("stable signatures collide across packages: %q", sigMine)
	}
}
