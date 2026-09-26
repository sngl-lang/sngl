package lower

import "git.duckfam.us/jonathan/sngl/ir"

// passUnprovidedContext replaces every read of a context nothing provides with
// its default, when the default is a constant, and drops the context.
//
// Lowered by passContext instead, such a context is a field nothing writes,
// which the optimizer cannot see through -- so a platform override reading
// `markup.palette` handed every token a runtime value where a literal was
// there to be had. On a target that keeps contexts natively the read reached
// the emitter as a lookup for the same reason.
var passUnprovidedContext = pass{
	name:    "UnprovidedContext",
	enabled: func(Features) bool { return true },
	apply:   foldUnprovidedContexts,
}

func foldUnprovidedContexts(pkg *ir.Package, _ Features, _ Options) error {
	if pkg == nil || len(pkg.Contexts) == 0 {
		return nil
	}
	roots := []any{pkg}
	for _, fn := range collectReachableExternalFuncs(pkg) {
		roots = append(roots, fn)
	}
	provided := map[*ir.Context]bool{}
	for _, r := range roots {
		_ = ir.Walk(r, func(n ir.Node) error {
			switch x := n.(type) {
			case *ir.ContextProvider:
				provided[x.Ref] = true
			case *ir.Call:
				// A test's override provides it for the statements after it.
				if _, ctx := setContextArg(x); ctx != nil {
					provided[ctx] = true
				}
			}
			return nil
		})
	}
	fold := map[*ir.Context]bool{}
	kept := pkg.Contexts[:0]
	for _, ctx := range pkg.Contexts {
		if !provided[ctx] && ir.IsConst(ctx.Default) {
			fold[ctx] = true
			continue
		}
		kept = append(kept, ctx)
	}
	pkg.Contexts = kept
	if len(fold) == 0 {
		return nil
	}
	for _, r := range roots {
		err := ir.RewriteExprs(r, func(e ir.Expr) (ir.Expr, error) {
			if rd, ok := e.(*ir.ContextRead); ok && fold[rd.Ref] {
				return ir.CloneExprSharingDecls(rd.Ref.Default), nil
			}
			return e, nil
		})
		if err != nil {
			return err
		}
	}
	return nil
}
