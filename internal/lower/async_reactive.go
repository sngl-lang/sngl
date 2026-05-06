package lower

import (
	"fmt"
	"sort"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/ir"
)

var passAsyncReactive = pass{
	name:    "NoAsyncReactive",
	enabled: func(c Caps) bool { return c.NoAsyncReactive },
	apply:   lowerAsyncReactive,
}

// lowerAsyncReactive rewrites every named zero-arg computed whose body
// transitively calls an async function. For each such computed "foo":
//
//  1. A synthetic state var __async_foo of the same return type is appended to
//     pkg.Vars (with zero initializer).
//  2. A synthetic async kicker func $compute_foo is appended to pkg.Funcs.
//     Its body is a single assignment: __async_foo = <original body expr>.
//  3. The original computed is rewritten to be a synchronous read of
//     __async_foo.
//  4. An AsyncKicker record is appended to pkg.AsyncKickers so Tasks 8b/8c
//     can find and wire the kickers.
func lowerAsyncReactive(pkg *ir.Package, _ Caps) error {
	if pkg == nil {
		return nil
	}
	return lowerAsyncReactiveInFuncs(pkg, pkg.Funcs)
}

func lowerAsyncReactiveInFuncs(pkg *ir.Package, funcs []*ir.Func) error {
	for _, fn := range funcs {
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
func isReactiveAsyncComputed(fn *ir.Func) bool {
	if fn == nil || fn.IsTest || len(fn.Params) != 0 || fn.Name == "" {
		return false
	}
	// Must be an expression-body computed: AST body non-nil.
	if fn.AST == nil || fn.AST.Body == nil {
		return false
	}
	// Checker stores expression bodies as a single Return statement.
	if len(fn.Block) != 1 {
		return false
	}
	ret, ok := fn.Block[0].(*ir.Return)
	if !ok || ret.Value == nil {
		return false
	}
	return ir.ExprHasAsyncCall(ret.Value)
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
