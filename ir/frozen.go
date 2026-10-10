package ir

import (
	"fmt"
	"sync"
)

// frozen holds the packages, and their root scopes, that a check sharing
// them has finished with: every later check reads them and none may write
// them. Keyed by identity, so a build's copy of one is not frozen.
var frozen sync.Map

// Freeze marks p, and the scope its declarations are bound in, as read-only.
func Freeze(p *Package) {
	if p == nil {
		return
	}
	frozen.Store(p, true)
	if p.Symbols != nil && p.Symbols.Root != nil {
		frozen.Store(p.Symbols.Root, true)
	}
}

// IsFrozen reports whether p has been frozen.
func IsFrozen(p *Package) bool {
	if p == nil {
		return false
	}
	_, ok := frozen.Load(p)
	return ok
}

// MustWrite panics when p is frozen. A field write cannot be intercepted, so
// the known writers call it, and the library fingerprint catches the rest.
func MustWrite(p *Package, what string) {
	if IsFrozen(p) {
		panic(fmt.Sprintf("ir: %s writes %s, which is frozen", what, p.Path))
	}
}

func (s *Scope) mustWrite(name string) {
	if !s.IsPackageRoot {
		return
	}
	if _, ok := frozen.Load(s); ok {
		panic(fmt.Sprintf("ir: binding %q in a frozen package's scope", name))
	}
}
