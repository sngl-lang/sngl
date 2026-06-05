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
	htmlFrontendIntrinsic = "HtmlFrontend"
	htmlBackendIntrinsic  = "HtmlBackend"
)

// funcImportScheme returns the source scheme ("go", "js", …) of the import a
// native func originates from, or "" if fn is not a native (scheme-imported)
// func. It maps fn.NativePkg back to the matching ir.Import via
// Native.ImportPath, then derives the scheme from the import's AST path —
// mirroring isBundledImport in codegen/lang/javascript/jshelpers.go.
func funcImportScheme(pkg *ir.Package, fn *ir.Func) string {
	if pkg == nil || fn == nil || fn.NativePkg == "" {
		return ""
	}
	for _, imp := range pkg.Imports {
		if imp == nil || imp.Native == nil || imp.AST == nil {
			continue
		}
		if imp.Native.ImportPath != fn.NativePkg {
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
//     non-js:// import, which cannot run in the browser by default); else
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
// non-js:// native func, ignoring directive wrappers (a directive pins its own
// subtree, so we do not descend into it for the default rule).
func exprIsBackendByDefault(pkg *ir.Package, e ir.Expr) bool {
	found := false
	walkExpr(e, func(x ir.Expr) bool {
		c, ok := x.(*ir.Call)
		if !ok {
			return false
		}
		if c.Func != nil {
			switch c.Func.Intrinsic {
			case htmlFrontendIntrinsic, htmlBackendIntrinsic:
				// Directive wrapper pins its subtree; skip descent.
				return true
			}
			if s := funcImportScheme(pkg, c.Func); s != "" && s != "js" {
				found = true
				return true // short-circuit
			}
		}
		return false
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
	walkStmts(fn.Block, func(e ir.Expr) bool {
		// exprPlacement already considers e's whole subtree (and honors
		// directive-wrapper pinning), so classify e and prune its children:
		// descending further would re-inspect the inside of a directive
		// wrapper and ignore the pin.
		if exprPlacement(pkg, e) == Backend {
			backend = true
		}
		return true // prune subtree: e was classified as a whole
	})
	if backend {
		return Backend
	}
	return Frontend
}

// checkPlacementDirectives validates the html.frontend/html.backend directives
// across the whole package, returning the first build error found:
//
//   - html.backend(<const expr>): forcing a plain constant expression to the
//     server means generating a lazy-loaded static file, which is not yet
//     implemented (Task 6.2). A func value wrapped by html.backend becomes an
//     HTTP route and is allowed.
//   - html.frontend(<server-only value>): forcing a value client-side that is
//     explicitly pinned server-side (it wraps an html.backend(...) subtree) is
//     a contradiction and cannot be honored (Task 6.3).
//
// Note (6.3 simplification): a bare go:// call under html.frontend is allowed —
// it compiles to WASM (Phase 5). The only "server-only value" this v1 rule
// recognizes is one explicitly pinned with a nested html.backend directive.
// Inferring server-only *state* (vars mutated solely by backend routes) is left
// for a later pass.
func checkPlacementDirectives(pkg *ir.Package) error {
	if pkg == nil {
		return nil
	}
	var err error
	ir.WalkExprs(pkg, func(e ir.Expr) bool {
		c, ok := e.(*ir.Call)
		if !ok || c.Func == nil || len(c.Args) == 0 {
			return false
		}
		arg := c.Args[0].Value
		switch c.Func.Intrinsic {
		case htmlBackendIntrinsic:
			// A func value → HTTP route (supported). Anything else is a
			// constant/data expression: const→file backend, not implemented.
			if !isFuncValue(arg) {
				err = fmt.Errorf("html: html.backend(...) wrapping a constant expression compiles to a lazy-loaded static file, which is not yet implemented — only html.backend of a func/handler (an HTTP route) is supported")
				return true
			}
		case htmlFrontendIntrinsic:
			// A value explicitly pinned server-side (nested html.backend)
			// cannot also be forced client-side.
			if wrapsBackendDirective(arg) {
				err = fmt.Errorf("html: html.frontend(...) wraps a value pinned to the server with html.backend(...) — a value cannot run both client-side and server-side; remove one of the directives")
				return true
			}
		}
		return false
	})
	return err
}

// wrapsBackendDirective reports whether e (or any subexpression) is an
// html.backend(...) call — i.e. a value explicitly pinned server-side.
func wrapsBackendDirective(e ir.Expr) bool {
	found := false
	walkExpr(e, func(x ir.Expr) bool {
		if c, ok := x.(*ir.Call); ok && c.Func != nil && c.Func.Intrinsic == htmlBackendIntrinsic {
			found = true
			return true
		}
		return false
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
// import path and name. Used to decide WASM opt-in: a go:// func is shipped to
// the browser only if some use of it is forced Frontend via html.frontend.
type nativeFuncKey struct {
	importPath string
	name       string
}

// frontendNativeFuncs collects, across the whole package, the set of native
// funcs that are used inside an html.frontend(...) wrapper — i.e. funcs the
// author explicitly forced to run client-side. These are the ONLY non-js://
// funcs eligible for WASM compilation; bare (default-backend) usage ships no
// WASM. js:// funcs are excluded (they run as bundled JS, never WASM).
func frontendNativeFuncs(pkg *ir.Package) map[nativeFuncKey]bool {
	out := map[nativeFuncKey]bool{}
	if pkg == nil {
		return out
	}
	collect := func(sub ir.Expr) {
		walkExpr(sub, func(x ir.Expr) bool {
			c, ok := x.(*ir.Call)
			if !ok || c.Func == nil {
				return false
			}
			// A nested directive re-pins its own subtree; stop here and let
			// the top-level scan reach it independently.
			switch c.Func.Intrinsic {
			case htmlFrontendIntrinsic, htmlBackendIntrinsic:
				return true
			}
			if s := funcImportScheme(pkg, c.Func); s != "" && s != "js" {
				out[nativeFuncKey{importPath: c.Func.NativePkg, name: c.Func.Name}] = true
			}
			return false
		})
	}
	ir.WalkExprs(pkg, func(e ir.Expr) bool {
		if c, ok := e.(*ir.Call); ok && c.Func != nil && c.Func.Intrinsic == htmlFrontendIntrinsic {
			for _, a := range c.Args {
				collect(a.Value)
			}
		}
		return false // never short-circuit: scan the whole package
	})
	return out
}

// walkExpr visits e and each of its sub-expressions, calling fn on each.
// fn returns true to prune the subtree at that node (stop descending into its
// children) — used to honor directive-wrapper pinning and to short-circuit.
func walkExpr(e ir.Expr, fn func(ir.Expr) bool) {
	if e == nil {
		return
	}
	if fn(e) {
		return
	}
	switch x := e.(type) {
	case *ir.Binary:
		walkExpr(x.Left, fn)
		walkExpr(x.Right, fn)
	case *ir.Unary:
		walkExpr(x.Operand, fn)
	case *ir.Ternary:
		walkExpr(x.Cond, fn)
		walkExpr(x.Then, fn)
		walkExpr(x.Else, fn)
	case *ir.Call:
		walkExpr(x.Receiver, fn)
		walkExpr(x.Callee, fn)
		for _, a := range x.Args {
			walkExpr(a.Value, fn)
		}
	case *ir.Conversion:
		walkExpr(x.Operand, fn)
	case *ir.Select:
		walkExpr(x.Operand, fn)
	case *ir.Index:
		walkExpr(x.Operand, fn)
		walkExpr(x.Idx, fn)
	case *ir.ListLit:
		for _, el := range x.Elems {
			walkExpr(el, fn)
		}
	case *ir.MapLitIR:
		for _, kv := range x.Entries {
			walkExpr(kv.Key, fn)
			walkExpr(kv.Value, fn)
		}
	case *ir.StructLit:
		for _, f := range x.Fields {
			walkExpr(f.Value, fn)
		}
	case *ir.Lambda:
		if x.Func != nil {
			walkStmts(x.Func.Block, fn)
		}
	case *ir.Closure:
		if x.Func != nil {
			walkStmts(x.Func.Block, fn)
		}
	case *ir.Spread:
		walkExpr(x.Operand, fn)
	case *ir.Literal, *ir.Ident, *ir.ContextRead:
		// Leaf — no sub-expressions.
	}
}

// walkStmts visits every expression reachable from stmts via walkExpr.
func walkStmts(stmts []ir.Stmt, fn func(ir.Expr) bool) {
	for _, s := range stmts {
		walkStmt(s, fn)
	}
}

func walkStmt(s ir.Stmt, fn func(ir.Expr) bool) {
	switch n := s.(type) {
	case *ir.NodeInst:
		for _, p := range n.Props {
			walkExpr(p.Value, fn)
		}
		for _, h := range n.Handlers {
			if h.Func != nil {
				walkStmts(h.Func.Block, fn)
			}
		}
		walkStmts(n.Children, fn)
	case *ir.CallStmt:
		if n.Call != nil {
			walkExpr(n.Call, fn)
		}
	case *ir.Assign:
		walkExpr(n.Target, fn)
		walkExpr(n.Value, fn)
	case *ir.Toggle:
		walkExpr(n.Target, fn)
	case *ir.Emit:
		for _, a := range n.Args {
			walkExpr(a.Value, fn)
		}
	case *ir.LocalVar:
		walkExpr(n.Init, fn)
	case *ir.Return:
		walkExpr(n.Value, fn)
	case *ir.If:
		walkExpr(n.Cond, fn)
		walkStmts(n.Body, fn)
		walkStmts(n.Else, fn)
	case *ir.For:
		walkExpr(n.Iter, fn)
		walkStmts(n.Body, fn)
		walkStmts(n.Else, fn)
	case *ir.SlotInst:
		walkStmts(n.Children, fn)
	case *ir.PlatformFilter:
		walkStmts(n.Body, fn)
	case *ir.ErrorBoundary:
		walkStmts(n.Children, fn)
	case *ir.ContextProvider:
		walkStmts(n.Children, fn)
	}
}
