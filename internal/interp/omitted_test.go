package interp

import "testing"

// TestUnwrittenAsksTheValue holds an omitted field to what it holds rather
// than to its mark: a write through a nested field changes the struct the
// field holds in place, without passing through the outer struct's Set, and
// the field is written from then on.
func TestUnwrittenAsksTheValue(t *testing.T) {
	inner := &Struct{Fields: []Field{{Name: "x", Value: 0, Omitted: true, Filled: 0}}}
	outer := &Struct{Fields: []Field{{Name: "inner", Value: inner, Omitted: true, Filled: DeepCopy(inner)}}}
	if !outer.Fields[0].Unwritten() {
		t.Fatal("a field as its literal's omission filled it reads as written")
	}
	inner.Set("x", 1)
	if outer.Fields[0].Unwritten() {
		t.Error("a field written through a nested select still reads as left out")
	}
	if inner.Fields[0].Unwritten() {
		t.Error("a field Set wrote still reads as left out")
	}
}

// TestMergeKeepsOmissionWithinOneDeclaration hands a spread's omissions on
// only between structs of one declaration, whose omission means the same
// default.
func TestMergeKeepsOmissionWithinOneDeclaration(t *testing.T) {
	src := &Struct{Fields: []Field{{Name: "n", Value: 0, Omitted: true, Filled: 0}}}
	other := &Struct{}
	other.Merge(src)
	if other.Fields[0].Unwritten() {
		t.Error("an omission crossed into a struct of no declaration of its own")
	}
}
