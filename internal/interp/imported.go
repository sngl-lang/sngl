package interp

import "duckfam.us/sngl/ir"

// importedVarOwner is the program's root scope where sym is a var another
// package declares, bound there from its initializer the first time it is
// reached, or nil. Such a var is in no list this program walks, and read
// afresh from its initializer each time, `memory[k] = v` in sngl:settings
// wrote nowhere a later read looked. A compiled target holds it as the
// program's own state (lower.promoteVars), so a run, and each test, starts
// from its initializer.
func (env *Env) importedVarOwner(sym ir.Symbol) *Env {
	v, ok := sym.(*ir.Var)
	if !ok || v.IsConst || v.Init == nil {
		return nil
	}
	root := env.programRoot()
	if _, ok := root.vals[v]; ok {
		return root
	}
	if root.imported == nil {
		root.imported = map[*ir.Var]bool{}
		seen := map[*ir.Package]bool{root.Pkg: true}
		var visit func(p *ir.Package)
		visit = func(p *ir.Package) {
			if p == nil {
				return
			}
			for _, imp := range p.Imports {
				if imp == nil || imp.Pkg == nil || seen[imp.Pkg] {
					continue
				}
				seen[imp.Pkg] = true
				for _, iv := range imp.Pkg.Vars {
					root.imported[iv] = true
				}
				visit(imp.Pkg)
			}
		}
		visit(root.Pkg)
	}
	if !root.imported[v] {
		return nil
	}
	val, err := root.Eval(v.Init)
	if err != nil {
		return nil
	}
	root.vals[v] = copyOf(val, v.Init.ExprType())
	return root
}

// programRoot is the outermost scope of the run env belongs to, through the
// scopes a snapshot was taken of.
func (env *Env) programRoot() *Env {
	e := env
	for {
		for e.parent != nil {
			e = e.parent
		}
		if e.origin == nil {
			return e
		}
		e = e.origin
	}
}
