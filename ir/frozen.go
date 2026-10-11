package ir

import "fmt"

// Freeze marks p, the scope its declarations are bound in, and each of its
// declarations as read-only: the check that loaded p has ended, and every
// later check and build reads it. A flag on each object rather than a
// registry, so a build's copy of one is not frozen (the cloner leaves the
// flag behind) and a frozen library is collected with its last reader.
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

// MustWrite panics when p is frozen. A field write cannot be intercepted, so
// the known writers call it, and the library fingerprint catches the rest.
func MustWrite(p *Package, what string) {
	if IsFrozen(p) {
		panic(fmt.Sprintf("ir: %s writes %s, which is frozen", what, p.Path))
	}
}

// mustWrite is MustWrite for one declaration, which a build may hold a copy
// of: the copy names the frozen package it came from and is not frozen.
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
