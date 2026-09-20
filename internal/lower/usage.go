package lower

import (
	"fmt"

	"git.duckfam.us/jonathan/sngl/ir"
)

// passStampUsage records package-wide feature usage onto the IR so codegen
// reads a flag instead of re-walking the whole package (once per platform, and
// in several near-identical copies before this pass existed). It is always on
// and runs last, so it observes the fully-lowered IR for the active target —
// the same shape codegen sees. In particular i18n detection must run after
// InlinePure (which can collapse the i18n wrapper into a direct intrinsic);
// final-pass placement guarantees that.
var passStampUsage = pass{
	name:    "StampUsage",
	enabled: func(Caps) bool { return true },
	apply:   stampUsage,
}

func stampUsage(pkg *ir.Package, _ Caps, _ Options) error {
	if pkg == nil {
		return nil
	}
	pkg.UsesI18n = pkgUsesI18n(pkg)
	pkg.UsesAlert = pkgUsesAlert(pkg)
	pkg.UsesErrorHandling = pkgUsesErrorHandling(pkg)
	pkg.UsesRemote = pkgUsesRemote(pkg)
	return nil
}

// pkgUsesRemote matches any query lookup. A program that holds one has boxes
// that settle after the render that started them, so its entry point has to ask
// the store to tell it.
func pkgUsesRemote(pkg *ir.Package) bool {
	found := false
	ir.WalkExprs(pkg, func(e ir.Expr) error {
		if c, isCall := e.(*ir.Call); isCall && c.Func != nil && c.Func.Intrinsic == remoteQueryIntrinsic {
			found = true
			return ir.SkipAll
		}
		return nil
	})
	return found
}

// pkgUsesI18n matches any i18n entry-point call (ir.IsI18nCall) plus bare
// i18n.<pluralKey> Selects that survive in plural-map literals — the superset
// the Go backend already used, now shared by every backend.
func pkgUsesI18n(pkg *ir.Package) bool {
	found := false
	ir.WalkExprs(pkg, func(e ir.Expr) error {
		switch n := e.(type) {
		case *ir.Call:
			if ir.IsI18nCall(n) {
				found = true
				return ir.SkipAll
			}
		case *ir.Select:
			if ir.IsI18nPluralKey(n) {
				found = true
				return ir.SkipAll
			}
		}
		return nil
	})
	return found
}

// pkgUsesAlert matches any call whose receiver is the Alert stdlib namespace
// (Alert.toast/info/warn/error).
func pkgUsesAlert(pkg *ir.Package) bool {
	found := false
	ir.WalkExprs(pkg, func(e ir.Expr) error {
		if c, ok := e.(*ir.Call); ok && c.Func != nil && c.Func.Receiver == "Alert" {
			found = true
			return ir.SkipAll
		}
		return nil
	})
	return found
}

// pkgUsesErrorHandling reports whether the package contains any error-handling
// construct: a window/boundary @error handler, a fallible-call handler, a
// raise, or a func that can error.
func pkgUsesErrorHandling(pkg *ir.Package) bool {
	for _, w := range pkg.Windows {
		if w.ErrorHandler != nil || stmtsUseErrorHandling(w.Body) {
			return true
		}
		// A window owns funcs the way a component does, and the ones this
		// lowering synthesized are where the raises are: an effect group's
		// settle raises when it does not converge. Reading the body alone left
		// the error payload undeclared in a program whose only raise was there.
	}
	for _, comp := range pkg.Components {
		if stmtsUseErrorHandling(comp.Body) {
			return true
		}
		for _, f := range comp.Funcs {
			if f.CanError || stmtsUseErrorHandling(f.Block) {
				return true
			}
		}
	}
	for _, f := range pkg.Funcs {
		if f.CanError || stmtsUseErrorHandling(f.Block) {
			return true
		}
	}
	return false
}

func stmtsUseErrorHandling(stmts []ir.Stmt) bool {
	for _, s := range stmts {
		switch x := s.(type) {
		case *ir.ErrorBoundary:
			return true
		case *ir.NodeInst:
			for i := range x.Handlers {
				if x.Handlers[i].CanError {
					return true
				}
				if x.Handlers[i].Func != nil && stmtsUseErrorHandling(x.Handlers[i].Func.Block) {
					return true
				}
			}
			if stmtsUseErrorHandling(x.Children) {
				return true
			}
		case *ir.CallStmt:
			if x.Call != nil && x.Call.ErrorMode != ir.ErrorNone {
				return true
			}
		case *ir.If:
			if stmtsUseErrorHandling(x.Body) || stmtsUseErrorHandling(x.Else) {
				return true
			}
		case *ir.For:
			if stmtsUseErrorHandling(x.Body) || stmtsUseErrorHandling(x.Else) {
				return true
			}
		case *ir.SlotInst:
			if stmtsUseErrorHandling(x.Children) {
				return true
			}
		case *ir.ContextProvider:
			if stmtsUseErrorHandling(x.Children) {
				return true
			}
		case *ir.Window:
			if stmtsUseErrorHandling(x.Body) {
				return true
			}
		case *ir.Assign, *ir.LocalVar, *ir.Return, *ir.Emit, *ir.Toggle, *ir.CanvasRedrawStmt,
			*ir.Break, *ir.Continue:
			// No nested error sites.
		default:
			panic(fmt.Sprintf("stmtsUseErrorHandling: unhandled stmt %T", x))
		}
	}
	return false
}
