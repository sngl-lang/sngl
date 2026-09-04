package lower

import (
	"testing"

	"git.duckfam.us/jonathan/sngl/ir"
)

// TestInstanceEventsRewritesAnEmitInSlotContent names the bodies the walk this
// pass used to run did not reach. An emit it misses reaches codegen as a call
// to `emit`, which nothing declares -- the click throws and the handler the
// call site wrote is in the output nowhere at all, which is the whole failure
// passInstanceEvents exists to fix.
func TestInstanceEventsRewritesAnEmitInSlotContent(t *testing.T) {
	emitIn := func() *ir.Emit { return &ir.Emit{Name: "pick"} }
	inSlot, inProvider, inLambda := emitIn(), emitIn(), emitIn()

	// A handler on a node inside slot content the body supplies, one inside a
	// context provider, and one in a lifted lambda.
	picker := &ir.Component{
		Name:            "picker",
		RuntimeInstance: true,
		Events:          []*ir.EventDecl{{Name: "pick"}},
		Body: []ir.Stmt{
			&ir.NodeInst{Name: "panel", Slots: map[string]*ir.SlotContent{
				"header": {Body: []ir.Stmt{&ir.NodeInst{
					Name:     "button",
					Handlers: []ir.EventHandler{{Name: "click", Func: &ir.Func{Block: []ir.Stmt{inSlot}}}},
				}}},
			}},
			&ir.ContextProvider{Children: []ir.Stmt{&ir.NodeInst{
				Name:     "button",
				Handlers: []ir.EventHandler{{Name: "click", Func: &ir.Func{Block: []ir.Stmt{inProvider}}}},
			}}},
			&ir.LocalVar{Init: &ir.Lambda{Func: &ir.Func{Block: []ir.Stmt{inLambda}}}},
		},
	}
	pkg := &ir.Package{Components: []*ir.Component{{Name: "main"}, picker}}

	if err := lowerInstanceEvents(pkg, Caps{NoReactivity: true}, Options{}); err != nil {
		t.Fatalf("lowerInstanceEvents: %v", err)
	}

	left := 0
	_ = ir.Walk(picker, func(n ir.Node) error {
		if e, ok := n.(*ir.Emit); ok && e.Name == "pick" {
			left++
		}
		return nil
	})
	if left != 0 {
		t.Errorf("%d of 3 emits were left as emits", left)
	}
	calls := 0
	_ = ir.Walk(picker, func(n ir.Node) error {
		cs, ok := n.(*ir.CallStmt)
		if !ok || cs.Call == nil {
			return nil
		}
		if id, ok := cs.Call.Callee.(*ir.Ident); ok && id.Name == "pick" {
			calls++
		}
		return nil
	})
	if calls != 3 {
		t.Errorf("got %d calls of the pick prop, want 3", calls)
	}
}
