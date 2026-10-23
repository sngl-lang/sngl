package lower

import "duckfam.us/sngl/ir"

// passCallDefaults writes a parameter's default into each call that omits it:
// a function a backend emits takes every parameter it declares, and no
// emitter declares a default. It runs before the inliners rename what a
// default reads and before any pass appends an argument of its own. A native,
// or an intrinsic with no body, is called with whatever arity its host or
// emitter answers for, and is left as written. An intrinsic with a body is
// filled like any function: its emitter stands in for the body and reads every
// parameter by position, so `i18n.numberFloat(x)` with its `style` left out
// handed the locale NoContext appends to the style's slot.
var passCallDefaults = pass{
	name:    "CallDefaults",
	enabled: func(Features) bool { return true },
	apply:   lowerCallDefaults,
}

func lowerCallDefaults(pkg *ir.Package, _ Features, _ Options) error {
	if pkg == nil {
		return nil
	}
	return ir.Walk(pkg, func(n ir.Node) error {
		if c, ok := n.(*ir.Call); ok {
			fillDefaults(c)
		}
		return nil
	})
}

// fillDefaults matches c's arguments to its callee's parameters in order: the
// checker writes them in parameter order, named for their parameters, with a
// receiver first and unnamed. A component method's receiver is implicit at a
// call in its own body and is left for passNoImplicitRecv.
func fillDefaults(c *ir.Call) {
	fn := c.Func
	if fn == nil || fn.Intrinsic != "" && len(fn.Block) == 0 || (fn.Foreign.Name != "" && !fn.Foreign.Marked) {
		return
	}
	var args []ir.CallArg
	next, filled := 0, false
	for _, p := range fn.Params {
		switch {
		case next < len(c.Args) && (c.Args[next].Name == "" || c.Args[next].Name == p.Name):
			args = append(args, c.Args[next])
			next++
		case p.Receiver:
		case p.Default != nil:
			args = append(args, ir.CallArg{Name: p.Name, Value: ir.CloneExprSharingDecls(p.Default)})
			filled = true
		default:
			return
		}
	}
	if filled && next == len(c.Args) {
		c.Args = args
	}
}
