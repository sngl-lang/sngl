package javascript

// jshelpers.go — free helpers that are shared between the legacy translate_ir.go
// path and the new JsIRContext path. Relocated from translate_ir.go so they can
// be used in ircontext.go without cyclic dependency, and so they survive the
// eventual deletion of translate_ir.go.

import (
	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/ir"
)

// factoryName returns the JS factory function name for a component.
// MUST match the convention used by HTML codegen's factory emission.
// If you change this, also update codegen/platform/html/html.go.
func factoryName(comp *ir.Component) string {
	return "__cf_" + sanitizeJSIdent(comp.Name)
}

func sanitizeJSIdent(name string) string {
	out := make([]byte, 0, len(name))
	for i := 0; i < len(name); i++ {
		c := name[i]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_' {
			out = append(out, c)
		} else {
			out = append(out, '_')
		}
	}
	return string(out)
}

// isIntIR reports whether an IR expression is typed as int (for integer
// division lowering).
func isIntIR(e ir.Expr) bool {
	if e == nil {
		return false
	}
	t := e.ExprType()
	if t != nil && t.Kind == ir.TypeInt {
		return true
	}
	return false
}

// isPluralKeyMapType reports whether t is map<i18n.PluralKey, V>.
// The JS translator lowers such maps to plain objects with string keys,
// because the JS i18n runtime uses string plural categories exclusively.
func isPluralKeyMapType(t *ir.Type) bool {
	if t == nil || t.Kind != ir.TypeMap || len(t.Elems) < 1 {
		return false
	}
	k := t.Elems[0]
	if k == nil || k.Kind != ir.TypeStruct || k.Decl == nil {
		return false
	}
	return k.Decl.SymName() == "PluralKey"
}

// isBundledImport reports whether a native import is routed through the JS
// bundler (esbuild) instead of the WASM extern bridge. Decided by the source
// scheme: js:// is bundled, others (go://, etc.) go through WASM extern.
func isBundledImport(imp *ir.Import) bool {
	if imp == nil || imp.AST == nil {
		return false
	}
	scheme, _ := codegen.SplitScheme(imp.AST.Path)
	return scheme == "js"
}

// isBundledNativePkg reports whether the named native package path
// (NativePkg / ImportPath) corresponds to a bundled js:// import in pkg.
func isBundledNativePkg(pkg *ir.Package, nativePkg string) bool {
	if pkg == nil || nativePkg == "" {
		return false
	}
	for _, imp := range pkg.Imports {
		if imp == nil || imp.Native == nil {
			continue
		}
		if imp.Native.ImportPath != nativePkg {
			continue
		}
		return isBundledImport(imp)
	}
	return false
}

// nativeBundledNamespaceAliasCtx resolves a namespace alias name to the
// esbuild-compatible JS identifier when it maps to a bundled js:// native
// import. ExprCtx analogue of nativeBundledNamespaceAlias. Returns ("","")
// when not applicable.
func nativeBundledNamespaceAliasCtx(ctx *codegen.ExprCtx, nsName string) (jsAlias, importPath string) {
	if ctx == nil || ctx.Pkg == nil {
		return "", ""
	}
	for _, imp := range ctx.Pkg.Imports {
		if imp == nil || imp.Alias != nsName || imp.Native == nil {
			continue
		}
		if !isBundledImport(imp) {
			continue
		}
		path := imp.Native.ImportPath
		return codegen.NativeAlias(path), path
	}
	return "", ""
}
