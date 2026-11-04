package lower

import (
	"duckfam.us/sngl/ir"
)

// passEventTriggers writes a test's trigger of an event the target overrides
// -- `c.d.change(3)` where the target declares `func dial.change[platform](to)`
// -- as a call of that override on the instance, the method of the
// component's override the checker registered it as (ir.Func.Trigger). From
// there it is a test calling a method of an instance, which every target
// already reaches. A trigger of an event the target does not override is left
// for that target's invoker.
//
// First, ahead of the test-reach passes, which point a call of an instance's
// method at the copy the instance's splice made.
var passEventTriggers = pass{
	name:    "EventTriggers",
	enabled: func(Features) bool { return true },
	apply: func(pkg *ir.Package, _ Features, _ Options) error {
		_ = ir.Walk(pkg, func(n ir.Node) error {
			if call, ok := n.(*ir.Call); ok && call.Event != "" && call.Receiver != nil {
				rewriteEventTrigger(call)
			}
			return nil
		})
		return nil
	},
}

func rewriteEventTrigger(call *ir.Call) {
	t := call.Receiver.ExprType()
	if t == nil || t.Kind != ir.TypeComponent {
		return
	}
	comp, ok := t.Decl.(*ir.Component)
	if !ok {
		return
	}
	var trig *ir.Func
	for _, fn := range comp.Funcs {
		if fn.Trigger == call.Event {
			trig = fn
		}
	}
	var event *ir.EventDecl
	for _, e := range comp.Events {
		if e.Name == call.Event {
			event = e
		}
	}
	if trig == nil || event == nil {
		return
	}
	// The test hands the event's arguments by position; the override
	// selected some of its parameters by name.
	at := map[string]int{}
	for i, p := range event.Params {
		at[p.Name] = i
	}
	args := []ir.CallArg{{Value: call.Receiver}}
	for _, p := range trig.Params {
		if p.Receiver {
			continue
		}
		if i, ok := at[p.Name]; ok && i < len(call.Args) {
			args = append(args, ir.CallArg{Value: call.Args[i].Value})
		}
	}
	call.Func, call.Args, call.Event, call.Receiver = trig, args, "", nil
}
