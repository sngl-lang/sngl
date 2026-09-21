package codegen

import "git.duckfam.us/jonathan/sngl/ir"

// OwnerMethod resolves a receiver-qualified method call to the func it names,
// searching every state owner rather than package scope alone.
//
// The inliner hoists a component's method onto whatever it inlined that
// component into and then drops the component, so an instance's clone is a
// func of main or of the package while its receiver still names a declaration
// pkg.Components no longer lists. Searching pkg.Funcs alone misses it, and a
// backend that falls through then names something nothing declares -- a free
// function in Go, a method on `state` in JS. Both language translators ask this
// one question; the spelling of the call is theirs to decide.
//
// It used to report the owner too, for a host that emits an owner's funcs as
// methods to decide the spelling from. That answer does not survive a window
// owning nothing: a clone hoisted into a window body and a genuine top-level
// method on a user type are both the package's now, and Go told them apart by
// asking which owner held them. The receiver is what separates the two --
// LiftsToFreeFunc -- so the owner is no longer anyone's question.
func OwnerMethod(pkg *ir.Package, receiver, method string) (*ir.Func, bool) {
	if pkg == nil || receiver == "" {
		return nil, false
	}
	for _, o := range ir.Owners(pkg) {
		for _, f := range o.Funcs {
			if f.Receiver == receiver && f.Name == method {
				return f, true
			}
		}
	}
	return nil, false
}
