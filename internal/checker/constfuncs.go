package checker

import (
	"duckfam.us/sngl/ast"
	"duckfam.us/sngl/ir"
)

// checkConstFuncs holds every bodied `const func` to what the prefix says:
// its value depends on its arguments and on nothing else. The body may read
// its parameters, its own locals and constants, and call only other const
// funcs; its locals are the call's own, so writing them is allowed.
//
// A bodyless const func is trusted -- a host identifier, an intrinsic, an
// import -- because nothing in the program says what the host does, and
// buildFunc has already recorded it pure.
//
// Run after the purity fixpoint, where the const(...) assertions are, so what
// it reports is judged against every declaration's final Const. Each body is
// asked once, at its first offence: past it the function is already not what
// it says, and a list of every read would bury the one that matters.
func (c *checker) checkConstFuncs(funcs []*ir.Func) {
	for _, fn := range funcs {
		if fn == nil || !fn.Const || fn.AST == nil || !funcHasBody(fn.AST) {
			continue
		}
		c.checkConstFuncBody(fn)
	}
}

func (c *checker) checkConstFuncBody(fn *ir.Func) {
	locals := map[*ir.Var]bool{}
	_ = ir.Walk(fn.Block, func(n ir.Node) error {
		if lv, ok := n.(*ir.LocalVar); ok && lv.Sym != nil {
			locals[lv.Sym] = true
		}
		return nil
	})
	name := funcDeclName(fn)
	var reported bool
	report := func(pos ast.Pos, format string, args ...any) {
		if reported {
			return
		}
		reported = true
		if !pos.IsSet() {
			pos = funcDeclPos(fn)
		}
		c.error(pos, "const func %s "+format, append([]any{name}, args...)...)
	}
	_ = ir.Walk(fn.Block, func(n ir.Node) error {
		if reported {
			return ir.SkipAll
		}
		switch x := n.(type) {
		case *ir.Assign:
			if v := constFuncWriteRoot(x.Target); v != nil && !v.IsConst && !locals[v] {
				report(exprIdentPos(x.Target), "writes var %q", v.Name)
			}
		case *ir.Toggle:
			if v := constFuncWriteRoot(x.Target); v != nil && !v.IsConst && !locals[v] {
				report(exprIdentPos(x.Target), "writes var %q", v.Name)
			}
		case *ir.Emit:
			report(emitPos(x), "emits %q: a const func has no events to fire", x.Name)
		case *ir.Ident:
			pos := identPos(x)
			switch s := x.Sym.(type) {
			case *ir.Var:
				if !s.IsConst && !locals[s] {
					report(pos, "reads var %q", s.Name)
				}
			case *ir.Context:
				report(pos, "reads context %q", s.Name)
			}
		case *ir.Call:
			if x.Func == nil || x.Func.Const || x.Func == fn {
				return nil
			}
			if x.Event != "" {
				report(callExprPos(x), "emits %q: a const func has no events to fire", x.Event)
				return nil
			}
			callee := funcDeclName(x.Func)
			report(callExprPos(x), "calls %s, which is not const (declare it const func %s)", callee, callee)
		}
		return nil
	})
}

func identPos(x *ir.Ident) ast.Pos {
	if x.AST != nil {
		return x.AST.Pos
	}
	return ast.Pos{}
}

func callExprPos(x *ir.Call) ast.Pos {
	if x.AST != nil {
		return x.AST.Pos
	}
	return ast.Pos{}
}

func emitPos(x *ir.Emit) ast.Pos {
	if x.AST != nil {
		if p := stmtPos(x.AST); p != nil {
			return *p
		}
	}
	return ast.Pos{}
}

// constFuncWriteRoot is the var an assignment target is rooted at, through
// fields and indexes, or nil when it is rooted at anything else.
func constFuncWriteRoot(e ir.Expr) *ir.Var {
	for {
		switch x := e.(type) {
		case *ir.Ident:
			v, _ := x.Sym.(*ir.Var)
			return v
		case *ir.Select:
			e = x.Operand
		case *ir.Index:
			e = x.Operand
		default:
			return nil
		}
	}
}

func exprIdentPos(e ir.Expr) ast.Pos {
	for {
		switch x := e.(type) {
		case *ir.Ident:
			return identPos(x)
		case *ir.Select:
			e = x.Operand
		case *ir.Index:
			e = x.Operand
		default:
			return ast.Pos{}
		}
	}
}
