package lower

import (
	"testing"

	"git.duckfam.us/jonathan/sngl/ir"
)

func TestFindRecursiveCycles(t *testing.T) {
	// A → B → A (mutual), C → C (self), D → E (acyclic).
	a := &ir.Component{Name: "A"}
	b := &ir.Component{Name: "B"}
	c := &ir.Component{Name: "C"}
	d := &ir.Component{Name: "D"}
	e := &ir.Component{Name: "E"}

	a.Body = []ir.Stmt{&ir.NodeInst{Component: b}}
	b.Body = []ir.Stmt{&ir.NodeInst{Component: a}}
	c.Body = []ir.Stmt{&ir.NodeInst{Component: c}}
	d.Body = []ir.Stmt{&ir.NodeInst{Component: e}}
	e.Body = []ir.Stmt{}

	pkg := &ir.Package{Components: []*ir.Component{a, b, c, d, e}}
	got := findRecursiveCycles(pkg)
	want := map[*ir.Component]bool{a: true, b: true, c: true}
	for k := range want {
		if !got[k] {
			t.Errorf("missing %s from cycles", k.Name)
		}
	}
	for k := range got {
		if !want[k] {
			t.Errorf("unexpected cycle node %s", k.Name)
		}
	}
}

func TestFindRecursiveCyclesThroughIfBranch(t *testing.T) {
	tv := &ir.Component{Name: "TreeView"}
	tv.Body = []ir.Stmt{
		&ir.If{Body: []ir.Stmt{&ir.NodeInst{Component: tv}}},
	}
	pkg := &ir.Package{Components: []*ir.Component{tv}}
	got := findRecursiveCycles(pkg)
	if !got[tv] {
		t.Errorf("TreeView self-recursion should be detected")
	}
}

func TestInlineComponents_SkipsReactiveForBody(t *testing.T) {
	items := &ir.Var{
		Name: "items",
		Type: &ir.Type{Kind: ir.TypeList, Elems: []*ir.Type{{Kind: ir.TypeDyn}}},
	}
	card := &ir.Component{Name: "card"}

	// Handler that mutates `items` — makes items "reactive".
	mutatingHandler := &ir.NodeInst{
		Handlers: []ir.EventHandler{
			{
				Name: "click",
				Func: &ir.Func{Block: []ir.Stmt{
					&ir.Assign{
						Target: &ir.Ident{Name: "items", Sym: items},
						Value:  &ir.ListLit{},
					},
				}},
			},
		},
	}

	nodeInst := &ir.NodeInst{Component: card}
	forStmt := &ir.For{
		Key:  "x",
		Iter: &ir.Ident{Name: "items", Sym: items},
		Body: []ir.Stmt{nodeInst},
	}

	main := &ir.Component{
		Name: "main",
		Vars: []*ir.Var{items},
		Body: []ir.Stmt{forStmt, mutatingHandler},
	}

	pkg := &ir.Package{Components: []*ir.Component{card, main}}

	if err := lowerInlineComponents(pkg, Caps{NoInlineComponents: true}, Options{}); err != nil {
		t.Fatal(err)
	}

	// card should be retained.
	found := false
	for _, c := range pkg.Components {
		if c == card {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("card component should be retained when used inside reactive For body")
	}

	// The NodeInst inside the for body should still exist.
	if len(forStmt.Body) != 1 {
		t.Fatalf("for body length: got %d, want 1", len(forStmt.Body))
	}
	got, ok := forStmt.Body[0].(*ir.NodeInst)
	if !ok {
		t.Fatalf("for body[0]: got %T, want *ir.NodeInst", forStmt.Body[0])
	}
	if got.Component != card {
		t.Errorf("for body NodeInst.Component: got %v, want card", got.Component)
	}
}

func TestInlineComponents_SkipsRecursiveComponent(t *testing.T) {
	// component tree { tree(...) } — self-recursive.
	tree := &ir.Component{Name: "tree"}
	tree.Body = []ir.Stmt{&ir.NodeInst{Component: tree}}

	main := &ir.Component{
		Name: "main",
		Body: []ir.Stmt{&ir.NodeInst{Component: tree}},
	}

	pkg := &ir.Package{Components: []*ir.Component{tree, main}}

	if err := lowerInlineComponents(pkg, Caps{NoInlineComponents: true}, Options{}); err != nil {
		t.Fatal(err)
	}

	// tree should be retained as a real component.
	found := false
	for _, c := range pkg.Components {
		if c == tree {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("recursive tree component should be retained")
	}

	// The NodeInst in main.Body should still target tree.
	if len(main.Body) != 1 {
		t.Fatalf("main.Body length: got %d, want 1", len(main.Body))
	}
	got, ok := main.Body[0].(*ir.NodeInst)
	if !ok {
		t.Fatalf("main.Body[0]: got %T, want *ir.NodeInst", main.Body[0])
	}
	if got.Component != tree {
		t.Errorf("main.Body NodeInst.Component: got %v, want tree", got.Component)
	}
}
