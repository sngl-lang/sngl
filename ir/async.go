package ir

import "slices"

// BlockHasFuncvarAsyncCall returns true if any statement in stmts contains a
// funcvar call whose slot color is Async (or whose candidates include an async
// func for uncolored slots like SlotParam/SlotReturn). Falls back to
// BlockHasAsyncCall when pts is nil.
func BlockHasFuncvarAsyncCall(stmts []Stmt, pts *PointsToInfo) bool {
	if pts == nil {
		return BlockHasAsyncCall(stmts)
	}
	for _, s := range stmts {
		if stmtHasFuncvarAsyncCall(s, pts) {
			return true
		}
	}
	return false
}

func stmtHasFuncvarAsyncCall(s Stmt, pts *PointsToInfo) bool {
	switch x := s.(type) {
	case *CallStmt:
		return exprHasFuncvarAsyncCall(x.Call, pts)
	case *Assign:
		return exprHasFuncvarAsyncCall(x.Target, pts) || exprHasFuncvarAsyncCall(x.Value, pts)
	case *LocalVar:
		return exprHasFuncvarAsyncCall(x.Init, pts)
	case *Return:
		return exprHasFuncvarAsyncCall(x.Value, pts)
	case *If:
		return exprHasFuncvarAsyncCall(x.Cond, pts) ||
			BlockHasFuncvarAsyncCall(x.Body, pts) ||
			BlockHasFuncvarAsyncCall(x.Else, pts)
	case *Match:
		if exprHasFuncvarAsyncCall(x.Subject, pts) {
			return true
		}
		for _, arm := range x.Arms {
			if BlockHasFuncvarAsyncCall(arm.Body, pts) || slices.ContainsFunc(arm.Patterns, func(p Expr) bool { return exprHasFuncvarAsyncCall(p, pts) }) {
				return true
			}
		}
	case *For:
		return exprHasFuncvarAsyncCall(x.Iter, pts) ||
			BlockHasFuncvarAsyncCall(x.Body, pts) ||
			BlockHasFuncvarAsyncCall(x.Else, pts)
	}
	return false
}

// CalleeSlotKey resolves a funcvar callee expression to its PointsToKey.
// Used by any pass that needs to look up the points-to slot for a funcvar
// invocation (checker async analysis, JS codegen, etc.).
func CalleeSlotKey(e Expr) (PointsToKey, bool) {
	switch x := e.(type) {
	case *Ident:
		switch sym := x.Sym.(type) {
		case *Var:
			return SlotVarKey(sym), true
		case *Param:
			return SlotParamKey(sym), true
		}
	case *Select:
		if x.Operand != nil {
			t := x.Operand.ExprType()
			if t != nil && t.Kind == TypeStruct {
				return SlotFieldKey(t, x.Field), true
			}
		}
	case *Index:
		if x.Operand != nil {
			t := x.Operand.ExprType()
			if t != nil && t.Kind == TypeList {
				return SlotListElemKey(t), true
			}
		}
	case *Call:
		if x.Func != nil {
			return SlotReturnKey(x.Func), true
		}
	}
	return PointsToKey{}, false
}

func exprHasFuncvarAsyncCall(e Expr, pts *PointsToInfo) bool {
	if e == nil {
		return false
	}
	switch x := e.(type) {
	case *Call:
		// Direct call to a known async func, or through a value whose
		// signature is coloured async.
		if !x.Detached && (x.Func != nil && x.Func.IsAsync || CallsAsyncValue(x)) {
			return true
		}
		// Funcvar call: Func is nil, Callee carries the funcvar expression.
		if !x.Detached && x.Func == nil && x.Callee != nil {
			if k, ok := CalleeSlotKey(x.Callee); ok {
				if color, present := pts.SlotColor[k]; present {
					if color == ColorAsync {
						return true
					}
				} else {
					for _, fn := range pts.Candidates(k) {
						if fn.IsAsync {
							return true
						}
					}
				}
			}
		}
		for _, a := range x.Args {
			if exprHasFuncvarAsyncCall(a.Value, pts) {
				return true
			}
		}
		if exprHasFuncvarAsyncCall(x.Receiver, pts) {
			return true
		}
	case *Binary:
		return exprHasFuncvarAsyncCall(x.Left, pts) || exprHasFuncvarAsyncCall(x.Right, pts)
	case *Unary:
		return exprHasFuncvarAsyncCall(x.Operand, pts)
	case *Ternary:
		return exprHasFuncvarAsyncCall(x.Cond, pts) ||
			exprHasFuncvarAsyncCall(x.Then, pts) ||
			exprHasFuncvarAsyncCall(x.Else, pts)
	case *MatchExpr:
		if exprHasFuncvarAsyncCall(x.Subject, pts) {
			return true
		}
		for _, arm := range x.Arms {
			if exprHasFuncvarAsyncCall(arm.Value, pts) || slices.ContainsFunc(arm.Patterns, func(p Expr) bool { return exprHasFuncvarAsyncCall(p, pts) }) {
				return true
			}
		}
	case *Conversion:
		return exprHasFuncvarAsyncCall(x.Operand, pts)
	case *Select:
		return exprHasFuncvarAsyncCall(x.Operand, pts)
	case *Index:
		return exprHasFuncvarAsyncCall(x.Operand, pts) || exprHasFuncvarAsyncCall(x.Idx, pts)
	case *ListLit:
		for _, el := range x.Elems {
			if exprHasFuncvarAsyncCall(el, pts) {
				return true
			}
		}
	case *StructLit:
		for _, f := range x.Fields {
			if exprHasFuncvarAsyncCall(f.Value, pts) {
				return true
			}
		}
	case *Spread:
		return exprHasFuncvarAsyncCall(x.Operand, pts)
	case *Lambda, *Closure:
		// Constructing a closure is not calling it. What blocks is whatever
		// eventually invokes the closure, and the closure's own Func is
		// coloured in its own right -- so descending here colours the
		// *enclosing* function for work it only handed over. On Go that turned
		// `handle = every(d, func(){ …blocking… })` into an async statement,
		// which passAsyncOffload then refused for sitting inside the `if`
		// its caller wrapped it in.
	}
	return false
}

func BlockHasAsyncCall(stmts []Stmt) bool {
	return slices.ContainsFunc(stmts, StmtHasAsyncCall)
}

func StmtHasAsyncCall(s Stmt) bool {
	switch x := s.(type) {
	case *CallStmt:
		return ExprHasAsyncCall(x.Call)
	case *Assign:
		return ExprHasAsyncCall(x.Value)
	case *LocalVar:
		return ExprHasAsyncCall(x.Init)
	case *Return:
		return ExprHasAsyncCall(x.Value)
	case *If:
		return ExprHasAsyncCall(x.Cond) || BlockHasAsyncCall(x.Body) || BlockHasAsyncCall(x.Else)
	case *Match:
		if ExprHasAsyncCall(x.Subject) {
			return true
		}
		for _, arm := range x.Arms {
			if BlockHasAsyncCall(arm.Body) || slices.ContainsFunc(arm.Patterns, ExprHasAsyncCall) {
				return true
			}
		}
	case *For:
		return ExprHasAsyncCall(x.Iter) || BlockHasAsyncCall(x.Body) || BlockHasAsyncCall(x.Else)
	}
	return false
}

func ExprHasAsyncCall(e Expr) bool {
	if e == nil {
		return false
	}
	switch x := e.(type) {
	case *Call:
		if !x.Detached && (x.Func != nil && x.Func.IsAsync || CallsAsyncValue(x)) {
			return true
		}
		for _, a := range x.Args {
			if ExprHasAsyncCall(a.Value) {
				return true
			}
		}
		if x.Receiver != nil && ExprHasAsyncCall(x.Receiver) {
			return true
		}
	case *Binary:
		return ExprHasAsyncCall(x.Left) || ExprHasAsyncCall(x.Right)
	case *Unary:
		return ExprHasAsyncCall(x.Operand)
	case *Ternary:
		return ExprHasAsyncCall(x.Cond) || ExprHasAsyncCall(x.Then) || ExprHasAsyncCall(x.Else)
	case *MatchExpr:
		if ExprHasAsyncCall(x.Subject) {
			return true
		}
		for _, arm := range x.Arms {
			if ExprHasAsyncCall(arm.Value) || slices.ContainsFunc(arm.Patterns, ExprHasAsyncCall) {
				return true
			}
		}
	case *Conversion:
		return ExprHasAsyncCall(x.Operand)
	case *Select:
		return ExprHasAsyncCall(x.Operand)
	case *Index:
		return ExprHasAsyncCall(x.Operand) || ExprHasAsyncCall(x.Idx)
	case *ListLit:
		if slices.ContainsFunc(x.Elems, ExprHasAsyncCall) {
			return true
		}
	case *StructLit:
		for _, f := range x.Fields {
			if ExprHasAsyncCall(f.Value) {
				return true
			}
		}
	case *Spread:
		return ExprHasAsyncCall(x.Operand)
	case *Lambda, *Closure:
		// See exprHasFuncvarAsyncCall: a closure is handed over, not called.
	}
	return false
}

// CallsAsyncValue reports whether call is a call through a func value whose
// signature the checker coloured async: an async function may flow into the
// slot it reads (checker.colorAsyncSlots).
//
// A function literal called where it stands -- a func-typed prop's argument
// substituted for the parameter its component calls -- waits where its own
// function does.
func CallsAsyncValue(call *Call) bool {
	if call.Func != nil || call.Callee == nil {
		return false
	}
	switch x := call.Callee.(type) {
	case *Lambda:
		if x.Func != nil && x.Func.IsAsync {
			return true
		}
	case *Closure:
		if x.Func != nil && x.Func.IsAsync {
			return true
		}
	}
	t := call.Callee.ExprType()
	return t != nil && t.Sig != nil && t.Sig.Color == ColorAsync
}
