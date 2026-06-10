package lower

import (
	"testing"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/ir"
)

// createNodeLV builds the LocalVar shape passDeclarative emits for a
// flattened widget node: `var __nN dyn = lower.CreateNode("tag")`.
func createNodeLV(id, tag string) *ir.LocalVar {
	return &ir.LocalVar{
		Name: id,
		Type: ir.TypDyn,
		Init: &ir.Call{
			Type:     ir.TypDyn,
			Receiver: &ir.Ident{Name: "lower"},
			Func:     &ir.Func{Name: "CreateNode"},
			Args:     []ir.CallArg{{Value: &ir.Literal{Type: ir.TypString, Raw: tag}}},
		},
	}
}

// elemRef builds a synthesized element-ref ident (`#__nN`) of the kind
// passDeclarative emits in AppendChild / PropAssign / returns.
func elemRef(id string) *ir.Ident {
	return &ir.Ident{Name: id, Type: ir.TypDyn, IsElementRef: true, Synthesized: true}
}

// appendChildStmt mirrors the lower.AppendChild(parent, child) CallStmt
// passDeclarative emits.
func appendChildStmt(parent, child string) *ir.CallStmt {
	return &ir.CallStmt{Call: &ir.Call{
		Type:     ir.TypVoid,
		Receiver: &ir.Ident{Name: "lower"},
		Func:     &ir.Func{Name: "AppendChild"},
		Args: []ir.CallArg{
			{Value: elemRef(parent)},
			{Value: elemRef(child)},
		},
	}}
}

// TestNodeEscape_RecursiveComponentRefsAreLocal verifies the escape pass
// marks a recursive component render method's internal widget refs as
// LOCAL (they are created and used only within that component body), while
// a main-tree ref touched by a reactive updater func stays ESCAPING (a
// Model field). This is the core invariant that prevents the gtk4 recursion
// self-append hang: each recursion frame must get its own locals.
func TestNodeEscape_RecursiveComponentRefsAreLocal(t *testing.T) {
	// Recursive component body: creates __n2 (vbox) and __n3 (text),
	// appends __n3 to __n2 — all internal temps, none referenced elsewhere.
	tree := &ir.Component{Name: "TreeView"}
	tree.Body = []ir.Stmt{
		createNodeLV("__n2", "vbox"),
		createNodeLV("__n3", "text"),
		appendChildStmt("__n2", "__n3"),
	}

	// Main body: creates __n0 (vbox) and __n1 (text). __n1 is mutated by a
	// reactive updater func below, so it must escape to a Model field.
	main := &ir.Component{Name: "main"}
	main.Body = []ir.Stmt{
		createNodeLV("__n0", "vbox"),
		createNodeLV("__n1", "text"),
		appendChildStmt("__n0", "__n1"),
	}
	// A reactive updater func (a separate scope) that references __n1 — the
	// updater patches the text node's prop when state changes.
	updater := &ir.Func{
		Name:        "__update0",
		Synthesized: true,
		Block: []ir.Stmt{
			&ir.Assign{
				Target: &ir.Select{Operand: elemRef("__n1"), Field: "value", Type: ir.TypDyn},
				Op:     ast.AssignSet,
				Value:  &ir.Literal{Type: ir.TypString, Raw: "x"},
			},
		},
	}
	main.Funcs = []*ir.Func{updater}

	pkg := &ir.Package{Components: []*ir.Component{tree, main}}

	caps := Caps{NoDeclarative: true}
	if err := lowerNodeEscape(pkg, caps, Options{}); err != nil {
		t.Fatalf("node escape: %v", err)
	}

	// Recursive component: both internal refs must be LOCAL.
	if !tree.LocalRefs["__n2"] {
		t.Errorf("expected __n2 to be a local ref in TreeView; got LocalRefs=%v", tree.LocalRefs)
	}
	if !tree.LocalRefs["__n3"] {
		t.Errorf("expected __n3 to be a local ref in TreeView; got LocalRefs=%v", tree.LocalRefs)
	}

	// Main body: __n0 is created+used only in main.Body → local.
	if !main.LocalRefs["__n0"] {
		t.Errorf("expected __n0 to be a local ref in main; got LocalRefs=%v", main.LocalRefs)
	}
	// __n1 is referenced by the updater func (another scope) → ESCAPES,
	// must NOT be marked local.
	if main.LocalRefs["__n1"] {
		t.Errorf("expected __n1 to ESCAPE (referenced by updater func), but it was marked local; LocalRefs=%v", main.LocalRefs)
	}
}
