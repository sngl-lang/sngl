package interp

import (
	"fmt"

	"git.duckfam.us/jonathan/sngl/ir"
)

// BuildEnv creates an Env populated for evaluation in the scope of the given
// package and optional component. When compName is empty, only package-level
// consts/funcs are populated; when set, the named component's vars/consts/
// props/funcs are also seeded and env.Comp/BodyStmts are wired up.
func BuildEnv(pkg *ir.Package, compName string) (*Env, error) {
	env := newPackageEnv(pkg)
	if compName == "" {
		for _, c := range pkg.Consts {
			env.Set(c, evalInit(env, c.Init))
		}
		return env, nil
	}

	comp := FindComponent(pkg, compName)
	if comp == nil {
		return nil, fmt.Errorf("component %q not found", compName)
	}

	// Seed package-level vars and consts so component bodies and any nested
	// child components can read/write global state. A component-local
	// declaration of the same name is a different symbol, so it binds
	// separately rather than overwriting.
	seedPackageState(env, pkg)

	for _, v := range comp.Vars {
		env.Set(v, evalInit(env, v.Init))
	}
	for _, p := range comp.Props {
		env.Set(p.Sym, evalInit(env, p.Default))
	}
	env.Comp = comp
	env.BodyStmts = comp.Body
	return env, nil
}

// BuildProgramEnv creates an Env for running pkg as a program: its package
// state seeded, and its windows and package body as what mounts.
func BuildProgramEnv(pkg *ir.Package) *Env {
	env := newPackageEnv(pkg)
	seedPackageState(env, pkg)
	body := make([]ir.Stmt, 0, len(pkg.Windows)+len(pkg.Body))
	for _, w := range pkg.Windows {
		body = append(body, w)
	}
	env.BodyStmts = append(body, pkg.Body...)
	return env
}

func seedPackageState(env *Env, pkg *ir.Package) {
	for _, v := range pkg.Vars {
		env.Set(v, evalInit(env, v.Init))
	}
	for _, c := range pkg.Consts {
		env.Set(c, evalInit(env, c.Init))
	}
}

func newPackageEnv(pkg *ir.Package) *Env {
	env := NewEnv()
	env.Pkg = pkg
	env.Units = buildUnitTables(pkg)

	// Seed context defaults so ContextRead expressions evaluate to their
	// declared default values before any t.setContext() override is applied.
	if len(pkg.Contexts) > 0 {
		env.ContextVals = make(map[*ir.Context]any, len(pkg.Contexts))
		for _, ctx := range pkg.Contexts {
			if ctx.Default != nil {
				v, err := env.Eval(ctx.Default)
				if err == nil {
					env.ContextVals[ctx] = v
					if ctx.Name == "locale" {
						if s, ok := v.(string); ok {
							env.Locale = s
						}
					}
				}
			}
		}
	}

	// An imported package's consts are seeded first, so a `calc.WIDTH` written
	// here resolves to the same value the imported code reads. They are bound
	// by symbol, so a name this package also declares still wins: its own
	// binding is written after.
	seedImportedConsts(env, pkg, map[*ir.Package]bool{})
	return env
}

// seedImportedConsts binds the consts of every package this one imports,
// transitively, so a const named through a namespace has a value at runtime.
// Depth first, so a package's own imports are seeded before it is.
func seedImportedConsts(env *Env, pkg *ir.Package, seen map[*ir.Package]bool) {
	if pkg == nil || seen[pkg] {
		return
	}
	seen[pkg] = true
	for _, imp := range pkg.Imports {
		if imp == nil || imp.Pkg == nil {
			continue
		}
		seedImportedConsts(env, imp.Pkg, seen)
		for _, c := range imp.Pkg.Consts {
			env.Set(c, evalInit(env, c.Init))
		}
	}
}

func evalInit(env *Env, expr ir.Expr) any {
	if expr == nil {
		return nil
	}
	v, err := env.Eval(expr)
	if err == nil {
		return v
	}
	return nil
}

func buildUnitTables(pkg *ir.Package) map[string]*unitTable {
	tables := map[string]*unitTable{}
	add := func(units []*ir.UnitDef) {
		for _, u := range units {
			t := buildUnitTableFromDef(u)
			for suffix := range t.Conversions {
				tables[suffix] = t
			}
		}
	}
	// Precedence, widest last: what the program declares, then what a dot
	// import lifted into its scope, then what an import holds under a name of
	// its own.
	addIfNew := func(units []*ir.UnitDef) {
		for _, u := range units {
			t := buildUnitTableFromDef(u)
			for suffix := range t.Conversions {
				if _, exists := tables[suffix]; !exists {
					tables[suffix] = t
				}
			}
		}
	}
	add(pkg.Units)
	if pkg.Symbols != nil {
		var found []*ir.UnitDef
		pkg.Symbols.EachSymbol(func(sym ir.Symbol) bool {
			if u, ok := sym.(*ir.UnitDef); ok {
				found = append(found, u)
			}
			return true
		})
		addIfNew(found)
	}
	// An aliased import binds a namespace, not the declarations inside it, so
	// the walk above never sees them: `import time "sngl:time"` left the
	// interpreter with no table for `ms`/`s`/`m`/`h` at all, and a duration
	// literal was then its own bare number -- `1m` and `60s` compared as 1
	// against 60 and reported unequal. A dot import worked, which is what hid
	// it.
	for _, imp := range pkg.Imports {
		if imp != nil && imp.Pkg != nil {
			addIfNew(imp.Pkg.Units)
		}
	}
	return tables
}

func buildUnitTableFromDef(u *ir.UnitDef) *unitTable {
	t := &unitTable{
		Conversions: make(map[string]float64),
		BaseOf:      make(map[string]string),
	}
	if len(u.Suffixes) > 0 {
		t.Base = u.Suffixes[0].Name
	}
	for _, s := range u.Suffixes {
		t.Conversions[s.Name] = s.Factor
		t.BaseOf[s.Name] = s.BaseName
	}
	for _, b := range u.Bases() {
		t.Bases = append(t.Bases, b.Name)
	}
	return t
}

func propSym(comp *ir.Component, name string) ir.Symbol {
	for _, p := range comp.Props {
		if p.Name == name && p.Sym != nil {
			return p.Sym
		}
	}
	return nil
}

// FindComponent returns the named component or nil.
func FindComponent(pkg *ir.Package, name string) *ir.Component {
	for _, c := range pkg.Components {
		if c.Name == name {
			return c
		}
	}
	return nil
}
