// async.go: shared "does this body transitively contain an async call"
// helpers. Used by the checker's color-propagation pass and by the
// html platform's codegen to decide async-keyword placement.
package ir

import "slices"

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
	case *For:
		return ExprHasAsyncCall(x.Iter) || BlockHasAsyncCall(x.Body) || BlockHasAsyncCall(x.Else)
	case *PlatformFilter:
		return BlockHasAsyncCall(x.Body)
	}
	return false
}

func ExprHasAsyncCall(e Expr) bool {
	if e == nil {
		return false
	}
	switch x := e.(type) {
	case *Call:
		if x.Func != nil && x.Func.IsAsync {
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
	case *Lambda:
		return BlockHasAsyncCall(x.Func.Block)
	case *Closure:
		if x.Func != nil {
			return BlockHasAsyncCall(x.Func.Block)
		}
	}
	return false
}
