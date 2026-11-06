package ir

import "duckfam.us/sngl/ast"

// TestMountID marks the call a test body mounts its component with. No
// declaration carries it: passTestMount writes the call where a compiled test
// first needs the component, and each test emitter spells it.
const TestMountID = "test.mount"

// A TestPreset is what a statement of a test body sets up before the
// component the test takes mounts: the context a `t.setContext` names, or the
// prop or var of the component a write gives its starting value.
type TestPreset struct {
	Context string
	Field   Symbol
}

// TestSetup reports whether s only sets up the component comp names, which the
// test has not yet needed mounted, and what it sets.
//
// A test mounts its component at the first statement that needs it. Until
// then a `t.setContext` sets the value the mount reads, and a plain write of one of the component's own props or vars,
// `c.name = "b"`, the value it starts from, as a call site would. A write
// whose value reads the component or the package's state reads what the
// mount makes, and needs it; so does every other statement.
func TestSetup(s Stmt, comp *Param, pkg *Package) (TestPreset, bool) {
	if comp == nil {
		return TestPreset{}, false
	}
	switch x := s.(type) {
	case *CallStmt:
		c := x.Call
		if !IsTestCall(c, TestSetContextID) || readsSym(c, comp) || readsPackageVar(c, pkg) {
			return TestPreset{}, false
		}
		name := ""
		for _, a := range c.Args {
			switch v := a.Value.(type) {
			case *ContextRead:
				name = v.Ref.Name
			case *Literal:
				// passContext leaves the context argument as its name.
				if v.Type == TypString {
					name = v.Value
				}
			}
			if name != "" {
				break
			}
		}
		if name == "" {
			return TestPreset{}, false
		}
		return TestPreset{Context: name}, true
	case *Assign:
		if x.Op != ast.AssignSet {
			return TestPreset{}, false
		}
		sel, ok := x.Target.(*Select)
		if !ok {
			return TestPreset{}, false
		}
		id, ok := sel.Operand.(*Ident)
		if !ok || id.Sym != Symbol(comp) || readsSym(x.Value, comp) || readsPackageVar(x.Value, pkg) {
			return TestPreset{}, false
		}
		if f := componentField(comp, sel.Field); f != nil {
			return TestPreset{Field: f}, true
		}
	}
	return TestPreset{}, false
}

// componentField is the prop or var of the component comp is a parameter of
// named name, or nil.
func componentField(comp *Param, name string) Symbol {
	if comp.Type == nil {
		return nil
	}
	c, ok := comp.Type.Decl.(*Component)
	if !ok {
		return nil
	}
	for _, p := range c.Props {
		if p.Name == name && p.Sym != nil {
			return p.Sym
		}
	}
	for _, v := range c.Vars {
		if v.Name == name {
			return v
		}
	}
	return nil
}

// readsSym reports whether anything under root reads sym.
func readsSym(root any, sym Symbol) bool {
	found := false
	_ = Walk(root, func(n Node) error {
		if id, ok := n.(*Ident); ok && id.Sym == sym {
			found = true
			return SkipAll
		}
		return nil
	})
	return found
}

// readsPackageVar reports whether anything under root reads a var of pkg.
func readsPackageVar(root any, pkg *Package) bool {
	if pkg == nil || len(pkg.Vars) == 0 {
		return false
	}
	vars := make(map[Symbol]bool, len(pkg.Vars))
	for _, v := range pkg.Vars {
		vars[v] = true
	}
	found := false
	_ = Walk(root, func(n Node) error {
		if id, ok := n.(*Ident); ok && vars[id.Sym] {
			found = true
			return SkipAll
		}
		return nil
	})
	return found
}
