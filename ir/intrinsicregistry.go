package ir

import (
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

// RegisterIntrinsic records what a #[intrinsic] declaration says. A later check
// of the same library replaces the entry with an equal one.
func RegisterIntrinsic(def IntrinsicDef) {
	if def.Name == "" {
		return
	}
	intrinsicsMu.Lock()
	defer intrinsicsMu.Unlock()
	intrinsics[def.Name] = &def
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

// AllIntrinsics returns every registered definition, ordered by id.
func AllIntrinsics() []IntrinsicDef {
	intrinsicsMu.RLock()
	defer intrinsicsMu.RUnlock()
	out := make([]IntrinsicDef, 0, len(intrinsics))
	for _, def := range intrinsics {
		out = append(out, *def)
	}
	slices.SortFunc(out, func(a, b IntrinsicDef) int {
		switch {
		case a.Name < b.Name:
			return -1
		case a.Name > b.Name:
			return 1
		}
		return 0
	})
	return out
}
