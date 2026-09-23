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
	u := scanUsage(pkg)
	pkg.UsesI18n = u.i18n
	pkg.UsesAlert = u.alert
	pkg.UsesErrorHandling = pkgUsesErrorHandling(pkg)
	pkg.UsesRemote = u.remote
	return nil
}

// usage is what one expression walk of the package answers.
type usage struct {
	// i18n: an i18n entry-point call (ir.IsI18nCall), or a bare
	// i18n.<pluralKey> Select surviving in a plural-map literal.
	i18n bool
	// alert: a call whose receiver is the Alert stdlib namespace.
	alert bool
	// remote: a query lookup. A program that holds one has boxes that settle
	// after the render that started them, so its entry point has to ask the
	// store to tell it.
	remote bool
}

func scanUsage(pkg *ir.Package) usage {
	var u usage
	_ = ir.WalkExprs(pkg, func(e ir.Expr) error {
		switch n := e.(type) {
		case *ir.Call:
			if ir.IsI18nCall(n) {
				u.i18n = true
			}
			if n.Func != nil {
				if n.Func.Receiver == "Alert" {
					u.alert = true
				}
				if n.Func.Intrinsic == remoteQueryIntrinsic {
					u.remote = true
				}
			}
		case *ir.Select:
			if ir.IsI18nPluralKey(n) {
				u.i18n = true
			}
		}
		if u.i18n && u.alert && u.remote {
			return ir.SkipAll
		}
		return nil
	})
	return u
}

// pkgUsesErrorHandling reports whether the package contains any error-handling
// construct: a window/boundary @error handler, a fallible-call handler, a
// raise, or a func that can error.
func pkgUsesErrorHandling(pkg *ir.Package) bool {
	if stmtsUseErrorHandling(pkg.Body) {
		return true
	}
	for _, w := range pkg.Windows {
		if w.ErrorHandler != nil || stmtsUseErrorHandling(w.Children) {
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
		case *ir.Assign, *ir.LocalVar, *ir.Return, *ir.Emit, *ir.Toggle, *ir.CanvasRedrawStmt,
			*ir.Break, *ir.Continue:
			// No nested error sites.
		default:
			panic(fmt.Sprintf("stmtsUseErrorHandling: unhandled stmt %T", x))
		}
	}
	return false
}
