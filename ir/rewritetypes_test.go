package ir

import "testing"

// A library package other than the one rewritten is shared by every build: a
// namespace naming it leads to it, and nothing in it is rewritten, though a
// target's is not frozen and no Frozen flag guards what it holds. A scheme's
// package, which the build makes, is rewritten with the program.
func TestRewriteTypesLeavesOtherPackages(t *testing.T) {
	from, to := &Type{Kind: TypeInt}, &Type{Kind: TypeFloat}
	slot := &SlotDecl{Name: "src", Content: from}
	lib := &Package{Path: "sngl:language/lib", Components: []*Component{{Slots: []*SlotDecl{slot}}}}
	built := &Var{Type: from}
	scheme := &Package{Path: "openapi:api", Vars: []*Var{built}}
	own := &Var{Type: from}
	prog := &Package{
		Vars: []*Var{own},
		Body: []Stmt{
			&CallStmt{Call: &Call{Receiver: &Ident{Name: "lib", Sym: &Namespace{Name: "lib", Pkg: lib}}}},
			&CallStmt{Call: &Call{Receiver: &Ident{Name: "api", Sym: &Namespace{Name: "api", Pkg: scheme}}}},
		},
	}
	RewriteTypes(prog, func(t *Type) *Type {
		if t == from {
			return to
		}
		return t
	})
	if own.Type != to {
		t.Errorf("the program's own var kept its type")
	}
	if built.Type != to {
		t.Errorf("a package the build made, not frozen, kept its type")
	}
	if slot.Content != from {
		t.Errorf("a library package's slot was rewritten through a namespace naming it")
	}
}
