package checker

import "git.duckfam.us/jonathan/sngl/ir"

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
	funcs := allFuncs(pkg)
	for {
		changed := false
		for _, fn := range funcs {
			if fn.IsAsync {
				continue
			}
			if ptBlockHasFuncvarAsyncCall(fn.Block, pkg.PointsTo) {
				fn.IsAsync = true
				changed = true
			}
		}
		if !changed {
			break
		}
	}
}

func ptBlockHasFuncvarAsyncCall(stmts []ir.Stmt, pts *ir.PointsToInfo) bool {
	for _, s := range stmts {
		if ptStmtHasFuncvarAsyncCall(s, pts) {
			return true
		}
	}
	return false
}

func ptStmtHasFuncvarAsyncCall(s ir.Stmt, pts *ir.PointsToInfo) bool {
	switch x := s.(type) {
	case *ir.CallStmt:
		return ptExprHasFuncvarAsyncCall(x.Call, pts)
	case *ir.Assign:
		return ptExprHasFuncvarAsyncCall(x.Value, pts) || ptExprHasFuncvarAsyncCall(x.Target, pts)
	case *ir.LocalVar:
		return ptExprHasFuncvarAsyncCall(x.Init, pts)
	case *ir.Return:
		return ptExprHasFuncvarAsyncCall(x.Value, pts)
	case *ir.If:
		return ptExprHasFuncvarAsyncCall(x.Cond, pts) ||
			ptBlockHasFuncvarAsyncCall(x.Body, pts) ||
			ptBlockHasFuncvarAsyncCall(x.Else, pts)
	case *ir.For:
		return ptExprHasFuncvarAsyncCall(x.Iter, pts) ||
			ptBlockHasFuncvarAsyncCall(x.Body, pts) ||
			ptBlockHasFuncvarAsyncCall(x.Else, pts)
	case *ir.PlatformFilter:
		return ptBlockHasFuncvarAsyncCall(x.Body, pts)
	}
	return false
}

func ptExprHasFuncvarAsyncCall(e ir.Expr, pts *ir.PointsToInfo) bool {
	if e == nil {
		return false
	}
	switch x := e.(type) {
	case *ir.Call:
		// Direct call to known async func — already handled by analyzeAsync,
		// but check here too for completeness.
		if x.Func != nil && x.Func.IsAsync {
			return true
		}
		// Funcvar call: Func is nil, Callee carries the funcvar expression.
		if x.Func == nil && x.Callee != nil {
			if k, ok := receiverSlotKey(x.Callee); ok {
				// Storage slots carry a precomputed color.
				if color, present := pts.SlotColor[k]; present {
					if color == ir.ColorAsync {
						return true
					}
				} else {
					// Param/return slots: fall back to candidate inspection.
					for _, fn := range pts.Candidates(k) {
						if fn.IsAsync {
							return true
						}
					}
				}
			}
		}
		for _, a := range x.Args {
			if ptExprHasFuncvarAsyncCall(a.Value, pts) {
				return true
			}
		}
		if ptExprHasFuncvarAsyncCall(x.Receiver, pts) {
			return true
		}
	case *ir.Binary:
		return ptExprHasFuncvarAsyncCall(x.Left, pts) || ptExprHasFuncvarAsyncCall(x.Right, pts)
	case *ir.Unary:
		return ptExprHasFuncvarAsyncCall(x.Operand, pts)
	case *ir.Ternary:
		return ptExprHasFuncvarAsyncCall(x.Cond, pts) ||
			ptExprHasFuncvarAsyncCall(x.Then, pts) ||
			ptExprHasFuncvarAsyncCall(x.Else, pts)
	case *ir.Conversion:
		return ptExprHasFuncvarAsyncCall(x.Operand, pts)
	case *ir.Select:
		return ptExprHasFuncvarAsyncCall(x.Operand, pts)
	case *ir.Index:
		return ptExprHasFuncvarAsyncCall(x.Operand, pts) || ptExprHasFuncvarAsyncCall(x.Idx, pts)
	case *ir.ListLit:
		for _, el := range x.Elems {
			if ptExprHasFuncvarAsyncCall(el, pts) {
				return true
			}
		}
	case *ir.StructLit:
		for _, f := range x.Fields {
			if ptExprHasFuncvarAsyncCall(f.Value, pts) {
				return true
			}
		}
	case *ir.Spread:
		return ptExprHasFuncvarAsyncCall(x.Operand, pts)
	case *ir.Lambda:
		if x.Func != nil {
			return ptBlockHasFuncvarAsyncCall(x.Func.Block, pts)
		}
	case *ir.Closure:
		if x.Func != nil {
			return ptBlockHasFuncvarAsyncCall(x.Func.Block, pts)
		}
	}
	return false
}

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
