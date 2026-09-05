package codegen

import "git.duckfam.us/jonathan/sngl/ir"

// OwnerMethod resolves a receiver-qualified method call to the func it names
// and the state owner that holds it, searching every owner rather than package
// scope alone.
//
// The inliner hoists a component's method onto whatever it inlined that
// component into and then drops the component, so an instance's clone is a
// func of main or of a window while its receiver still names a declaration
// pkg.Components no longer lists. Searching pkg.Funcs alone misses it, and a
// backend that falls through then names something nothing declares -- a free
// function in Go, a method on `state` in JS. Both language translators ask this
// one question; the spelling of the call is theirs to decide, and the owner is
// what a host that emits an owner's funcs as methods needs to decide it.
//
// A nested method is registered in both pkg.Funcs and its component's Funcs
// (the checker's registerNestedMethods appends the same *ir.Func to each), so
// such a method resolves to the package owner -- the first one Owners lists --
// which is also the scope a host emits it into.
func OwnerMethod(pkg *ir.Package, receiver, method string) (*ir.Func, ir.Owner, bool) {
	if pkg == nil || receiver == "" {
		return nil, ir.Owner{}, false
	}
	for _, o := range ir.Owners(pkg) {
		for _, f := range o.Funcs {
			if f.Receiver == receiver && f.Name == method {
				return f, o, true
			}
		}
	}
	return nil, ir.Owner{}, false
}
