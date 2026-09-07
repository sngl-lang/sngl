package codegen

import (
	"sort"

	"git.duckfam.us/jonathan/sngl/ir"
)

// collectUsedUnits returns every unit declaration the package needs a type
// for: the ones it declares, plus the ones it only uses. `measurement` is
// declared in sngl:ui, so a program that writes `7px` never puts it on
// pkg.Units, and a backend that emits one type per unit emitted none --
// leaving generated Go referring to a `Measurement` that does not exist.
//
// Declaration order is not meaningful across two sources, so the result is
// sorted by name to keep generated output stable.
func collectUsedUnits(pkg *ir.Package) []*ir.UnitDef {
	if pkg == nil {
		return nil
	}
	seen := map[*ir.UnitDef]bool{}
	var out []*ir.UnitDef
	add := func(t *ir.Type) {
		ud := ir.UnitDeclOf(t)
		if ud == nil || seen[ud] {
			return
		}
		seen[ud] = true
		out = append(out, ud)
	}
	for _, u := range pkg.Units {
		if !seen[u] {
			seen[u] = true
			out = append(out, u)
		}
	}
	for _, v := range pkg.Vars {
		add(v.Type)
	}
	// An expression's type covers the rest: a prop, a local, a return value
	// and a binary result all carry one.
	_ = ir.WalkExprs(pkg, func(e ir.Expr) error {
		add(e.ExprType())
		return nil
	})
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
