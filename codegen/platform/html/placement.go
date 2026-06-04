package html

import (
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
