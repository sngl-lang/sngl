package checker

import "slices"

import "git.duckfam.us/jonathan/sngl/ir"

// analyzeAsync propagates IsAsync over the call graph by fixed-point.
//
// Sources: native-imported funcs whose declared signature returned a
// Promise<T> (the importer set IsAsync at decl time).
//
// Propagation: any SNGL function whose body transitively calls an
// IsAsync function becomes IsAsync itself. Mirrors the CanError pass,
// but without handler-scoping — async is purely a transitive property.
func (c *checker) analyzeAsync() {
	pkg := c.pkg
	if pkg == nil {
		return
	}
	funcs := allFuncs(pkg)
	for {
		changed := false
		for _, fn := range funcs {
			if fn.IsAsync {
				continue
			}
			if blockHasAsyncCall(fn.Block) {
				fn.IsAsync = true
				changed = true
			}
		}
		if !changed {
			break
		}
	}
}

func blockHasAsyncCall(stmts []ir.Stmt) bool {
	return slices.ContainsFunc(stmts, stmtHasAsyncCall)
}

func stmtHasAsyncCall(s ir.Stmt) bool {
	switch x := s.(type) {
	case *ir.CallStmt:
		return exprHasAsyncCall(x.Call)
	case *ir.Assign:
		return exprHasAsyncCall(x.Value)
	case *ir.LocalVar:
		return exprHasAsyncCall(x.Init)
	case *ir.Return:
		return exprHasAsyncCall(x.Value)
	case *ir.If:
		return exprHasAsyncCall(x.Cond) || blockHasAsyncCall(x.Body) || blockHasAsyncCall(x.Else)
	case *ir.For:
		return exprHasAsyncCall(x.Iter) || blockHasAsyncCall(x.Body) || blockHasAsyncCall(x.Else)
	case *ir.PlatformFilter:
		return blockHasAsyncCall(x.Body)
	}
	return false
}

func exprHasAsyncCall(e ir.Expr) bool {
	if e == nil {
		return false
	}
	switch x := e.(type) {
	case *ir.Call:
		if x.Func != nil && x.Func.IsAsync {
			return true
		}
		for _, a := range x.Args {
			if exprHasAsyncCall(a.Value) {
				return true
			}
		}
		if x.Receiver != nil && exprHasAsyncCall(x.Receiver) {
			return true
		}
	case *ir.Binary:
		return exprHasAsyncCall(x.Left) || exprHasAsyncCall(x.Right)
	case *ir.Unary:
		return exprHasAsyncCall(x.Operand)
	case *ir.Ternary:
		return exprHasAsyncCall(x.Cond) || exprHasAsyncCall(x.Then) || exprHasAsyncCall(x.Else)
	case *ir.Conversion:
		return exprHasAsyncCall(x.Operand)
	case *ir.Select:
		return exprHasAsyncCall(x.Operand)
	case *ir.Index:
		return exprHasAsyncCall(x.Operand) || exprHasAsyncCall(x.Idx)
	case *ir.ListLit:
		if slices.ContainsFunc(x.Elems, exprHasAsyncCall) {
			return true
		}
	case *ir.StructLit:
		for _, f := range x.Fields {
			if exprHasAsyncCall(f.Value) {
				return true
			}
		}
	case *ir.Spread:
		return exprHasAsyncCall(x.Operand)
	}
	return false
}
