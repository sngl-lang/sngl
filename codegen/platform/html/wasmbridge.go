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

// collectWASMPackages scans imports for packages with runtime-used functions
// that need WASM compilation. In v2, native imports are resolved at the
// codegen pipeline level, so this currently returns nil.
// TODO: re-implement when v2 import resolution is wired through.
func collectWASMPackages(_ *ir.Package) []wasmPackage {
	return nil
}
