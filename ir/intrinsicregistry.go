package ir

import (
	"cmp"
	"fmt"
	"iter"
	"maps"
	"slices"
	"sync"
)

// The intrinsics a check has registered, keyed by id. Only what the loaded
// packages declare, so a target's (html.frontend) are absent until its package
// is loaded; anything needing the whole set walks the source instead.
//
// It exists because ir cannot import the checker, and three lowering passes
// synthesize a call to an intrinsic with no declaration in hand.
var (
	intrinsicsMu sync.RWMutex
	intrinsics   = map[string]*IntrinsicDef{}
)

// RegisterIntrinsic records what a #[intrinsic] declaration says.
//
// Two declarations claiming one id panics, as a duplicate emitter does in
// codegen's registry: an id is the dispatch key every backend answers to, so
// one naming two declarations means half its call sites reach the wrong
// signature. The mark is reachable only from library source -- user source
// cannot import sngl:internal/marks -- so this is a compiler or stdlib
// programmer error and no program can provoke it.
//
// Re-registering the same declaration is how a second check of the same
// library behaves, and updates the entry. Same declaration means same package
// and same spelling; a duplicate of that within one package is already an
// error where it is declared.
func RegisterIntrinsic(def IntrinsicDef) {
	if def.Name == "" {
		return
	}
	intrinsicsMu.Lock()
	defer intrinsicsMu.Unlock()
	if prev, ok := intrinsics[def.Name]; ok && !prev.sameDeclAs(def) {
		panic(fmt.Sprintf("ir: intrinsic %q is declared twice, as %s and as %s",
			def.Name, prev.declSite(), def.declSite()))
	}
	intrinsics[def.Name] = &def
}

func (d IntrinsicDef) sameDeclAs(other IntrinsicDef) bool {
	return d.Pkg == other.Pkg && d.DeclaredAs == other.DeclaredAs
}

func (d IntrinsicDef) declSite() string {
	if d.Pkg == "" {
		return d.DeclaredAs
	}
	return d.Pkg + "." + d.DeclaredAs
}

// LookupIntrinsic returns the registered definition for an id, or nil when no
// loaded package declares it.
func LookupIntrinsic(name string) *IntrinsicDef {
	if name == "" {
		return nil
	}
	intrinsicsMu.RLock()
	defer intrinsicsMu.RUnlock()
	return intrinsics[name]
}

// IntrinsicByName returns the definition registered for the given id (e.g.
// "list.push"), or ok=false if no loaded package declares it.
func IntrinsicByName(name string) (IntrinsicDef, bool) {
	def := LookupIntrinsic(name)
	if def == nil {
		return IntrinsicDef{}, false
	}
	return *def, true
}

// AllIntrinsics yields every registered definition, ordered by id. The
// snapshot is taken under the lock and yielded without it, so a consumer may
// look one up as it goes.
func AllIntrinsics() iter.Seq[*IntrinsicDef] {
	intrinsicsMu.RLock()
	out := slices.Collect(maps.Values(intrinsics))
	intrinsicsMu.RUnlock()

	slices.SortFunc(out, func(a, b *IntrinsicDef) int { return cmp.Compare(a.Name, b.Name) })
	return slices.Values(out)
}
