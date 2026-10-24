package lib_test

import (
	"testing"

	"duckfam.us/sngl/internal/checker"
	"duckfam.us/sngl/ir"
	"duckfam.us/sngl/lib"
)

// TestLibraryDeclaresNoRetainsParameter pins that no library function keeps
// a pointer to what it is handed. A `#[retains]` argument lives as long as
// the node around the call, and a call no component encloses is refused, so
// a library function with one would be a function a program could only call
// from a component's handler. Nothing in lib/ needs that today: what gtk4
// declares takes scalars, enums, the context GTK lends a draw callback, and
// strings GTK and cairo copy (design/ownership.md). The first that does is a
// decision, and this is where it is noticed.
func TestLibraryDeclaresNoRetainsParameter(t *testing.T) {
	loadEveryPackage(t)
	seen := map[*ir.Package]bool{}
	var visit func(pkg *ir.Package)
	visit = func(pkg *ir.Package) {
		if pkg == nil || seen[pkg] {
			return
		}
		seen[pkg] = true
		funcs := append([]*ir.Func(nil), pkg.Funcs...)
		for _, c := range pkg.Components {
			funcs = append(funcs, c.Funcs...)
		}
		for _, fn := range funcs {
			for _, p := range fn.Params {
				if p.Transfer == ir.TransferRetains {
					t.Errorf("%s: parameter %s of %s is #[retains]; a program can only hand it a value from a component's handler or function", pkg.Path, p.Name, fn.Name)
				}
			}
		}
		for _, imp := range pkg.Imports {
			if imp != nil {
				visit(imp.Pkg)
			}
		}
	}
	for _, name := range lib.Packages() {
		visit(checker.LibPackage(name))
	}
	if len(seen) == 0 {
		t.Fatal("no library package loaded")
	}
}
