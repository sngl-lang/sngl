package lower

import "duckfam.us/sngl/ir"

// passCallDefaults writes a parameter's default into each call that omits it.
//
// The checker leaves an omitted argument out of Call.Args and keeps the
// default on the parameter, which the optimizer's inliner and the interpreter
// read. A function a backend emits takes every parameter it declares: Go has no
// default, and the JavaScript and Kotlin emitters declare none, so `plus(1)`
// for `func plus(k int, by int = 2)` did not compile on Go and added
// `undefined` on JavaScript.
//
// A native or an intrinsic is the host's or an emitter's to call, with
// whatever arity it answers for, and is left as written.
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

func fillDefaults(c *ir.Call) {
	fn := c.Func
	if fn == nil || fn.Intrinsic != "" || (fn.Foreign.Name != "" && !fn.Foreign.Marked) || len(c.Args) >= len(fn.Params) {
		return
	}
	args := make([]ir.CallArg, 0, len(fn.Params))
	bound := map[*ir.Param]ir.Expr{}
	next := 0
	for _, p := range fn.Params {
		if next < len(c.Args) && (c.Args[next].Name == "" || c.Args[next].Name == p.Name) {
			args = append(args, c.Args[next])
			bound[p] = c.Args[next].Value
			next++
			continue
		}
		if p.Default == nil {
			return
		}
		def := ir.CloneExprSharingDecls(p.Default)
		// A default may name an earlier parameter, which at the call is the
		// argument bound to it.
		_ = ir.RewriteExprs(def, func(e ir.Expr) (ir.Expr, error) {
			if id, ok := e.(*ir.Ident); ok {
				if p, ok := id.Sym.(*ir.Param); ok && bound[p] != nil {
					return ir.CloneExprSharingDecls(bound[p]), nil
				}
			}
			return e, nil
		})
		args = append(args, ir.CallArg{Name: p.Name, Value: def})
		bound[p] = def
	}
	if next == len(c.Args) {
		c.Args = args
	}
}
