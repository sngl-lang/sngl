package lower

import (
	"strings"
	"testing"

	"duckfam.us/sngl/ir"
)

// A NodeInst targeting a non-native, non-empty component inside a
// reactive For body is left in place by passNoInlineComponents and then
// lowered by the slot generator into CreateComponent.
func TestReactivity_ComponentInSlotEmitsCreateComponent(t *testing.T) {
	items := &ir.Var{
		Name: "items",
		Type: &ir.Type{Kind: ir.TypeList, Elems: []*ir.Type{{Kind: ir.TypeDyn}}},
	}
	// card needs a non-empty body for hasRealComponentBody to elect the
	// CreateComponent path. Give it a single dummy var.
	card := &ir.Component{
		Name: "card",
		Vars: []*ir.Var{{Name: "dummy", Type: ir.TypInt}},
	}
	main := &ir.Component{
		Name: "main",
		Vars: []*ir.Var{items},
		Body: []ir.Stmt{
			&ir.For{
				Key:  "x",
				Iter: &ir.Ident{Name: "items", Sym: items, Type: items.Type},
				Body: []ir.Stmt{&ir.NodeInst{Name: "card", Component: card}},
			},
		},
	}

	pkg := &ir.Package{Components: []*ir.Component{card, main}}

	if err := lowerInlineComponents(pkg, without("inlineComponents"), Options{}); err != nil {
		t.Fatalf("inline: %v", err)
	}
	if err := lowerReactivity(pkg, without("reactivity"), Options{}); err != nil {
		t.Fatalf("reactivity: %v", err)
	}

	var foundCreate bool
	for _, fn := range main.Funcs {
		if !strings.HasPrefix(fn.Name, "__renderSlot") {
			continue
		}
		if findCreateComponentInStmts(fn.Block) {
			foundCreate = true
			break
		}
	}
	if !foundCreate {
		names := make([]string, len(main.Funcs))
		for i, f := range main.Funcs {
			names[i] = f.Name
		}
		t.Errorf("expected CreateComponent in a synthesized __renderSlot func; got funcs=%v", names)
	}
}

// findCreateComponentInStmts walks stmts recursively (into If/For
// bodies) looking for a `var _ = lower.CreateComponent(...)` LocalVar.
func findCreateComponentInStmts(stmts []ir.Stmt) bool {
	for _, s := range stmts {
		switch n := s.(type) {
		case *ir.LocalVar:
			if call, ok := n.Init.(*ir.Call); ok && call.Func != nil && call.Func.Name == "CreateComponent" {
				return true
			}
		case *ir.If:
			if findCreateComponentInStmts(n.Body) || findCreateComponentInStmts(n.Else) {
				return true
			}
		case *ir.For:
			if findCreateComponentInStmts(n.Body) || findCreateComponentInStmts(n.Else) {
				return true
			}
		}
	}
	return false
}
