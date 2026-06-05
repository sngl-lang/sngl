package html

import (
	"io/fs"
	"slices"

	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/ir"
)

// wasmPackage describes an imported package that needs WASM compilation.
type wasmPackage struct {
	namespace  string
	importPath string
	funcs      []codegen.WASMFunc
}

// collectWASMPackages scans imports for scheme-based packages that expose
// impure functions. Such functions cannot be evaluated at compile time.
//
// Per the placement model (GitLab #27), a non-js:// func defaults to BACKEND
// and is NOT shipped to the browser. WASM exposure is opt-in: a func is
// compiled to WASM and exposed via window.__sngl_externs ONLY when the author
// forces it client-side with html.frontend(...). collectWASMPackages therefore
// includes a func only if it appears in frontendNativeFuncs(pkg). A package
// with no frontend-forced funcs contributes no WASM at all.
func collectWASMPackages(pkg *ir.Package, fsys fs.FS, projectDir string) []wasmPackage {
	if pkg == nil {
		return nil
	}
	frontend := frontendNativeFuncs(pkg)
	var out []wasmPackage
	for _, imp := range pkg.Imports {
		if imp == nil || imp.Native == nil || imp.AST == nil {
			continue
		}
		scheme, uri := codegen.SplitScheme(imp.AST.Path)
		if scheme == "" {
			continue
		}
		// js:// modules run as JS in the browser via the esbuild bundle
		// (see jsbundle.go). They never need a WASM extern bridge.
		if scheme == "js" {
			continue
		}
		si := codegen.LookupScheme(scheme)
		if si == nil {
			continue
		}
		var (
			decls *ir.NativeImport
			err   error
		)
		if fsa, ok := si.(codegen.FSAwareScheme); ok && fsys != nil {
			decls, err = fsa.ResolveFS(uri, fsys, projectDir)
		} else {
			decls, err = si.Resolve(uri, projectDir)
		}
		if err != nil || decls == nil {
			continue
		}
		var funcs []codegen.WASMFunc
		for _, f := range decls.Funcs {
			// WASM is opt-in: only funcs forced client-side via html.frontend
			// are compiled to WASM. Bare/backend usage ships nothing.
			if !frontend[nativeFuncKey{importPath: decls.ImportPath, name: f.Name}] {
				continue
			}
			if f.Purity == ir.PurityPure {
				continue
			}
			// Skip funcs the checker already flagged as unusable — variadic
			// params, function-typed params, multi-value returns, etc. The
			// WASM bridge would emit malformed Go for these.
			if f.Unusable != "" {
				continue
			}
			// ref<T> is a SNGL-internal mutable-capture handle; it has no
			// stable JSON representation and the bridge has no way to keep
			// the Go and JS sides aliased to the same backing storage.
			// Refuse to expose any func whose signature touches one.
			if containsRef(f.Return) {
				continue
			}
			skip := false
			for _, p := range f.Params {
				if containsRef(p.Type) {
					skip = true
					break
				}
			}
			if skip {
				continue
			}
			paramTypes := make([]string, len(f.Params))
			for i, p := range f.Params {
				paramTypes[i] = wasmTypeHint(p.Type)
			}
			funcs = append(funcs, codegen.WASMFunc{
				Name:           f.Name,
				ParamTypes:     paramTypes,
				ReturnType:     wasmTypeHint(f.Return),
				HasErrorReturn: f.HasErrorReturn,
			})
		}
		if len(funcs) == 0 {
			continue
		}
		out = append(out, wasmPackage{
			namespace:  imp.Alias,
			importPath: decls.ImportPath,
			funcs:      funcs,
		})
	}
	return out
}

// containsRef reports whether t is, or transitively contains, a ref<T>.
func containsRef(t *ir.Type) bool {
	if t == nil {
		return false
	}
	if t.Kind == ir.TypeRef {
		return true
	}
	return slices.ContainsFunc(t.Elems, containsRef)
}

// wasmTypeHint translates an IR type to the Go type expression the WASM
// bridge should declare for it. Imported-package structs are qualified with
// the bridge's "pkg" alias (the import path is aliased in generated code).
//
// Values returned by this function are interpreted by the bridge emitter:
//   - "string" / "int" / "float" / "bool" use direct js.Value accessors;
//   - any other non-empty value is a typed declaration that the bridge
//     marshals through JSON;
//   - "" means dyn / unrepresentable, and the bridge passes the raw js.Value
//     through unchanged (matching prior behavior for unknown types).
func wasmTypeHint(t *ir.Type) string {
	if t == nil {
		return ""
	}
	switch t.Kind {
	case ir.TypeString:
		return "string"
	case ir.TypeInt:
		return "int"
	case ir.TypeFloat:
		return "float"
	case ir.TypeBool:
		return "bool"
	case ir.TypeList:
		if len(t.Elems) != 1 {
			return ""
		}
		elem := wasmTypeHint(t.Elems[0])
		if elem == "" {
			return ""
		}
		return "[]" + elem
	case ir.TypeStruct:
		if t.Decl == nil {
			return ""
		}
		return "pkg." + t.Decl.SymName()
	}
	return ""
}
