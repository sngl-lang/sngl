package lower

import (
	"strconv"

	"duckfam.us/sngl/ast"
	"duckfam.us/sngl/ir"
)

// passBuiltinMethods makes each library method of a builtin type that a call
// still names a function of the package: `n.format(16)` is
// `__int_format(n, 16)`.
//
// No output can declare a method on an int, and passLibFuncs promotes plain
// helpers only, so such a method reaches a target by being inlined or not at
// all. The optimizer inlines every one the library writes for that -- each
// builtin format reads its parameters once, and an appendFormat is one call --
// so a build that optimizes first leaves nothing here. A pipeline that lowers
// without optimizing does, and emitted `d.format(10, 0, " ")` on a Go int
// (gtk4's TestIntegration_ComputedBodyImportCollected).
//
// Early, so that each pass after it sees an ordinary function: an
// appendFormat's builder becomes a string under StringBuilders and its
// `ref[builder]` a value handed back under RefParams, and a format's defaults
// are written by CallDefaults. A generic receiver's method is left alone: its
// `this` is no parameter until ReceiverParam, and Monomorphize copies it.
var passBuiltinMethods = pass{
	name:    "BuiltinMethods",
	enabled: func(Features) bool { return true },
	apply:   lowerBuiltinMethods,
}

func lowerBuiltinMethods(pkg *ir.Package, _ Features, _ Options) error {
	if err := ir.RewriteExprs(pkg, func(e ir.Expr) (ir.Expr, error) {
		if call, ok := e.(*ir.Call); ok && promotableBuiltinMethod(call.Func) {
			if inlined := inlineSettled(call, 0); inlined != nil {
				return inlined, nil
			}
		}
		return e, nil
	}); err != nil {
		return err
	}
	promoted := map[*ir.Func]bool{}
	return ir.Walk(pkg, func(n ir.Node) error {
		call, ok := n.(*ir.Call)
		if !ok || !promotableBuiltinMethod(call.Func) {
			return nil
		}
		fn := call.Func
		if !promoted[fn] {
			promoted[fn] = true
			fn.Name = "__" + fn.RecvName() + "_" + fn.Name
			fn.Receiver = nil
			pkg.Funcs = append(pkg.Funcs, fn)
		}
		return nil
	})
}

// inlineSettled is call, of a library function whose body returns one
// expression, as that expression with the arguments in place of its
// parameters and each conditional they settle taken -- `n.format()` is
// `_text(n)`, as the optimizer would have made it -- or nil where an argument
// that is not a path or a literal would be read other than once. A library
// helper it calls in turn is inlined the same way, or left for passLibFuncs.
func inlineSettled(call *ir.Call, depth int) ir.Expr {
	fn := call.Func
	if depth > 8 || fn == nil || fn.PkgPath() == "" || fn.Intrinsic != "" || fn.Foreign.Path != "" ||
		len(fn.TypeParams) > 0 || len(fn.RecvTypeParams) > 0 || fn.RecvParam != nil || len(fn.Block) != 1 {
		return nil
	}
	ret, ok := fn.Block[0].(*ir.Return)
	if !ok || ret.Value == nil {
		return nil
	}
	// The arguments are in parameter order, a named one for its own
	// parameter, and CallDefaults has not yet written the rest.
	args := map[*ir.Param]ir.Expr{}
	next := 0
	for _, p := range fn.Params {
		switch {
		case next < len(call.Args) && (call.Args[next].Name == "" || call.Args[next].Name == p.Name):
			args[p] = call.Args[next].Value
			next++
		case p.Default != nil:
			args[p] = p.Default
		default:
			return nil
		}
	}
	if next != len(call.Args) {
		return nil
	}
	consts := map[*ir.Param]ir.Expr{}
	for p, a := range args {
		if _, ok := constValue(a); ok {
			consts[p] = a
		}
	}
	body := settle(substitute(ir.CloneExprSharingDecls(ret.Value), consts))
	reads := map[*ir.Param]int{}
	_ = ir.WalkExprs(body, func(e ir.Expr) error {
		if id, ok := e.(*ir.Ident); ok {
			if p, ok := id.Sym.(*ir.Param); ok {
				reads[p]++
			}
		}
		return nil
	})
	for p, n := range reads {
		if n > 1 && !copyable(args[p]) {
			return nil
		}
	}
	return rewriteExpr(substitute(body, args), func(e ir.Expr) (ir.Expr, error) {
		if c, ok := e.(*ir.Call); ok && c != call && c.Func != nil && !c.Func.IsMethod() {
			if inlined := inlineSettled(c, depth+1); inlined != nil {
				return inlined, nil
			}
		}
		return e, nil
	})
}

// substitute is e with each parameter subs binds read as its argument.
func substitute(e ir.Expr, subs map[*ir.Param]ir.Expr) ir.Expr {
	return rewriteExpr(e, func(x ir.Expr) (ir.Expr, error) {
		if id, ok := x.(*ir.Ident); ok {
			if p, ok := id.Sym.(*ir.Param); ok && subs[p] != nil {
				return ir.CloneExprSharingDecls(subs[p]), ir.SkipDir
			}
		}
		return x, nil
	})
}

// rewriteExpr is e rewritten by f, e itself included.
func rewriteExpr(e ir.Expr, f func(ir.Expr) (ir.Expr, error)) ir.Expr {
	holder := &ir.Return{Value: e}
	_ = ir.RewriteExprs(holder, f)
	return holder.Value
}

// settle is e with each conditional whose condition is a constant replaced
// by the branch it takes.
func settle(e ir.Expr) ir.Expr {
	t, ok := e.(*ir.Ternary)
	if !ok {
		return e
	}
	if v, ok := constValue(t.Cond); ok {
		if b, isBool := v.(bool); isBool {
			if b {
				return settle(t.Then)
			}
			return settle(t.Else)
		}
	}
	return e
}

// copyable reports whether e may be evaluated as many times as it is read.
func copyable(e ir.Expr) bool {
	switch x := e.(type) {
	case *ir.Literal:
		return true
	case *ir.Ident:
		return true
	case *ir.Select:
		return copyable(x.Operand)
	}
	_, ok := constValue(e)
	return ok
}

// constValue is e's value where e is a literal number, string or bool, a
// negated number, or a comparison or a logical operator over those.
func constValue(e ir.Expr) (any, bool) {
	switch x := e.(type) {
	case *ir.Literal:
		if x.Type == nil {
			return nil, false
		}
		switch x.Type.Kind {
		case ir.TypeBool:
			return x.Value == "true", true
		case ir.TypeInt, ir.TypeFloat:
			f, err := strconv.ParseFloat(x.Value, 64)
			return f, err == nil
		case ir.TypeString:
			return x.Value, true
		}
	case *ir.Unary:
		if x.Op == ast.UnaryNeg {
			if v, ok := constValue(x.Operand); ok {
				if f, isNum := v.(float64); isNum {
					return -f, true
				}
			}
		}
	case *ir.Binary:
		l, lok := constValue(x.Left)
		r, rok := constValue(x.Right)
		if !lok || !rok {
			return nil, false
		}
		if lb, ok := l.(bool); ok {
			rb, ok := r.(bool)
			if !ok {
				return nil, false
			}
			switch x.Op {
			case ast.BinAnd:
				return lb && rb, true
			case ast.BinOr:
				return lb || rb, true
			case ast.BinEq:
				return lb == rb, true
			case ast.BinNeq:
				return lb != rb, true
			}
			return nil, false
		}
		if lf, ok := l.(float64); ok {
			rf, ok := r.(float64)
			if !ok {
				return nil, false
			}
			switch x.Op {
			case ast.BinEq:
				return lf == rf, true
			case ast.BinNeq:
				return lf != rf, true
			case ast.BinLt:
				return lf < rf, true
			case ast.BinLte:
				return lf <= rf, true
			case ast.BinGt:
				return lf > rf, true
			case ast.BinGte:
				return lf >= rf, true
			}
			return nil, false
		}
		if ls, ok := l.(string); ok {
			rs, ok := r.(string)
			if !ok {
				return nil, false
			}
			switch x.Op {
			case ast.BinEq:
				return ls == rs, true
			case ast.BinNeq:
				return ls != rs, true
			}
		}
	}
	return nil, false
}

// promotableBuiltinMethod reports whether fn is a method a library wrote, with
// a body, for a builtin type that takes no type parameters, in a package this
// build copied.
func promotableBuiltinMethod(fn *ir.Func) bool {
	if fn == nil || fn.Frozen || fn.PkgPath() == "" || fn.Intrinsic != "" || len(fn.Block) == 0 ||
		len(fn.RecvTypeParams) > 0 || len(fn.TypeParams) > 0 || fn.RecvParam != nil {
		return false
	}
	sd, ok := fn.RecvDecl().(*ir.StructDef)
	return ok && (sd.Builtin != "" || sd.Intrinsic != "") && len(sd.TypeParams) == 0
}
