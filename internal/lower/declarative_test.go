package lower

import (
	"testing"

	"git.duckfam.us/jonathan/sngl/ir"
)

// A recursive component (tree → tree) is left in place by
// passNoInlineComponents because it sits in a cycle. passDeclarative then
// lowers the NodeInst into a CreateComponent + (handled by caller)
// AppendChild instead of CreateNode("tree").
func TestDeclarative_RecursiveComponentEmitsCreateComponent(t *testing.T) {
	tree := &ir.Component{Name: "tree"}
	tree.Body = []ir.Stmt{&ir.NodeInst{Name: "tree", Component: tree}}
	main := &ir.Component{
		Name: "main",
		Body: []ir.Stmt{&ir.NodeInst{Name: "tree", Component: tree}},
	}

	pkg := &ir.Package{Components: []*ir.Component{tree, main}}

	if err := lowerInlineComponents(pkg, Caps{NoInlineComponents: true}, Options{}); err != nil {
		t.Fatalf("inline: %v", err)
	}
	if err := lowerDeclarative(pkg, Caps{NoDeclarative: true}, Options{}); err != nil {
		t.Fatalf("declarative: %v", err)
	}

	if !blockContainsCreateComponent(main.Body) {
		t.Errorf("expected CreateComponent in main.Body for static recursive NodeInst; got %#v", main.Body)
	}
}

// blockContainsCreateComponent reports whether stmts contains a
// `var _ = lower.CreateComponent(...)` LocalVar at any depth a slot or
// component body would surface — used by both declarative + reactivity
// tests.
func blockContainsCreateComponent(stmts []ir.Stmt) bool {
	for _, s := range stmts {
		lv, ok := s.(*ir.LocalVar)
		if !ok {
			continue
		}
		call, ok := lv.Init.(*ir.Call)
		if !ok {
			continue
		}
		if call.Func != nil && call.Func.Name == "CreateComponent" {
			return true
		}
	}
	return false
}
