package optimize

import "git.duckfam.us/jonathan/sngl/ir"

// hasUnresolvedNativeCall reports whether the package graph still holds a call
// the compile-time evaluator would have to run: an ir.Call whose selector names
// a pure, non-file function of a native import. It answers
// without cloning or folding anything, which is the point — after the first
// Optimize call those calls have folded to literals, so the second call skips
// the round loop entirely instead of cloning an expanded IR to discover
// nothing.
//
// The walk is deliberately approximate: it ignores node kinds it does not know
// and does not enter lambda bodies. A miss costs one extra `go build` and
// nothing else, because a request made when no batch is open is evaluated on
// its own (see requestPureNativeFunc).
func hasUnresolvedNativeCall(pkg *ir.Package, cfg *Config) bool {
	seen := map[*ir.Package]bool{}
	var walk func(*ir.Package) bool
	walk = func(p *ir.Package) bool {
		if p == nil || seen[p] {
			return false
		}
		seen[p] = true
		if pkgHasNativeCall(p, cfg) {
			return true
		}
		for _, imp := range p.Imports {
			if walk(imp.Pkg) {
				return true
			}
		}
		return false
	}
	return walk(pkg)
}

func pkgHasNativeCall(pkg *ir.Package, cfg *Config) bool {
	// The scan needs the package's own native imports, which getNativeImports
	// derives and memoizes on a context; a throwaway one is enough here.
	ctx := &evalCtx{platform: cfg.Platform, language: cfg.Language, pkg: pkg}
	if len(ctx.getNativeImports()) == 0 {
		return false
	}

	found := false
	visit := func(e ir.Expr) {
		if found {
			return
		}
		if call, ok := e.(*ir.Call); ok && isEvaluableNativeCall(call, ctx) {
			found = true
		}
	}
	scanExprs := func(exprs ...ir.Expr) {
		for _, e := range exprs {
			scanExpr(e, visit)
		}
	}
	scanVar := func(v *ir.Var) {
		scanExprs(v.Init)
		for _, h := range v.Handlers {
			if h.Func != nil {
				scanStmts(h.Func.Block, visit)
			}
		}
	}

	for _, c := range pkg.Consts {
		scanVar(c)
	}
	for _, v := range pkg.Vars {
		scanVar(v)
	}
	for _, f := range pkg.Funcs {
		scanStmts(f.Block, visit)
	}
	for _, s := range pkg.Structs {
		for _, f := range s.Fields {
			scanExprs(f.Default)
		}
	}
	scanStmts(pkg.Body, visit)
	for _, comp := range pkg.Components {
		for _, p := range comp.Props {
			scanExprs(p.Default)
		}
		for _, v := range comp.Vars {
			scanVar(v)
		}
		for _, f := range comp.Funcs {
			scanStmts(f.Block, visit)
		}
		for _, t := range comp.Timers {
			scanTimer(t, visit)
		}
		scanStmts(comp.Body, visit)
	}
	for _, w := range pkg.Windows {
		for _, v := range w.Vars {
			scanVar(v)
		}
		for _, f := range w.Funcs {
			scanStmts(f.Block, visit)
		}
		scanStmts(w.Body, visit)
	}
	for _, t := range pkg.Timers {
		scanTimer(t, visit)
	}
	return found
}

// isEvaluableNativeCall reports whether call is a call to a pure native
// function the evaluator could run. It matches what evalNativeCall accepts —
// resolution is by AST selector and native import, not by ir.Func, which the
// checker may or may not have attached — minus the file: scheme, which the
// folder reads directly off disk.
func isEvaluableNativeCall(call *ir.Call, ctx *evalCtx) bool {
	name, ns, ok := nativeCallTarget(call, ctx)
	if !ok {
		return false
	}
	for _, f := range ns.Funcs {
		if f.Name == name && f.Purity == ir.PurityPure && f.Foreign.Path != "file" && f.Foreign.Unusable == "" {
			return true
		}
	}
	return false
}

func scanTimer(t *ir.Timer, visit func(ir.Expr)) {
	if t == nil {
		return
	}
	scanExpr(t.Interval, visit)
	if t.Handler != nil {
		scanStmts(t.Handler.Block, visit)
	}
}

func scanStmts(stmts []ir.Stmt, visit func(ir.Expr)) {
	for _, s := range stmts {
		scanStmt(s, visit)
	}
}

// scanStmt and scanExpr are tolerant siblings of walkStmtExprs/walkAllExprs:
// those panic on a node kind they do not list, which is right for a walker
// whose caller depends on completeness. This one runs on every compile and
// only decides whether to look harder, so an unknown node is skipped.
func scanStmt(s ir.Stmt, visit func(ir.Expr)) {
	switch n := s.(type) {
	case *ir.NodeInst:
		for _, p := range n.Props {
			scanExpr(p.Value, visit)
		}
		for _, h := range n.Handlers {
			if h.Func != nil {
				scanStmts(h.Func.Block, visit)
			}
		}
		scanExpr(n.Key, visit)
		scanExpr(n.Ref, visit)
		scanStmts(n.Children, visit)
	case *ir.If:
		scanExpr(n.Cond, visit)
		scanStmts(n.Body, visit)
		scanStmts(n.Else, visit)
	case *ir.For:
		scanExpr(n.Iter, visit)
		scanStmts(n.Body, visit)
		scanStmts(n.Else, visit)
	case *ir.Assign:
		scanExpr(n.Target, visit)
		scanExpr(n.Value, visit)
	case *ir.Return:
		scanExpr(n.Value, visit)
	case *ir.LocalVar:
		scanExpr(n.Init, visit)
	case *ir.CallStmt:
		scanExpr(n.Call, visit)
	case *ir.Window:
		scanExpr(n.Href, visit)
		scanExpr(n.Title, visit)
		scanExpr(n.Favicon, visit)
		scanStmts(n.Body, visit)
	case *ir.SlotInst:
		scanStmts(n.Children, visit)
	case *ir.ContextProvider:
		scanExpr(n.Value, visit)
		scanStmts(n.Children, visit)
	case *ir.ErrorBoundary:
		scanStmts(n.Children, visit)
	case *ir.Emit:
		for _, a := range n.Args {
			scanExpr(a.Value, visit)
		}
	case *ir.Toggle:
		scanExpr(n.Target, visit)
	}
}

func scanExpr(e ir.Expr, visit func(ir.Expr)) {
	if e == nil {
		return
	}
	visit(e)
	switch x := e.(type) {
	case *ir.Binary:
		scanExpr(x.Left, visit)
		scanExpr(x.Right, visit)
	case *ir.Unary:
		scanExpr(x.Operand, visit)
	case *ir.Ternary:
		scanExpr(x.Cond, visit)
		scanExpr(x.Then, visit)
		scanExpr(x.Else, visit)
	case *ir.Call:
		scanExpr(x.Receiver, visit)
		for _, a := range x.Args {
			scanExpr(a.Value, visit)
		}
	case *ir.Conversion:
		scanExpr(x.Operand, visit)
	case *ir.Select:
		scanExpr(x.Operand, visit)
	case *ir.Index:
		scanExpr(x.Operand, visit)
		scanExpr(x.Idx, visit)
	case *ir.ListLit:
		for _, el := range x.Elems {
			scanExpr(el, visit)
		}
	case *ir.StructLit:
		for _, f := range x.Fields {
			scanExpr(f.Value, visit)
		}
	case *ir.MapLitIR:
		for _, kv := range x.Entries {
			scanExpr(kv.Key, visit)
			scanExpr(kv.Value, visit)
		}
	case *ir.Spread:
		scanExpr(x.Operand, visit)
	case *ir.Lambda:
		if x.Func != nil {
			scanStmts(x.Func.Block, visit)
		}
	}
}
