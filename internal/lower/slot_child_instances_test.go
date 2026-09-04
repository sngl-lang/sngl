package lower

import (
	"testing"

	"git.duckfam.us/jonathan/sngl/ir"
)

// TestSlotChildInstancesReachesSlotContent names the bodies the walk this pass
// used to run did not reach. A list written inside `slot header { }` or under
// a context provider is a list like any other: without the synthesized child
// the slot has no identity to match, so it removes every row and appends them
// all back on each render, which is the degradation the pass exists to
// prevent.
func TestSlotChildInstancesReachesSlotContent(t *testing.T) {
	items := &ir.Var{
		Name: "items",
		Type: &ir.Type{Kind: ir.TypeList, Elems: []*ir.Type{{Kind: ir.TypeDyn}}},
	}
	// A handler writing `items` is what makes it reactive.
	mutate := &ir.NodeInst{Name: "button", Handlers: []ir.EventHandler{{
		Name: "click",
		Func: &ir.Func{Block: []ir.Stmt{&ir.Assign{
			Target: &ir.Ident{Name: "items", Sym: items},
			Value:  &ir.ListLit{},
		}}},
	}}}

	row := func() *ir.NodeInst { return &ir.NodeInst{Name: "text"} }
	inSlot, inProvider := row(), row()
	loop := func(body ir.Stmt) *ir.For {
		return &ir.For{Key: "x", Iter: &ir.Ident{Name: "items", Sym: items}, Body: []ir.Stmt{body}}
	}
	main := &ir.Component{Name: "main", Vars: []*ir.Var{items}, Body: []ir.Stmt{
		mutate,
		&ir.NodeInst{Name: "panel", Slots: map[string]*ir.SlotContent{
			"header": {Body: []ir.Stmt{loop(inSlot)}},
		}},
		&ir.ContextProvider{Children: []ir.Stmt{loop(inProvider)}},
	}}
	pkg := &ir.Package{Components: []*ir.Component{main}}

	if err := lowerSlotChildInstances(pkg, Caps{}, Options{}); err != nil {
		t.Fatalf("lowerSlotChildInstances: %v", err)
	}
	if got := len(pkg.Components); got != 3 {
		t.Fatalf("got %d components, want main plus one synthesized child per loop", got)
	}
	// Each loop body now instantiates the component its row became, and the
	// row itself is that component's body.
	for _, where := range []struct {
		name string
		body []ir.Stmt
		row  *ir.NodeInst
	}{
		{"slot content", main.Body[1].(*ir.NodeInst).Slots["header"].Body[0].(*ir.For).Body, inSlot},
		{"context provider", main.Body[2].(*ir.ContextProvider).Children[0].(*ir.For).Body, inProvider},
	} {
		inst, ok := where.body[0].(*ir.NodeInst)
		if !ok || inst.Component == nil {
			t.Errorf("%s: loop body is still %T, not an instantiation", where.name, where.body[0])
			continue
		}
		if len(inst.Component.Body) != 1 || inst.Component.Body[0] != where.row {
			t.Errorf("%s: synthesized component does not hold the row", where.name)
		}
		if !inst.Component.RuntimeInstance {
			t.Errorf("%s: synthesized component is not a runtime instance", where.name)
		}
	}
}
