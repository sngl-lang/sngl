package checker

import (
	"strings"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/ir"
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
//  3. Async function into sync slot: deferred.  The ir.FuncSig / ir.Type for
//     TypeFunc carries no IsAsync/color field, so there is no way to inspect
//     whether the declared parameter/field type expects a sync vs async func.
//     This rule will be implementable once FuncSig gains an IsAsync flag.
//     See spec §5 rule 3 (closure points-to).
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
		collectCalleesInStmts(out, comp.Body)
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
			collectCalleesInExpr(out, prop.Value)
		}
		collectCalleesInStmts(out, n.Children)
	case *ir.If:
		collectCalleesInStmts(out, n.Body)
		collectCalleesInStmts(out, n.Else)
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
