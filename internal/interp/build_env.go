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

	// Register package-level functions (includes stdlib merged by the checker).
	for _, fn := range pkg.Funcs {
		if !fn.IsTest {
			env.SetFunc(fn)
		}
	}
	// Register type-attached stdlib methods (stored in the symbol table, not pkg.Funcs).
	if pkg.Symbols != nil {
		for typeName, methods := range pkg.Symbols.Methods {
			for _, fn := range methods {
				env.Funcs[typeName+"."+fn.Name] = fn
			}
		}
	}

	if compName == "" {
		for _, c := range pkg.Consts {
			env.Consts[c.Name] = evalInit(env, c.Init)
		}
		return env, nil
	}

	comp := FindComponent(pkg, compName)
	if comp == nil {
		if compName == "main" {
			for _, v := range pkg.Vars {
				env.Vars[v.Name] = evalInit(env, v.Init)
			}
			for _, c := range pkg.Consts {
				env.Consts[c.Name] = evalInit(env, c.Init)
			}
			return env, nil
		}
		return nil, fmt.Errorf("component %q not found", compName)
	}

	// Seed package-level vars and consts so component bodies and any nested
	// child components can read/write global state. Component-local vars
	// declared below shadow these by name.
	for _, v := range pkg.Vars {
		env.Vars[v.Name] = evalInit(env, v.Init)
	}
	for _, c := range pkg.Consts {
		env.Consts[c.Name] = evalInit(env, c.Init)
	}

	for _, v := range comp.Vars {
		if v.IsConst {
			env.Consts[v.Name] = evalInit(env, v.Init)
		} else {
			env.Vars[v.Name] = evalInit(env, v.Init)
		}
	}
	for _, p := range comp.Props {
		env.Vars[p.Name] = evalInit(env, p.Default)
	}
	for _, fn := range comp.Funcs {
		env.SetFunc(fn)
	}
	env.Comp = comp
	env.BodyStmts = comp.Body
	return env, nil
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
	add(pkg.Units)
	if pkg.Symbols != nil {
		for _, sym := range pkg.Symbols.Types {
			if u, ok := sym.(*ir.UnitDef); ok {
				t := buildUnitTableFromDef(u)
				for suffix := range t.Conversions {
					if _, exists := tables[suffix]; !exists {
						tables[suffix] = t
					}
				}
			}
		}
	}
	return tables
}

func buildUnitTableFromDef(u *ir.UnitDef) *unitTable {
	t := &unitTable{Conversions: make(map[string]float64)}
	if len(u.Suffixes) > 0 {
		t.Base = u.Suffixes[0].Name
	}
	for _, s := range u.Suffixes {
		t.Conversions[s.Name] = s.Factor
	}
	return t
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
