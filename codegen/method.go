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

// PackageStateFuncs names the top-level funcs whose body reads or writes a
// package-level var, and everything that reaches one through a call.
//
// A target whose package state lives inside one scope -- a Model's fields on
// bubbletea, fyne and gtk4, a composable's `remember`ed locals on android --
// has to emit these inside that scope. Emitted beside it, the body names
// something the file does not declare.
//
// The transitive half is load-bearing: a caller left outside spells a call to
// something declared inside, which is the same undefined name one function
// further out.
//
// Asked here rather than once per language because the question is about the
// IR and not about a host: what differs is the consequence -- a Go method on
// Model, a Kotlin local `fun` -- and each target still decides that for itself.
func PackageStateFuncs(pkg *ir.Package) map[*ir.Func]bool {
	if pkg == nil {
		return nil
	}
	// Consts are excluded because a target spells one the same inside the
	// scope and outside it; a state var is reached through the scope.
	state := map[ir.Symbol]bool{}
	for _, v := range pkg.Vars {
		state[v] = true
	}

	touches := map[*ir.Func]bool{}
	calls := map[*ir.Func][]*ir.Func{}
	for _, fn := range pkg.Funcs {
		var visit func(ir.Node) error
		visit = func(n ir.Node) error {
			switch e := n.(type) {
			case *ir.Ident:
				if state[e.Sym] {
					touches[fn] = true
				}
				if callee, ok := e.Sym.(*ir.Func); ok {
					calls[fn] = append(calls[fn], callee)
				}
			case *ir.Call:
				if e.Func != nil {
					calls[fn] = append(calls[fn], e.Func)
				}
				// A boundary's or window's handler is inlined at the call it
				// catches, and ir.Walk does not follow ResolvedHandler there.
				if h := ir.CatchingHandler(e); h != nil && h != e.ErrorHandler && h.Func != nil {
					_ = ir.Walk(h.Func.Block, visit)
				}
			case *ir.If:
				if e.Catch != nil && e.Catch.Func != nil {
					_ = ir.Walk(e.Catch.Func.Block, visit)
				}
			}
			return nil
		}
		_ = ir.Walk(fn.Block, visit)
	}

	for changed := true; changed; {
		changed = false
		for fn, callees := range calls {
			if touches[fn] {
				continue
			}
			for _, callee := range callees {
				if touches[callee] {
					touches[fn] = true
					changed = true
					break
				}
			}
		}
	}
	return touches
}
