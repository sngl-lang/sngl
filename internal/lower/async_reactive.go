package lower

import (
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/ir"
)

var passAsyncReactive = pass{
	name:    "NoAsyncReactive",
	enabled: func(c Features) bool { return !c.AsyncReactive },
	apply:   lowerAsyncReactive,
}

// lowerAsyncReactive runs two phases:
//
//  1. Hoist inline async subexpressions inside visual-node props into
//     synthetic anonymous zero-arg computed funcs (__hoist_N).
//  2. Lower every reactive async computed (named + just-hoisted) to a
//     settle-state-var (__async_X) + kicker ($compute_X).
func lowerAsyncReactive(pkg *ir.Package, _ Features, _ Options) error {
	if pkg == nil {
		return nil
	}

	// Phase 1: hoist inline async subexpressions into synthetic computeds.
	if err := hoistInlineAsyncReactive(pkg); err != nil {
		return err
	}

	// Phase 2: lower every reactive async computed (named + just-hoisted).
	// Snapshot pkg.Funcs length before to avoid iterating over funcs we add
	// as kickers (kickers are not computeds themselves).
	snapshot := make([]*ir.Func, len(pkg.Funcs))
	copy(snapshot, pkg.Funcs)
	for _, fn := range snapshot {
		if !isReactiveAsyncComputed(fn) {
			continue
		}
		if err := lowerNamedAsyncComputed(pkg, fn); err != nil {
			return err
		}
	}
	return nil
}

// isReactiveAsyncComputed reports whether fn is a named zero-arg computed
// whose single-statement body transitively calls an async function.
// Uses a structural test: a single Return statement with an async-calling value.
// Synthetic hoisted funcs (no AST) are also matched by this structural test.
func isReactiveAsyncComputed(fn *ir.Func) bool {
	if fn == nil || fn.IsTest || len(fn.Params) != 0 || fn.Name == "" {
		return false
	}
	// Checker stores expression bodies as a single Return statement.
	// Synthetic hoisted funcs are also shaped this way.
	if len(fn.Block) != 1 {
		return false
	}
	ret, ok := fn.Block[0].(*ir.Return)
	if !ok || ret.Value == nil {
		return false
	}
	// Must be an async expression body — either from user AST or synthetic hoist.
	// For user-defined computed funcs, additionally require AST body to be set
	// (distinguishes a computed from a regular function with a coincidental
	// single-return body).
	if !ir.ExprHasAsyncCall(ret.Value) {
		return false
	}
	// Accept if: (a) it's a synthetic hoist (no AST, name starts with "__hoist_"),
	// or (b) it has AST.Body.
	if fn.AST == nil {
		return strings.HasPrefix(fn.Name, "__hoist_")
	}
	return fn.AST.Body != nil
}

// hoistInlineAsyncReactive walks every reactive expression context in the
// package's visual nodes (NodeInst props in windows and components) and hoists
// maximal async-bearing subexpressions into synthetic __hoist_N zero-arg
// computed funcs appended to pkg.Funcs.
//
// A subexpression is hoistable if none of its free identifiers are locals
// (*ir.Param or *ir.LoopVar). When a local is captured, the subexpression is
// left alone, for the checker rule to reject.
//
// Hoisting is done depth-first with a maximality rule: the largest valid
// async-bearing subtree is hoisted; its interior is not separately hoisted.
func hoistInlineAsyncReactive(pkg *ir.Package) error {
	h := &hoister{pkg: pkg}
	hoistInStmts(h, pkg.Body)
	// Walk windows.
	for _, w := range pkg.Windows {
		hoistInStmts(h, w.Children)
	}
	// Walk components.
	for _, comp := range pkg.Components {
		hoistInStmts(h, comp.Body)
	}
	return nil
}

// hoister holds the counter and package reference for the hoisting phase.
type hoister struct {
	pkg     *ir.Package
	counter int
}

// freshHoistName returns the next synthetic hoist name (__hoist_0, __hoist_1, …).
func (h *hoister) freshHoistName() string {
	name := "__hoist_" + strconv.Itoa(h.counter)
	h.counter++
	return name
}

// hoistInStmts walks statement slices looking for NodeInst prop expressions.
func hoistInStmts(h *hoister, stmts []ir.Stmt) {
	for _, s := range stmts {
		hoistInStmt(h, s)
	}
}

func hoistInStmt(h *hoister, s ir.Stmt) {
	switch n := s.(type) {
	case *ir.NodeInst:
		// Hoist each prop value.
		for i := range n.Props {
			n.Props[i].Value = hoistExpr(h, n.Props[i].Value)
		}
		hoistInStmts(h, n.Children)
	case *ir.If:
		hoistInStmts(h, n.Body)
		hoistInStmts(h, n.Else)
	case *ir.For:
		hoistInStmts(h, n.Body)
		hoistInStmts(h, n.Else)
	case *ir.SlotInst:
		hoistInStmts(h, n.Children)
	case *ir.ErrorBoundary:
		hoistInStmts(h, n.Children)
	case *ir.Assign, *ir.LocalVar, *ir.Return, *ir.CallStmt, *ir.Emit, *ir.Toggle, *ir.ContextProvider,
		*ir.Break, *ir.Continue:
		// Hoister targets only NodeInst prop expressions; non-visual stmts
		// don't host hoistable async subtrees.
	default:
		panic(fmt.Sprintf("hoistInStmt: unhandled %T", n))
	}
}

// hoistExpr attempts to hoist async subexpressions in e. Returns the
// (possibly rewritten) expression. When e itself is hoistable (async and no
// locals), it returns a Call to a new __hoist_N func. Otherwise it recurses
// into children that contain async subexpressions, replacing those.
func hoistExpr(h *hoister, e ir.Expr) ir.Expr {
	if !ir.ExprHasAsyncCall(e) {
		return e
	}
	// If this entire expression is hoistable, hoist it.
	if !exprHasLocals(e) {
		return synthesizeHoist(h, e)
	}
	// Cannot hoist the whole expression — recurse into async-bearing children.
	return hoistChildren(h, e)
}

// synthesizeHoist creates a __hoist_N func whose body returns e, appends it
// to pkg.Funcs, and returns a Call to it.
func synthesizeHoist(h *hoister, e ir.Expr) ir.Expr {
	name := h.freshHoistName()
	retType := e.ExprType()
	fn := &ir.Func{
		Name:    name,
		IsAsync: true,
		Return:  retType,
		Block: []ir.Stmt{
			&ir.Return{Value: e},
		},
		// AST intentionally nil — isReactiveAsyncComputed accepts nil AST for synthetic hoists.
	}
	h.pkg.Funcs = append(h.pkg.Funcs, fn)
	return &ir.Call{
		Type: retType,
		Func: fn,
	}
}

// hoistChildren recurses into the children of e that contain async calls,
// hoisting valid subtrees within them. Returns a structurally-equivalent
// expression with those subtrees replaced.
func hoistChildren(h *hoister, e ir.Expr) ir.Expr {
	switch x := e.(type) {
	case *ir.Binary:
		cp := *x
		cp.Left = hoistExpr(h, x.Left)
		cp.Right = hoistExpr(h, x.Right)
		return &cp
	case *ir.Unary:
		cp := *x
		cp.Operand = hoistExpr(h, x.Operand)
		return &cp
	case *ir.Ternary:
		cp := *x
		cp.Cond = hoistExpr(h, x.Cond)
		cp.Then = hoistExpr(h, x.Then)
		cp.Else = hoistExpr(h, x.Else)
		return &cp
	case *ir.Call:
		cp := *x
		if cp.Receiver != nil {
			cp.Receiver = hoistExpr(h, x.Receiver)
		}
		cp.Args = make([]ir.CallArg, len(x.Args))
		for i, a := range x.Args {
			cp.Args[i] = a
			cp.Args[i].Value = hoistExpr(h, a.Value)
		}
		return &cp
	case *ir.Conversion:
		cp := *x
		cp.Operand = hoistExpr(h, x.Operand)
		return &cp
	case *ir.Select:
		cp := *x
		cp.Operand = hoistExpr(h, x.Operand)
		return &cp
	case *ir.Index:
		cp := *x
		cp.Operand = hoistExpr(h, x.Operand)
		cp.Idx = hoistExpr(h, x.Idx)
		return &cp
	case *ir.ListLit:
		cp := *x
		cp.Elems = make([]ir.Expr, len(x.Elems))
		for i, el := range x.Elems {
			cp.Elems[i] = hoistExpr(h, el)
		}
		return &cp
	case *ir.StructLit:
		cp := *x
		cp.Fields = make([]ir.FieldInit, len(x.Fields))
		for i, f := range x.Fields {
			cp.Fields[i] = f
			cp.Fields[i].Value = hoistExpr(h, f.Value)
		}
		return &cp
	case *ir.MapLitIR:
		cp := *x
		cp.Entries = make([]ir.MapEntry, len(x.Entries))
		for i, en := range x.Entries {
			cp.Entries[i] = ir.MapEntry{
				Key:   hoistExpr(h, en.Key),
				Value: hoistExpr(h, en.Value),
			}
		}
		return &cp
	case *ir.Spread:
		cp := *x
		cp.Operand = hoistExpr(h, x.Operand)
		return &cp
	case *ir.Literal, *ir.Ident, *ir.ContextRead, *ir.Lambda, *ir.Closure:
		// Terminal or closure-bodied — hoister never descends into lambda
		// bodies (they execute at invocation, not at view eval time).
		return e
	default:
		panic(fmt.Sprintf("hoistChildren: unhandled %T", x))
	}
}

// exprHasLocals reports whether e references any local-scoped identifier
// (*ir.Param or *ir.LoopVar). If it does, the expression cannot be hoisted
// into a package-level zero-arg computed.
func exprHasLocals(e ir.Expr) bool {
	if e == nil {
		return false
	}
	switch x := e.(type) {
	case *ir.Ident:
		switch x.Sym.(type) {
		case *ir.Param, *ir.LoopVar:
			return true
		}
		return false
	case *ir.Binary:
		return exprHasLocals(x.Left) || exprHasLocals(x.Right)
	case *ir.Unary:
		return exprHasLocals(x.Operand)
	case *ir.Ternary:
		return exprHasLocals(x.Cond) || exprHasLocals(x.Then) || exprHasLocals(x.Else)
	case *ir.Call:
		if x.Receiver != nil && exprHasLocals(x.Receiver) {
			return true
		}
		for _, a := range x.Args {
			if exprHasLocals(a.Value) {
				return true
			}
		}
		return false
	case *ir.Conversion:
		return exprHasLocals(x.Operand)
	case *ir.Select:
		return exprHasLocals(x.Operand)
	case *ir.Index:
		return exprHasLocals(x.Operand) || exprHasLocals(x.Idx)
	case *ir.ListLit:
		return slices.ContainsFunc(x.Elems, exprHasLocals)
	case *ir.StructLit:
		for _, f := range x.Fields {
			if exprHasLocals(f.Value) {
				return true
			}
		}
		return false
	case *ir.Spread:
		return exprHasLocals(x.Operand)
	case *ir.MapLitIR:
		for _, en := range x.Entries {
			if exprHasLocals(en.Key) || exprHasLocals(en.Value) {
				return true
			}
		}
		return false
	case *ir.Literal, *ir.ContextRead:
		return false
	case *ir.Lambda, *ir.Closure:
		// Lambdas/closures evaluated at invocation time — their internal
		// param refs aren't "locals of the surrounding expression."
		return false
	default:
		panic(fmt.Sprintf("exprHasLocals: unhandled %T", x))
	}
}

func lowerNamedAsyncComputed(pkg *ir.Package, fn *ir.Func) error {
	retType := fn.Return
	if retType == nil {
		return fmt.Errorf("NoAsyncReactive: computed %s has no return type", fn.Name)
	}
	zero := ir.ZeroExpr(retType)
	if zero == nil {
		return fmt.Errorf("NoAsyncReactive: cannot lower %s: no zero value for type %s", fn.Name, retType)
	}

	stateVarName := "__async_" + fn.Name
	syntheticVar := &ir.Var{
		Name: stateVarName,
		Type: retType,
		Init: zero,
	}
	pkg.Vars = append(pkg.Vars, syntheticVar)

	// The kicker's body: __async_foo = <orig body expr>
	origRet := fn.Block[0].(*ir.Return)
	kickerAssign := &ir.Assign{
		Target: &ir.Ident{Name: stateVarName, Sym: syntheticVar, Type: retType},
		Op:     ast.AssignSet,
		Value:  origRet.Value,
	}
	kicker := &ir.Func{
		Name:    "$compute_" + fn.Name,
		IsAsync: true,
		Block:   []ir.Stmt{kickerAssign},
		// void return: Return is nil
	}
	pkg.Funcs = append(pkg.Funcs, kicker)

	// Compute reactive deps of the kicker body using the reactivity walker.
	deps := kickerDeps(pkg, origRet.Value)

	// Track the kicker for Tasks 8b/8c.
	pkg.AsyncKickers = append(pkg.AsyncKickers, ir.AsyncKickerEntry{
		Func:         kicker,
		OrigComputed: fn.Name,
		StateVarName: stateVarName,
		Deps:         deps,
	})

	// Rewrite the original computed to a synchronous read.
	fn.Block = []ir.Stmt{
		&ir.Return{
			Value: &ir.Ident{Name: stateVarName, Sym: syntheticVar, Type: retType},
		},
	}
	fn.IsAsync = false

	return nil
}

// kickerDeps returns a sorted slice of reactive var names that expr reads.
// It reuses the reactivityState.exprDeps walker from reactivity.go.
func kickerDeps(pkg *ir.Package, expr ir.Expr) []string {
	st := &reactivityState{
		pkg:          pkg,
		reactiveVars: collectReactiveVars(pkg),
		reverseDeps:  make(map[*ir.Var][]reactiveProp),
	}
	depsMap := st.exprDeps(expr)
	names := make([]string, 0, len(depsMap))
	for v := range depsMap {
		names = append(names, v.Name)
	}
	sort.Strings(names)
	return names
}
