package ir

import "fmt"

// Freeze marks p, the scope its declarations are bound in, and each of its
// declarations as read-only: the check that loaded p has ended, and every
// later check and build reads it. The flag is on each object, so a build's
// copy of one is not frozen (the cloner leaves it behind) and a frozen library
// is collected with its last reader.
func Freeze(p *Package) {
	if p == nil {
		return
	}
	p.Frozen = true
	if p.Symbols != nil && p.Symbols.Root != nil {
		p.Symbols.Root.Frozen = true
	}
	for _, c := range p.Components {
		c.Frozen = true
		for _, fn := range c.Funcs {
			fn.Frozen = true
		}
	}
	for _, fns := range [][]*Func{p.Funcs, p.Macros} {
		for _, fn := range fns {
			fn.Frozen = true
		}
	}
	for _, s := range p.Structs {
		s.Frozen = true
	}
	for _, e := range p.Enums {
		e.Frozen = true
	}
	for _, u := range p.Units {
		u.Frozen = true
	}
	// A package's Contexts also holds the ones it adopted from its imports,
	// which their own package freezes.
	for _, ctx := range p.Contexts {
		if ctx.Pkg == p {
			ctx.Frozen = true
		}
	}
	for _, vs := range [][]*Var{p.Consts, p.Vars} {
		for _, v := range vs {
			v.Frozen = true
		}
	}
}

// IsFrozen reports whether p has been frozen.
func IsFrozen(p *Package) bool {
	return p != nil && p.Frozen
}

// MustWriteDecl panics when sym is frozen. A field write cannot be
// intercepted, so the known writers call it, and the library fingerprint
// catches the rest. It asks the declaration rather than its package because a
// build's copy names the frozen package it came from and is not frozen.
func MustWriteDecl(sym Symbol, what string) {
	switch d := sym.(type) {
	case *StructDef:
		d.mustWrite(what)
	case *EnumDef:
		d.mustWrite(what)
	case *UnitDef:
		d.mustWrite(what)
	case *Component:
		d.mustWrite(what)
	case *Func:
		d.mustWrite(what)
	case *Var:
		d.mustWrite(what)
	}
}

func (d *DeclInfo) mustWrite(what string) {
	if d.Frozen {
		panic(fmt.Sprintf("ir: %s writes %s, which is frozen", what, d.PkgPath()))
	}
}

func (s *Scope) mustWrite(name string) {
	if s.IsPackageRoot && s.Frozen {
		panic(fmt.Sprintf("ir: binding %q in a frozen package's scope", name))
	}
}
