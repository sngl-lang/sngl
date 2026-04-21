package html

import (
	"strings"

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
// impure functions (or impure func-typed vars). Such functions cannot be
// evaluated at compile time, so they are compiled to WASM and exposed to
// the page via window.__sngl_externs for JS to call at runtime.
func collectWASMPackages(pkg *ir.Package, projectDir string) []wasmPackage {
	if pkg == nil {
		return nil
	}
	var out []wasmPackage
	for _, imp := range pkg.Imports {
		if imp == nil || imp.Native == nil || imp.AST == nil {
			continue
		}
		scheme, uri := splitScheme(imp.AST.Path)
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
			if f.Pure {
				continue
			}
			funcs = append(funcs, codegen.WASMFunc{
				Name:       f.Name,
				ParamTypes: f.ParamTypes,
				ReturnType: f.ReturnType,
			})
		}
		for _, v := range decls.Vars {
			if !v.IsFunc || v.Pure {
				continue
			}
			funcs = append(funcs, codegen.WASMFunc{Name: v.Name})
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

func splitScheme(path string) (scheme, uri string) {
	if before, after, ok := strings.Cut(path, "://"); ok {
		return before, after
	}
	return "", path
}
