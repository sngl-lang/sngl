package checker

import (
	"slices"
	"strings"

	"duckfam.us/sngl/ast"
	"duckfam.us/sngl/ir"
)

// checkAsyncRules runs post-analyzeAsync validation rules that reject code
// patterns the lowering passes cannot handle.
//
// Three rules are checked:
//
//  1. Reserved-prefix names: any user-declared func or var whose name starts
//     with "__async_" or "__hoist_" is rejected.  Those prefixes are reserved
//     for the lowering pass.
//
//  2. Parameterized async in reactive context: a non-zero-param func that is
//     async (IsAsync=true) is rejected ONLY IF it is directly called from a
//     reactive expression — i.e., a visual-node prop value in a window or
//     component body.  The settled-state lowering (NoAsyncReactive) emits a
//     single state var per computed func and cannot key per-call-argument.
//     Event handlers, timer bodies, and regular function bodies are NOT
//     reactive contexts; they run as async wrappers and can freely call
//     parameterized async functions.
//
//  3. Async function into a host's callback: a function that waits for an
//     async call, handed to a parameter of a native or an intrinsic whose
//     result the host uses (refuseAsyncIntoHostCallbacks). Anywhere else an
//     async function may go wherever a function may: a func-typed slot is
//     coloured by what flows into it (colorAsyncSlots), and a call through it
//     waits.
func (c *checker) checkAsyncRules() {
	pkg := c.pkg
	if pkg == nil {
		return
	}

	// Rule 1: reserved name prefixes.
	for _, fn := range pkg.Funcs {
		if fn.AST != nil {
			checkReservedPrefixAt(c, fn.Name, fn.AST.Pos)
		}
	}
	for _, comp := range pkg.Components {
		for _, fn := range comp.Funcs {
			if fn.AST != nil {
				checkReservedPrefixAt(c, fn.Name, fn.AST.Pos)
			}
		}
	}
	for _, v := range pkg.Vars {
		if v.AST != nil {
			checkReservedPrefixAt(c, v.Name, varDeclPos(v.AST))
		}
	}
	for _, comp := range pkg.Components {
		for _, v := range comp.Vars {
			if v.AST != nil {
				checkReservedPrefixAt(c, v.Name, varDeclPos(v.AST))
			}
		}
	}

	// Rule 3: an async function into a host's callback.
	c.refuseAsyncIntoHostCallbacks()

	// Rule 2: parameterized async functions called from a reactive context.
	// Collect all funcs directly referenced inside reactive prop expressions.
	reactive := collectReactiveCallees(pkg)
	for _, fn := range allFuncs(pkg) {
		if fn.IsAsync && len(fn.Params) > 0 && fn.AST != nil {
			if reactive[fn] {
				c.error(fn.AST.Pos, "async expression not allowed in parameterized reactive context")
			}
		}
	}
}

// collectReactiveCallees returns the set of *ir.Func directly called from
// reactive prop expressions.  Reactive expressions are the Value fields of
// NodeInst.Props inside window and component bodies.  Event handlers, timer
// bodies, and function bodies are NOT reactive contexts.
//
// Only direct calls are collected (i.e., *ir.Call.Func).  Indirect calls
// through func-typed vars are conservatively excluded (they will be addressed
// when FuncSig gains IsAsync; see Rule 3).
func collectReactiveCallees(pkg *ir.Package) map[*ir.Func]bool {
	out := make(map[*ir.Func]bool)
	collectCalleesInStmts(out, pkg.Body)
	for _, comp := range pkg.Components {
		collectCalleesInStmts(out, comp.Stmts)
	}
	return out
}

// collectCalleesInStmts walks a statement slice looking for NodeInst prop
// expressions and recurses into nested visual nodes.  It does NOT descend
// into event handler bodies or function bodies.
func collectCalleesInStmts(out map[*ir.Func]bool, stmts []ir.Stmt) {
	for _, s := range stmts {
		collectCalleesInStmt(out, s)
	}
}

func collectCalleesInStmt(out map[*ir.Func]bool, s ir.Stmt) {
	switch n := s.(type) {
	case *ir.NodeInst:
		for _, prop := range n.Props {
			// A function literal handed to a prop is a value the component
			// calls where it means to -- a handler, a query's `fetch` -- and
			// the render does not call it.
			if _, isLambda := prop.Value.(*ir.Lambda); isLambda {
				continue
			}
			collectCalleesInExpr(out, prop.Value)
		}
		collectCalleesInStmts(out, n.Children)
	case *ir.If:
		collectCalleesInStmts(out, n.Body)
		collectCalleesInStmts(out, n.Else)
	case *ir.Match:
		for _, arm := range n.Arms {
			collectCalleesInStmts(out, arm.Body)
		}
	case *ir.For:
		collectCalleesInStmts(out, n.Body)
		collectCalleesInStmts(out, n.Else)
	case *ir.SlotInst:
		collectCalleesInStmts(out, n.Children)
	case *ir.ErrorBoundary:
		collectCalleesInStmts(out, n.Children)
	}
}

func collectCalleesInExpr(out map[*ir.Func]bool, e ir.Expr) {
	if e == nil {
		return
	}
	switch x := e.(type) {
	case *ir.Call:
		if x.Func != nil {
			out[x.Func] = true
		}
		collectCalleesInExpr(out, x.Receiver)
		for _, a := range x.Args {
			collectCalleesInExpr(out, a.Value)
		}
	case *ir.Binary:
		collectCalleesInExpr(out, x.Left)
		collectCalleesInExpr(out, x.Right)
	case *ir.Unary:
		collectCalleesInExpr(out, x.Operand)
	case *ir.Ternary:
		collectCalleesInExpr(out, x.Cond)
		collectCalleesInExpr(out, x.Then)
		collectCalleesInExpr(out, x.Else)
	case *ir.MatchExpr:
		collectCalleesInExpr(out, x.Subject)
		for _, arm := range x.Arms {
			collectCalleesInExpr(out, arm.Value)
		}
	case *ir.Conversion:
		collectCalleesInExpr(out, x.Operand)
	case *ir.Select:
		collectCalleesInExpr(out, x.Operand)
	case *ir.Index:
		collectCalleesInExpr(out, x.Operand)
		collectCalleesInExpr(out, x.Idx)
	case *ir.ListLit:
		for _, el := range x.Elems {
			collectCalleesInExpr(out, el)
		}
	case *ir.StructLit:
		for _, f := range x.Fields {
			collectCalleesInExpr(out, f.Value)
		}
	case *ir.Spread:
		collectCalleesInExpr(out, x.Operand)
	case *ir.Lambda:
		if x.Func != nil {
			collectCalleesInFuncBlock(out, x.Func.Block)
		}
	}
}

// collectCalleesInFuncBlock walks a regular function body (not a visual-node
// body) and records every directly-called *ir.Func.  This differs from
// collectCalleesInStmts which only descends into visual-node statement types
// (NodeInst, If, For, …).  Lambda and Closure bodies are regular function
// blocks whose statements are Return, Assign, CallStmt, LocalVar, etc.
func collectCalleesInFuncBlock(out map[*ir.Func]bool, stmts []ir.Stmt) {
	for _, s := range stmts {
		collectCalleesInFuncStmt(out, s)
	}
}

func collectCalleesInFuncStmt(out map[*ir.Func]bool, s ir.Stmt) {
	switch n := s.(type) {
	case *ir.Return:
		collectCalleesInExpr(out, n.Value)
	case *ir.Assign:
		collectCalleesInExpr(out, n.Value)
	case *ir.LocalVar:
		collectCalleesInExpr(out, n.Init)
	case *ir.CallStmt:
		collectCalleesInExpr(out, n.Call)
	case *ir.If:
		collectCalleesInExpr(out, n.Cond)
		collectCalleesInFuncBlock(out, n.Body)
		collectCalleesInFuncBlock(out, n.Else)
	case *ir.Match:
		collectCalleesInExpr(out, n.Subject)
		for _, arm := range n.Arms {
			collectCalleesInFuncBlock(out, arm.Body)
		}
	case *ir.For:
		collectCalleesInExpr(out, n.Iter)
		collectCalleesInFuncBlock(out, n.Body)
		collectCalleesInFuncBlock(out, n.Else)
	}
}

func checkReservedPrefixAt(c *checker, name string, pos ast.Pos) {
	if strings.HasPrefix(name, "__async_") || strings.HasPrefix(name, "__hoist_") {
		c.error(pos, "name %q uses reserved prefix", name)
	}
}

func varDeclPos(stmt ast.Stmt) ast.Pos {
	if stmt == nil {
		return ast.Pos{}
	}
	if p := stmt.StmtPos(); p != nil {
		return *p
	}
	return ast.Pos{}
}

// refuseAsyncIntoHostCallbacks is rule 3: an async function handed to a
// callback a host calls -- a parameter of a native or an intrinsic -- whose
// result the host uses. The host calls it without waiting, so what it reads is
// the wait rather than the answer. A callback whose result is ignored may wait.
func (c *checker) refuseAsyncIntoHostCallbacks() {
	pts := c.pkg.PointsTo
	_ = ir.Walk(c.pkg, func(n ir.Node) error {
		call, ok := n.(*ir.Call)
		if !ok || call.Func == nil || !hostCalls(call.Func) {
			return nil
		}
		params := call.Func.Params
		args := call.Args
		// A method whose parameters do not list its receiver is handed it
		// first all the same.
		if len(args) == len(params)+1 {
			args = args[1:]
		}
		for i, a := range args {
			if i >= len(params) {
				break
			}
			t := params[i].Type
			if t == nil || t.Kind != ir.TypeFunc || t.Sig == nil || t.Sig.Return == nil || t.Sig.Return.Kind == ir.TypeVoid {
				continue
			}
			what := asyncValue(a.Value, pts)
			if what == "" {
				continue
			}
			pos := ast.Pos{}
			switch v := a.Value.(type) {
			case *ir.Ident:
				if v.AST != nil {
					pos = v.AST.Pos
				}
			case *ir.Lambda:
				if v.AST != nil {
					pos = v.AST.Pos
				}
			}
			if !pos.IsSet() && call.AST != nil {
				pos = call.AST.Pos
			}
			c.error(pos, "%s, so it cannot be handed to %s, which uses what it returns without waiting", what, call.Func.Name)
		}
		return nil
	})
}

// hostCalls reports whether fn is the host's: a native or an intrinsic
// declared with no body, which calls a function it is handed itself.
func hostCalls(fn *ir.Func) bool {
	return (fn.Intrinsic != "" || fn.Foreign.Name != "") && !hasDeclaredBody(fn)
}

// asyncValue says why e, a function handed somewhere, may wait for an async
// call, or "" when it cannot.
func asyncValue(e ir.Expr, pts *ir.PointsToInfo) string {
	switch x := e.(type) {
	case *ir.Lambda:
		if x.Func != nil && (x.Func.IsAsync || ir.BlockHasAsyncCall(x.Func.Block)) {
			return "this function waits for an async call"
		}
		return ""
	case *ir.Ident:
		if fn, ok := x.Sym.(*ir.Func); ok {
			if fn.IsAsync {
				return fn.Name + " waits for an async call"
			}
			return ""
		}
		if t := x.Type; t != nil && t.Sig != nil && t.Sig.Color == ir.ColorAsync {
			return x.Name + " may hold a function that waits for an async call"
		}
		if k, ok := ir.CalleeSlotKey(x); ok && pts != nil && slices.ContainsFunc(pts.Candidates(k), func(f *ir.Func) bool { return f.IsAsync }) {
			return x.Name + " may hold a function that waits for an async call"
		}
	}
	return ""
}
