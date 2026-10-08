package lower

import (
	"strconv"

	"duckfam.us/sngl/ir"
)

// passHoistBodyTypes renames a body-local struct, enum or unit whose name
// something else in the package already spells.
//
//	component first  { struct Local { a int } … }
//	component second { struct Local { b string } … }  ->  Local__second
//
// Only a name already claimed moves, and the first claimant keeps its
// spelling, so what a body compiles to depends on that body rather than on
// whether some unrelated component reuses the name. The reservation pass runs
// over the whole package first, so a top-level declaration always wins
// whatever order registration put the two in.
//
// Setting Name reaches every reference, which all ride on the declaration
// pointer. The one exception is ir.Func.Receiver, a type name held as a
// string; the declaration's own member table is what says which funcs those
// are.
var passHoistBodyTypes = pass{
	name:    "HoistBodyTypes",
	enabled: func(Features) bool { return true },
	apply:   hoistBodyTypes,
}

// bodyType is one renameable declaration, reached through the three kinds'
// common shape rather than a type switch at every step.
type bodyType struct {
	name    func() string
	setName func(string)
	owner   string
	methods map[string]*ir.Func
}

func hoistBodyTypes(pkg *ir.Package, _ Features, _ Options) error {
	if pkg == nil {
		return nil
	}
	var body []bodyType
	taken := map[string]bool{}

	reserve := func(name string) {
		if name != "" {
			taken[name] = true
		}
	}
	collect := func(bt bodyType) {
		if bt.owner == "" {
			reserve(bt.name())
			return
		}
		body = append(body, bt)
	}

	for _, sd := range pkg.Structs {
		collect(bodyType{func() string { return sd.Name }, func(s string) { sd.Name = s }, sd.BodyOwner, sd.Methods})
	}
	for _, ed := range pkg.Enums {
		collect(bodyType{func() string { return ed.Name }, func(s string) { ed.Name = s }, ed.BodyOwner, ed.Methods})
	}
	for _, ud := range pkg.Units {
		collect(bodyType{func() string { return ud.Name }, func(s string) { ud.Name = s }, ud.BodyOwner, ud.Methods})
	}
	if len(body) == 0 {
		return nil
	}
	// A type shares the host namespace with more than the other types: Go gets
	// `type Local struct` beside `func Local()`, which is what the checker's
	// interim error measured a body-local name against. A method is spelled
	// under its receiver on every target and claims nothing here.
	for _, fn := range pkg.Funcs {
		if fn.Receiver == "" {
			reserve(fn.Name)
		}
	}
	nestedComp := ir.BodyOwners(pkg)
	for _, comp := range pkg.Components {
		if nestedComp[comp] == nil {
			reserve(comp.Name)
		}
	}

	for _, bt := range body {
		old := bt.name()
		if old == "" {
			continue
		}
		if !taken[old] {
			taken[old] = true
			continue
		}
		fresh := freshTypeName(old, bt.owner, taken)
		taken[fresh] = true
		bt.setName(fresh)
		for _, m := range bt.methods {
			// Receiver is the type's name written out, so a method whose host
			// spelling this pass has just changed would otherwise be emitted
			// on a type nobody declares.
			if m.Receiver == old {
				m.Receiver = fresh
			}
		}
	}
	return nil
}

// freshTypeName is `Local__first` -- the name, the body it was written in --
// and a counter only where that is itself claimed, which takes two bodies of
// one name declaring one type name each.
func freshTypeName(name, owner string, taken map[string]bool) string {
	base := name + "__" + owner
	if !taken[base] {
		return base
	}
	for n := 2; ; n++ {
		cand := base + "__" + strconv.Itoa(n)
		if !taken[cand] {
			return cand
		}
	}
}
