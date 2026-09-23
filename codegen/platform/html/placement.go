package html

import (
	"fmt"

	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/ir"
)

// Placement classifies where a piece of SNGL code runs in an html build:
// client-side (Frontend) or server-side (Backend). See GitLab #27 and
// docs/superpowers/specs/2026-06-04-impure-imports-server-actions-design.md.
//
// This is a pure analysis: it inspects the IR and emits nothing. Phases 3+
// consume the classification to drive RenderModel / server-action emission.
type Placement int

const (
	// Frontend code runs in the browser (client JS, or opt-in WASM).
	Frontend Placement = iota
	// Backend code runs on the server (HTTP route handlers / render).
	Backend
)

// Intrinsic ids for the placement directives, preserved through inlining by
// the checker (Phase 1) so the html platform can recognize them here.
const (
	htmlFrontendIntrinsic = "html.frontend"
	htmlBackendIntrinsic  = "html.backend"
)

// funcImportScheme returns the source scheme ("go", "js", …) of the import a
// native func originates from, or "" if fn is not a native (scheme-imported)
// func. It maps fn.Foreign.Path back to the matching ir.Import via
// Native.ImportPath, then derives the scheme from the import's AST path —
// mirroring isBundledImport in codegen/lang/javascript/jshelpers.go.
func funcImportScheme(pkg *ir.Package, fn *ir.Func) string {
	if pkg == nil || fn == nil || fn.Foreign.Path == "" {
		return ""
	}
	for _, imp := range pkg.Imports {
		if imp == nil || imp.Native == nil || imp.AST == nil {
			continue
		}
		if imp.Native.ImportPath != fn.Foreign.Path {
			continue
		}
		scheme, _ := codegen.SplitScheme(imp.AST.Path)
		return scheme
	}
	return ""
}

// exprPlacement classifies a single expression.
//
//   - A call to the html.frontend intrinsic pins the expression Frontend.
//   - A call to the html.backend intrinsic pins the expression Backend.
//     (A directive pins its whole subtree: the default rule does not look
//     inside it.)
//   - Otherwise the expression is Backend iff any subexpression is a call to
//     a native func whose import scheme is non-empty and not "js" (i.e. a
//     non-js: import, which cannot run in the browser by default); else
//     Frontend.
func exprPlacement(pkg *ir.Package, e ir.Expr) Placement {
	if c, ok := e.(*ir.Call); ok && c.Func != nil {
		switch c.Func.Intrinsic {
		case htmlFrontendIntrinsic:
			return Frontend
		case htmlBackendIntrinsic:
			return Backend
		}
	}
	if exprIsBackendByDefault(pkg, e) {
		return Backend
	}
	return Frontend
}

// exprIsBackendByDefault reports whether e (or any subexpression) calls a
// non-js: native func, ignoring directive wrappers (a directive pins its own
// subtree, so we do not descend into it for the default rule).
func exprIsBackendByDefault(pkg *ir.Package, e ir.Expr) bool {
	found := false
	ir.WalkExprs(e, func(x ir.Expr) error {
		c, ok := x.(*ir.Call)
		if !ok {
			return nil
		}
		if c.Func != nil {
			switch c.Func.Intrinsic {
			case htmlFrontendIntrinsic, htmlBackendIntrinsic:
				// Directive wrapper pins its subtree; skip descent.
				return ir.SkipDir
			}
			if s := funcImportScheme(pkg, c.Func); s != "" && s != "js" {
				found = true
				return ir.SkipDir
			}
		}
		return nil
	})
	return found
}

// handlerPlacement classifies an event handler / func: Backend iff any
// expression in its body is Backend; else Frontend.
func handlerPlacement(pkg *ir.Package, fn *ir.Func) Placement {
	if fn == nil {
		return Frontend
	}
	backend := false
	ir.WalkExprs(fn.Block, func(e ir.Expr) error {
		// exprPlacement already considers e's whole subtree (and honors
		// directive-wrapper pinning), so classify e and prune its children:
		// descending further would re-inspect the inside of a directive
		// wrapper and ignore the pin.
		if exprPlacement(pkg, e) == Backend {
			backend = true
		}
		return ir.SkipDir // prune subtree: e was classified as a whole
	})
	if backend {
		return Backend
	}
	return Frontend
}

// placementScan is what one walk of the package says about its
// html.frontend/html.backend directives. Generate asks both questions of the
// same IR.
type placementScan struct {
	// err is the first misused directive, a build error:
	//
	//   - html.backend(<const expr>): forcing a plain constant expression to
	//     the server means generating a lazy-loaded static file, which is not
	//     implemented. A func value wrapped by html.backend becomes an HTTP
	//     route and is allowed.
	//   - html.frontend(<server-only value>): forcing a value client-side that
	//     is explicitly pinned server-side (it wraps an html.backend(...)
	//     subtree) is a contradiction and cannot be honored.
	//
	// A bare go: call under html.frontend is allowed -- it compiles to WASM.
	// The only "server-only value" recognized is one explicitly pinned with a
	// nested html.backend directive; inferring server-only *state* is left for
	// a later pass.
	err error
	// frontend is the native funcs used inside an html.frontend(...) wrapper,
	// the ones the author forced client-side. They are the only non-js: funcs
	// eligible for WASM; js: funcs run as bundled JS and are excluded.
	frontend map[nativeFuncKey]bool
}

func scanPlacement(pkg *ir.Package) placementScan {
	s := placementScan{frontend: map[nativeFuncKey]bool{}}
	if pkg == nil {
		return s
	}
	_ = ir.WalkExprs(pkg, func(e ir.Expr) error {
		c, ok := e.(*ir.Call)
		if !ok || c.Func == nil {
			return nil
		}
		switch c.Func.Intrinsic {
		case htmlBackendIntrinsic:
			// A func value → HTTP route (supported). Anything else is a
			// constant/data expression: const→file backend, not implemented.
			if s.err == nil && len(c.Args) > 0 && !isFuncValue(c.Args[0].Value) {
				s.err = fmt.Errorf("html: html.backend(...) wrapping a constant expression compiles to a lazy-loaded static file, which is not yet implemented — only html.backend of a func/handler (an HTTP route) is supported")
			}
		case htmlFrontendIntrinsic:
			// A value explicitly pinned server-side (nested html.backend)
			// cannot also be forced client-side.
			if s.err == nil && len(c.Args) > 0 && wrapsBackendDirective(c.Args[0].Value) {
				s.err = fmt.Errorf("html: html.frontend(...) wraps a value pinned to the server with html.backend(...) — a value cannot run both client-side and server-side; remove one of the directives")
			}
			for _, a := range c.Args {
				collectFrontendNatives(pkg, a.Value, s.frontend)
			}
		}
		return nil
	})
	return s
}

// wrapsBackendDirective reports whether e (or any subexpression) is an
// html.backend(...) call — i.e. a value explicitly pinned server-side.
func wrapsBackendDirective(e ir.Expr) bool {
	found := false
	ir.WalkExprs(e, func(x ir.Expr) error {
		if c, ok := x.(*ir.Call); ok && c.Func != nil && c.Func.Intrinsic == htmlBackendIntrinsic {
			found = true
			return ir.SkipDir
		}
		return nil
	})
	return found
}

// isFuncValue reports whether e denotes a function value (a lambda/closure or
// an expression whose type is a func type) — the form html.backend turns into
// an HTTP route.
func isFuncValue(e ir.Expr) bool {
	switch e.(type) {
	case *ir.Lambda, *ir.Closure:
		return true
	}
	if t := e.ExprType(); t != nil && t.Kind == ir.TypeFunc {
		return true
	}
	return false
}

// nativeFuncKey identifies a native (scheme-imported) func by its originating
// import path and name. Used to decide WASM opt-in: a go: func is shipped to
// the browser only if some use of it is forced Frontend via html.frontend.
type nativeFuncKey struct {
	importPath string
	name       string
}

// collectFrontendNatives adds the native funcs sub calls to out, stopping at a
// nested directive: it re-pins its own subtree, and the package scan reaches
// it independently.
func collectFrontendNatives(pkg *ir.Package, sub ir.Expr, out map[nativeFuncKey]bool) {
	ir.WalkExprs(sub, func(x ir.Expr) error {
		c, ok := x.(*ir.Call)
		if !ok || c.Func == nil {
			return nil
		}
		switch c.Func.Intrinsic {
		case htmlFrontendIntrinsic, htmlBackendIntrinsic:
			return ir.SkipDir
		}
		if s := funcImportScheme(pkg, c.Func); s != "" && s != "js" {
			out[nativeFuncKey{importPath: c.Func.Foreign.Path, name: c.Func.Name}] = true
		}
		return nil
	})
}
