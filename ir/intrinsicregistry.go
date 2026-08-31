package ir

import (
	"slices"
	"sync"
)

// The intrinsics a check has registered, keyed by id.
//
// An intrinsic is a declaration: `#[intrinsic("list.push", mutates,
// mutatesReceiver)] func list<T>.push(item T) list<T>` states the id, the
// signature, and what a call costs, and the checker reads all three off it.
// This used to be a hand-written table in Go beside them, which is a second
// record of the same facts and lost every comparison against the first: it
// could not name a type the library declares (`date` and `PluralKey` read as
// dyn), it could not name a type variable (every collection signature read as
// list<dyn>), and where lowering rewrites a call's parameters it gave up and
// recorded none at all. It also claimed `int` for five void functions, which
// nothing noticed for as long as nothing read a return type off it.
//
// So the declarations populate it instead. What is left in Go is the lookup
// the phases that cannot see a declaration need: three lowering passes
// synthesize a call to an intrinsic the program never wrote, and `ir` cannot
// import the checker.
//
// A registered def is therefore only as complete as the packages a check
// loaded -- a target's intrinsics (html.frontend) arrive with its package, not
// with lib/. Anything wanting the whole contract has to walk the source, which
// is what lib's TestEveryIntrinsicHasAnEmitter does.
var (
	intrinsicsMu sync.RWMutex
	intrinsics   = map[string]*IntrinsicDef{}
)

// RegisterIntrinsic records what a #[intrinsic] declaration says. Called by the
// checker as it registers the declaration, so a later check of the same library
// replaces the entry with an equal one.
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
// "list.push"), or ok=false if no loaded package declares it. Lets the checker
// and lowering passes read intrinsic metadata by id rather than matching method
// names.
func IntrinsicByName(name string) (IntrinsicDef, bool) {
	def := LookupIntrinsic(name)
	if def == nil {
		return IntrinsicDef{}, false
	}
	return *def, true
}

// AllIntrinsics returns every registered definition, ordered by id. What it
// covers depends on which packages have been loaded in this process.
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
