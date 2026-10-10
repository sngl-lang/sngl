package checker

import "duckfam.us/sngl/ir"

// analyzeAsyncWithPointsTo extends color propagation to cover funcvar call
// sites: if a slot pointed to by a Callee contains any async candidate, the
// enclosing function is colored async. Runs after analyzePointsTo populates
// pkg.PointsTo. The pass is a fixed-point loop so that chains of callers are
// handled correctly.
func (c *checker) analyzeAsyncWithPointsTo() {
	pkg := c.pkg
	if pkg == nil || pkg.PointsTo == nil {
		return
	}
	// Lambdas too, for the reason analyzeAsync lists them: colouring stops at
	// a closure -- constructing one is not calling it -- so the arrow holding
	// the awaiting call is the only thing left to colour, and nothing colours
	// what it was not given.
	funcs := allFuncsAndLambdas(pkg)
	for {
		changed := false
		for _, fn := range funcs {
			if fn.IsAsync {
				continue
			}
			if ir.BlockHasFuncvarAsyncCall(fn.Block, pkg.PointsTo) {
				fn.IsAsync = true
				changed = true
			}
		}
		if !changed {
			break
		}
	}
}

// analyzeAsync propagates IsAsync over the call graph by fixed-point.
//
// Sources: native-imported funcs whose declared signature returned a
// Promise<T> (the importer set IsAsync at decl time).
//
// Propagation: any SNGL function whose body transitively calls an
// IsAsync function becomes IsAsync itself. Mirrors the CanError pass,
// but without handler-scoping — async is purely a transitive property.
// Lambdas are visited too, but only in their own right: allFuncsAndLambdas
// lists each one's Func, so it gets IsAsync and codegen emits the `async`
// keyword on the lambda expression itself. The function that *holds* the
// lambda does not -- constructing a closure is not calling it, which is why
// ExprHasAsyncCall stops at a Lambda rather than reading through to its body.
func (c *checker) analyzeAsync() {
	pkg := c.pkg
	if pkg == nil {
		return
	}
	funcs := allFuncsAndLambdas(pkg)
	for {
		changed := false
		for _, fn := range funcs {
			if fn.IsAsync {
				continue
			}
			if ir.BlockHasAsyncCall(fn.Block) {
				fn.IsAsync = true
				changed = true
			}
		}
		if !changed {
			break
		}
	}
}

// allFuncsAndLambdas extends allFuncs with the *ir.Func backing every
// *ir.Lambda reachable from the package's top-level statements and func
// bodies. Async coloring needs to set IsAsync on lambda funcs so codegen
// emits `async () => ...` when the body awaits.
func allFuncsAndLambdas(pkg *ir.Package) []*ir.Func {
	funcs := allFuncs(pkg)
	seen := make(map[*ir.Func]bool, len(funcs))
	for _, fn := range funcs {
		seen[fn] = true
	}
	for _, fn := range append([]*ir.Func(nil), funcs...) {
		collectLambdaFuncs(fn.Block, &funcs, seen)
	}
	return funcs
}

func collectLambdaFuncs(stmts []ir.Stmt, out *[]*ir.Func, seen map[*ir.Func]bool) {
	for _, s := range stmts {
		switch x := s.(type) {
		case *ir.Assign:
			collectLambdasInExpr(x.Value, out, seen)
		case *ir.LocalVar:
			if x.Init != nil {
				collectLambdasInExpr(x.Init, out, seen)
			}
		case *ir.Return:
			if x.Value != nil {
				collectLambdasInExpr(x.Value, out, seen)
			}
		case *ir.CallStmt:
			if x.Call != nil {
				collectLambdasInExpr(x.Call, out, seen)
			}
		case *ir.If:
			collectLambdasInExpr(x.Cond, out, seen)
			collectLambdaFuncs(x.Body, out, seen)
			collectLambdaFuncs(x.Else, out, seen)
		case *ir.For:
			collectLambdasInExpr(x.Iter, out, seen)
			collectLambdaFuncs(x.Body, out, seen)
		case *ir.Emit:
			for _, a := range x.Args {
				collectLambdasInExpr(a.Value, out, seen)
			}
		}
	}
}

func collectLambdasInExpr(e ir.Expr, out *[]*ir.Func, seen map[*ir.Func]bool) {
	if e == nil {
		return
	}
	switch x := e.(type) {
	case *ir.Lambda:
		if x.Func != nil && !seen[x.Func] {
			seen[x.Func] = true
			*out = append(*out, x.Func)
			collectLambdaFuncs(x.Func.Block, out, seen)
		}
	case *ir.Binary:
		collectLambdasInExpr(x.Left, out, seen)
		collectLambdasInExpr(x.Right, out, seen)
	case *ir.Unary:
		collectLambdasInExpr(x.Operand, out, seen)
	case *ir.Ternary:
		collectLambdasInExpr(x.Cond, out, seen)
		collectLambdasInExpr(x.Then, out, seen)
		collectLambdasInExpr(x.Else, out, seen)
	case *ir.Select:
		collectLambdasInExpr(x.Operand, out, seen)
	case *ir.Index:
		collectLambdasInExpr(x.Operand, out, seen)
		collectLambdasInExpr(x.Idx, out, seen)
	case *ir.Call:
		if x.Receiver != nil {
			collectLambdasInExpr(x.Receiver, out, seen)
		}
		if x.Callee != nil {
			collectLambdasInExpr(x.Callee, out, seen)
		}
		for _, a := range x.Args {
			collectLambdasInExpr(a.Value, out, seen)
		}
	case *ir.Conversion:
		collectLambdasInExpr(x.Operand, out, seen)
	case *ir.StructLit:
		for _, f := range x.Fields {
			collectLambdasInExpr(f.Value, out, seen)
		}
	case *ir.ListLit:
		for _, el := range x.Elems {
			collectLambdasInExpr(el, out, seen)
		}
	case *ir.MapLitIR:
		for _, ent := range x.Entries {
			collectLambdasInExpr(ent.Key, out, seen)
			collectLambdasInExpr(ent.Value, out, seen)
		}
	case *ir.Spread:
		collectLambdasInExpr(x.Operand, out, seen)
	}
}
