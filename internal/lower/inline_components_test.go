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
