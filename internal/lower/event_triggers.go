package lower

import (
	"fmt"

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
	apply: func(pkg *ir.Package, _ Features, opts Options) error {
		_ = ir.Walk(pkg, func(n ir.Node) error {
			if call, ok := n.(*ir.Call); ok && call.Event != "" && call.Receiver != nil {
				rewriteEventTrigger(call, opts)
			}
			return nil
		})
		return nil
	},
}

func rewriteEventTrigger(call *ir.Call, opts Options) {
	t := call.Receiver.ExprType()
	if t == nil || t.Kind != ir.TypeComponent {
		return
	}
	comp, ok := t.Decl.(*ir.Component)
	if !ok {
		return
	}
	// A library component's override is swapped in only later
	// (passPlatformExtensionBody), and is what declares the trigger.
	var trig *ir.Func
	for _, fn := range comp.FuncsFor(opts.Platform, opts.Language) {
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
	if opts.triggers != nil {
		opts.triggers[call] = *call
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

// verifyTriggersReached refuses a test's call of a trigger that hands it
// fewer arguments than it takes: a copy a loop's body was spliced into once
// per element takes the loop's variables, which a test's `c.pick[1]` does
// not say, and bubbletea's build did not compile. An assertion at the end of
// Lower, as verifyMutationsAreStatements is.
func verifyTriggersReached(pkg *ir.Package) error {
	var bad error
	for _, fn := range pkg.Funcs {
		if fn == nil || !fn.IsTest {
			continue
		}
		_ = ir.Walk(fn.Block, func(n ir.Node) error {
			c, ok := n.(*ir.Call)
			if !ok || c.Func == nil || c.Func.Trigger == "" || len(c.Args) >= len(c.Func.Params) {
				return nil
			}
			bad = fmt.Errorf("%s: %s", exprPos(c), unreachedTrigger(c.Func.Trigger))
			return ir.SkipAll
		})
		if bad != nil {
			return bad
		}
	}
	return nil
}
