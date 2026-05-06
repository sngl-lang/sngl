package checker

import (
	"strings"

	"git.duckfam.us/jonathan/sngl/ast"
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
//  2. Parameterized async: any non-zero-param func that is async (i.e.
//     IsAsync=true after the fixed-point propagation) is rejected.  The
//     settled-state lowering (NoAsyncReactive) emits a single state var per
//     computed func; it cannot key per-call-argument.
//
//     Note: the spec calls for a tighter definition ("async called from a
//     reactive context"), but detecting reactive context requires a full
//     visual-tree walk that is out of scope here.  We conservatively reject
//     all parameterized async funcs, which is safe: the lowering would fail
//     on them anyway, and the user can always restructure by separating the
//     async call from the parameterization.
//
//  3. Async function into sync slot: deferred.  The ir.FuncSig / ir.Type for
//     TypeFunc carries no IsAsync/color field, so there is no way to inspect
//     whether the declared parameter/field type expects a sync vs async func.
//     This rule will be implementable once FuncSig gains an IsAsync flag.
//     See spec §5 rule 3 and Task 47 (closure points-to).
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

	// Rule 2: parameterized async functions.
	for _, fn := range allFuncs(pkg) {
		if fn.IsAsync && len(fn.Params) > 0 && fn.AST != nil {
			c.error(fn.AST.Pos, "async expression not allowed in parameterized reactive context")
		}
	}
}

// checkReservedPrefixAt emits an error if name starts with a reserved prefix.
func checkReservedPrefixAt(c *checker, name string, pos ast.Pos) {
	if strings.HasPrefix(name, "__async_") || strings.HasPrefix(name, "__hoist_") {
		c.error(pos, "name %q uses reserved prefix", name)
	}
}

// varDeclPos extracts the position from an ir.Var's AST node.
func varDeclPos(stmt ast.Stmt) ast.Pos {
	if stmt == nil {
		return ast.Pos{}
	}
	if p := stmt.StmtPos(); p != nil {
		return *p
	}
	return ast.Pos{}
}

