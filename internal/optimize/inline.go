package optimize

import (
	"git.duckfam.us/jonathan/sngl/ir"
)

// inlineCall attempts to replace a pure function call with its inlined body.
// Returns nil if inlining is not applicable.
func inlineCall(call *ir.Call, ctx *evalCtx) ir.Expr {
	f := call.Func
	if f == nil || f.Purity != ir.PurityPure {
		return nil
	}
	if len(f.TypeParams) > 0 {
		return nil // skip generic functions
	}
	if len(f.Block) != 1 {
		return nil // only inline single-expression functions
	}
	ret, ok := f.Block[0].(*ir.Return)
	if !ok || ret.Value == nil {
		return nil
	}
	if callsFunc(ret.Value, f) {
		return nil // skip recursive functions
	}

	// Build substitution map: param → argument expression.
	subs := make(map[*ir.Param]ir.Expr, len(f.Params))
	for i, p := range f.Params {
		if i < len(call.Args) {
			subs[p] = call.Args[i].Value
		} else if p.Default != nil {
			subs[p] = p.Default
		} else {
			return nil // missing argument
		}
	}

	// Clone the return expression and substitute parameters.
	result := cloneExpr(ret.Value)
	result = substituteParams(result, subs)
	return result
}

// callsFunc reports whether the expression contains a call to the given function.
func callsFunc(e ir.Expr, target *ir.Func) bool {
	if e == nil {
		return false
	}
	switch x := e.(type) {
	case *ir.Call:
		if x.Func == target {
			return true
		}
		if callsFunc(x.Receiver, target) {
			return true
		}
		for _, a := range x.Args {
			if callsFunc(a.Value, target) {
				return true
			}
		}
	case *ir.Binary:
		return callsFunc(x.Left, target) || callsFunc(x.Right, target)
	case *ir.Unary:
		return callsFunc(x.Operand, target)
	case *ir.Ternary:
		return callsFunc(x.Cond, target) || callsFunc(x.Then, target) || callsFunc(x.Else, target)
	case *ir.Conversion:
		return callsFunc(x.Operand, target)
	case *ir.Select:
		return callsFunc(x.Operand, target)
	case *ir.Index:
		return callsFunc(x.Operand, target) || callsFunc(x.Idx, target)
	case *ir.ListLit:
		for _, el := range x.Elems {
			if callsFunc(el, target) {
				return true
			}
		}
	case *ir.StructLit:
		for _, f := range x.Fields {
			if callsFunc(f.Value, target) {
				return true
			}
		}
	case *ir.Spread:
		return callsFunc(x.Operand, target)
	case *ir.Lambda:
		for _, s := range x.Func.Block {
			if callsFuncStmt(s, target) {
				return true
			}
		}
	}
	return false
}

func callsFuncStmt(s ir.Stmt, target *ir.Func) bool {
	switch n := s.(type) {
	case *ir.Return:
		return callsFunc(n.Value, target)
	case *ir.If:
		if callsFunc(n.Cond, target) {
			return true
		}
		for _, s := range n.Body {
			if callsFuncStmt(s, target) {
				return true
			}
		}
		for _, s := range n.Else {
			if callsFuncStmt(s, target) {
				return true
			}
		}
	case *ir.Assign:
		return callsFunc(n.Value, target)
	case *ir.CallStmt:
		if n.Call != nil {
			return callsFunc(n.Call.Receiver, target) || callsFuncInArgs(n.Call.Args, target)
		}
	case *ir.LocalVar:
		return callsFunc(n.Init, target)
	case *ir.Emit:
		return callsFuncInArgs(n.Args, target)
	}
	return false
}

func callsFuncInArgs(args []ir.CallArg, target *ir.Func) bool {
	for _, a := range args {
		if callsFunc(a.Value, target) {
			return true
		}
	}
	return false
}

// substituteParams walks the expression tree, replacing parameter references
// with the corresponding argument expressions.
func substituteParams(e ir.Expr, subs map[*ir.Param]ir.Expr) ir.Expr {
	if e == nil {
		return nil
	}
	switch x := e.(type) {
	case *ir.Ident:
		if p, ok := x.Sym.(*ir.Param); ok {
			if arg, found := subs[p]; found {
				return cloneExpr(arg)
			}
		}
		return x
	case *ir.Binary:
		x.Left = substituteParams(x.Left, subs)
		x.Right = substituteParams(x.Right, subs)
	case *ir.Unary:
		x.Operand = substituteParams(x.Operand, subs)
	case *ir.Ternary:
		x.Cond = substituteParams(x.Cond, subs)
		x.Then = substituteParams(x.Then, subs)
		x.Else = substituteParams(x.Else, subs)
	case *ir.Call:
		x.Receiver = substituteParams(x.Receiver, subs)
		for i := range x.Args {
			x.Args[i].Value = substituteParams(x.Args[i].Value, subs)
		}
	case *ir.Conversion:
		x.Operand = substituteParams(x.Operand, subs)
	case *ir.Select:
		x.Operand = substituteParams(x.Operand, subs)
	case *ir.Index:
		x.Operand = substituteParams(x.Operand, subs)
		x.Idx = substituteParams(x.Idx, subs)
	case *ir.ListLit:
		for i := range x.Elems {
			x.Elems[i] = substituteParams(x.Elems[i], subs)
		}
	case *ir.StructLit:
		for i := range x.Fields {
			x.Fields[i].Value = substituteParams(x.Fields[i].Value, subs)
		}
	case *ir.Spread:
		x.Operand = substituteParams(x.Operand, subs)
	}
	return e
}

// cloneExpr creates a deep copy of an expression tree.
func cloneExpr(e ir.Expr) ir.Expr {
	if e == nil {
		return nil
	}
	switch x := e.(type) {
	case *ir.Literal:
		cp := *x
		return &cp
	case *ir.Ident:
		cp := *x
		return &cp
	case *ir.Binary:
		cp := *x
		cp.Left = cloneExpr(x.Left)
		cp.Right = cloneExpr(x.Right)
		return &cp
	case *ir.Unary:
		cp := *x
		cp.Operand = cloneExpr(x.Operand)
		return &cp
	case *ir.Ternary:
		cp := *x
		cp.Cond = cloneExpr(x.Cond)
		cp.Then = cloneExpr(x.Then)
		cp.Else = cloneExpr(x.Else)
		return &cp
	case *ir.Call:
		cp := *x
		cp.Receiver = cloneExpr(x.Receiver)
		cp.Args = make([]ir.CallArg, len(x.Args))
		for i, a := range x.Args {
			cp.Args[i] = ir.CallArg{Name: a.Name, Value: cloneExpr(a.Value)}
		}
		return &cp
	case *ir.Conversion:
		cp := *x
		cp.Operand = cloneExpr(x.Operand)
		return &cp
	case *ir.Select:
		cp := *x
		cp.Operand = cloneExpr(x.Operand)
		return &cp
	case *ir.Index:
		cp := *x
		cp.Operand = cloneExpr(x.Operand)
		cp.Idx = cloneExpr(x.Idx)
		return &cp
	case *ir.ListLit:
		cp := *x
		cp.Elems = make([]ir.Expr, len(x.Elems))
		for i, el := range x.Elems {
			cp.Elems[i] = cloneExpr(el)
		}
		return &cp
	case *ir.StructLit:
		cp := *x
		cp.Fields = make([]ir.FieldInit, len(x.Fields))
		for i, f := range x.Fields {
			cp.Fields[i] = ir.FieldInit{Name: f.Name, Value: cloneExpr(f.Value), Spread: f.Spread}
		}
		return &cp
	case *ir.Spread:
		cp := *x
		cp.Operand = cloneExpr(x.Operand)
		return &cp
	case *ir.Lambda:
		// Don't deep-clone lambda bodies — treat as opaque.
		cp := *x
		return &cp
	}
	return e
}

// cloneStmt creates a deep copy of a statement.
func cloneStmt(s ir.Stmt) ir.Stmt {
	if s == nil {
		return nil
	}
	switch n := s.(type) {
	case *ir.NodeInst:
		cp := *n
		cp.Props = make([]ir.Arg, len(n.Props))
		for i, p := range n.Props {
			cp.Props[i] = ir.Arg{Name: p.Name, Value: cloneExpr(p.Value)}
		}
		cp.Handlers = make([]ir.EventHandler, len(n.Handlers))
		copy(cp.Handlers, n.Handlers)
		cp.Children = cloneStmts(n.Children)
		return &cp
	case *ir.If:
		cp := *n
		cp.Cond = cloneExpr(n.Cond)
		cp.Body = cloneStmts(n.Body)
		cp.Else = cloneStmts(n.Else)
		return &cp
	case *ir.For:
		cp := *n
		cp.Iter = cloneExpr(n.Iter)
		cp.Body = cloneStmts(n.Body)
		cp.Else = cloneStmts(n.Else)
		return &cp
	case *ir.PlatformFilter:
		cp := *n
		cp.Body = cloneStmts(n.Body)
		return &cp
	case *ir.Assign:
		cp := *n
		cp.Value = cloneExpr(n.Value)
		return &cp
	case *ir.CallStmt:
		cp := *n
		if n.Call != nil {
			call := *n.Call
			call.Receiver = cloneExpr(n.Call.Receiver)
			call.Args = make([]ir.CallArg, len(n.Call.Args))
			for i, a := range n.Call.Args {
				call.Args[i] = ir.CallArg{Name: a.Name, Value: cloneExpr(a.Value)}
			}
			cp.Call = &call
		}
		return &cp
	case *ir.LocalVar:
		cp := *n
		cp.Init = cloneExpr(n.Init)
		return &cp
	case *ir.Return:
		cp := *n
		cp.Value = cloneExpr(n.Value)
		return &cp
	case *ir.Emit:
		cp := *n
		cp.Args = make([]ir.CallArg, len(n.Args))
		for i, a := range n.Args {
			cp.Args[i] = ir.CallArg{Name: a.Name, Value: cloneExpr(a.Value)}
		}
		return &cp
	case *ir.Toggle:
		cp := *n
		cp.Target = cloneExpr(n.Target)
		return &cp
	case *ir.SlotInst:
		cp := *n
		cp.Children = cloneStmts(n.Children)
		return &cp
	case *ir.Window:
		cp := *n
		cp.Href = cloneExpr(n.Href)
		cp.Title = cloneExpr(n.Title)
		cp.Favicon = cloneExpr(n.Favicon)
		cp.Body = cloneStmts(n.Body)
		return &cp
	}
	return s
}

func cloneStmts(stmts []ir.Stmt) []ir.Stmt {
	if stmts == nil {
		return nil
	}
	out := make([]ir.Stmt, len(stmts))
	for i, s := range stmts {
		out[i] = cloneStmt(s)
	}
	return out
}
