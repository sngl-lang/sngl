// Package names allocates identifiers into one flat namespace: reserve the
// spellings that are fixed, then request a preferred name and get back one
// that does not collide.
//
// It models no namespace of its own. Go's package scope holds types, funcs,
// vars and consts together while a method set is separate, and Kotlin and
// JavaScript divide them differently again — so a caller holding two
// namespaces builds two registries.
package names

import "strconv"

// Registry is one namespace: the names already spoken for, and a source of
// names that are not.
//
// Reserve is order-independent, Unique is not, so a caller must reserve
// everything it knows about before the first Unique — a fixed spelling
// arriving after a generated name has taken it has nothing left to bend.
type Registry struct {
	taken map[string]bool
}

// New returns a registry holding the given names.
func New(reserved ...string) *Registry {
	r := &Registry{taken: make(map[string]bool, len(reserved))}
	r.Reserve(reserved...)
	return r
}

// Reserve records names that must be spelled exactly as given.
func (r *Registry) Reserve(names ...string) {
	if r.taken == nil {
		r.taken = map[string]bool{}
	}
	for _, n := range names {
		r.taken[n] = true
	}
}

// Free reports whether name is still available. For a caller that has to
// allocate a *set* of related names together and cannot take them one at a
// time — see lower's __lambdaN / __lambdaN_caps pair.
func (r *Registry) Free(name string) bool {
	return !r.taken[name]
}

// Unique reserves and returns preferred, or the first of preferred2,
// preferred3, ... that is free. The suffix is the one import aliases take.
func (r *Registry) Unique(preferred string) string {
	name := preferred
	for i := 2; r.taken[name]; i++ {
		name = preferred + strconv.Itoa(i)
	}
	r.Reserve(name)
	return name
}
