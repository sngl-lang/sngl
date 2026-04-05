package html

import (
	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/codegen"
)

// wasmPackage describes an imported package that needs WASM compilation.
type wasmPackage struct {
	namespace  string
	importPath string
	funcs      []codegen.WASMFunc
}

// collectWASMPackages scans NativeImports for go:// (or other scheme) packages
// with runtime-used functions. Pure functions that were folded at compile time
// are excluded — only functions that survive optimization need WASM bindings.
func collectWASMPackages(doc *ast.Document) []wasmPackage {
	if doc.NativeImports == nil {
		return nil
	}

	var pkgs []wasmPackage
	for ns, ni := range doc.NativeImports {
		var funcs []codegen.WASMFunc
		for _, d := range ni.Data {
			if !d.IsFunc || !d.Extern {
				continue
			}
			if d.Resolved != nil && d.Resolved.NativePkg == "file" {
				continue
			}
			// Pure functions are evaluated at compile time — no WASM needed.
			if d.Purity == ast.PurityPure {
				continue
			}
			funcs = append(funcs, codegen.WASMFunc{
				Name:       d.Name,
				ParamTypes: d.ParamTypes,
				ReturnType: d.ReturnType,
			})
		}
		if len(funcs) > 0 {
			pkgs = append(pkgs, wasmPackage{
				namespace:  ns,
				importPath: ni.ImportPath,
				funcs:      funcs,
			})
		}
	}
	return pkgs
}
