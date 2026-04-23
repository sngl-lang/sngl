package html

import (
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
// impure functions. Such functions cannot be evaluated at compile time, so
// they are compiled to WASM and exposed to the page via window.__sngl_externs
// for JS to call at runtime.
func collectWASMPackages(pkg *ir.Package, projectDir string) []wasmPackage {
	if pkg == nil {
		return nil
	}
	var out []wasmPackage
	for _, imp := range pkg.Imports {
		if imp == nil || imp.Native == nil || imp.AST == nil {
			continue
		}
		scheme, uri := codegen.SplitScheme(imp.AST.Path)
		if scheme == "" {
			continue
		}
		si := codegen.LookupScheme(scheme)
		if si == nil {
			continue
		}
		decls, err := si.Resolve(uri, projectDir)
		if err != nil || decls == nil {
			continue
		}
		var funcs []codegen.WASMFunc
		for _, f := range decls.Funcs {
			if f.Purity == ir.PurityPure {
				continue
			}
			paramTypes := make([]string, len(f.Params))
			for i, p := range f.Params {
				paramTypes[i] = wasmTypeHint(p.Type)
			}
			funcs = append(funcs, codegen.WASMFunc{
				Name:       f.Name,
				ParamTypes: paramTypes,
				ReturnType: wasmTypeHint(f.Return),
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

// wasmTypeHint reduces an IR type to the small set of primitive names the
// WASM bridge knows how to marshal; anything else becomes empty, which the
// emitter treats as a raw js.Value.
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
	}
	return ""
}
