package lower

import (
	"slices"

	"duckfam.us/sngl/ir"
)

// passCallDefaults writes a parameter's default into each call that omits it:
// a function a backend emits takes every parameter it declares, and no
// emitter declares a default. It runs before the inliners rename what a
// default reads and before any pass appends an argument of its own. A native
// or an intrinsic is called with whatever arity its host or emitter answers
// for, and is left as written.
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
	if fn == nil || fn.Intrinsic != "" || (fn.Foreign.Name != "" && !fn.Foreign.Marked) {
		return
	}
	var args []ir.CallArg
	var recv ir.Expr
	next, filled := 0, false
	for _, p := range fn.Params {
		switch {
		case next < len(c.Args) && (c.Args[next].Name == "" || c.Args[next].Name == p.Name):
			if p.Receiver {
				recv = c.Args[next].Value
			}
			args = append(args, c.Args[next])
			next++
		case p.Receiver:
		case p.Default != nil:
			args = append(args, ir.CallArg{Name: p.Name, Value: defaultAt(fn, p.Default, recv)})
			filled = true
		default:
			return
		}
	}
	if filled && next == len(c.Args) {
		c.Args = args
	}
}

// defaultAt is def as a call reaching fn through recv writes it: a component
// method called through a handle reads the component's vars and props off the
// handle, since the caller has none of its own.
func defaultAt(fn *ir.Func, def ir.Expr, recv ir.Expr) ir.Expr {
	out := ir.CloneExprSharingDecls(def)
	comp, ok := fn.RecvDecl().(*ir.Component)
	if !ok || recv == nil {
		return out
	}
	through := func(e ir.Expr) ir.Expr {
		id, ok := e.(*ir.Ident)
		if !ok {
			return e
		}
		switch s := id.Sym.(type) {
		case *ir.Var:
			if !slices.Contains(comp.Vars, s) {
				return e
			}
		case *ir.Param:
			if !slices.ContainsFunc(comp.Props, func(p *ir.Prop) bool { return p.Sym == s }) {
				return e
			}
		default:
			return e
		}
		return &ir.Select{Type: id.Type, Operand: ir.CloneExprSharingDecls(recv), Field: id.Name}
	}
	out = through(out)
	_ = ir.RewriteExprs(out, func(e ir.Expr) (ir.Expr, error) { return through(e), nil })
	return out
}
