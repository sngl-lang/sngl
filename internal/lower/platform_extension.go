package lower

import "git.duckfam.us/jonathan/sngl/ir"

// passPlatformExtensionBody specializes each *ir.Component whose
// PlatformBodies map has an entry for the active platform by swapping
// that entry into Component.Body. The checker is platform-agnostic and
// collects every registered platform's `component sngl.X { platform <p>
// { ... } }` body into the map; the active platform is chosen here.
//
// Runs first so every subsequent pass (computed/reactivity/inline/etc.)
// sees the specialized body rather than an empty stdlib stub.
//
// When opts.Platform is empty (LSP, format, multi-platform discovery),
// the pass is a no-op — components keep whatever Body the checker left
// them with (empty for stdlib abstract components).
var passPlatformExtensionBody = pass{
	name:    "PlatformExtensionBody",
	enabled: func(c Caps) bool { return true },
	apply:   lowerPlatformExtensionBody,
}

func lowerPlatformExtensionBody(pkg *ir.Package, _ Caps, opts Options) error {
	if pkg == nil || opts.Platform == "" {
		return nil
	}
	// PlatformBodies lives on stdlib *ir.Component pointers which are
	// shared with pkg.Symbols.Comps but not stored in pkg.Components
	// (that list holds only user-package components). Walk the symbol
	// table so stdlib extensions get specialized too.
	if pkg.Symbols != nil {
		for _, sym := range pkg.Symbols.Comps {
			comp, ok := sym.(*ir.Component)
			if !ok || comp == nil || comp.PlatformBodies == nil {
				continue
			}
			if body, ok := comp.PlatformBodies[opts.Platform]; ok {
				comp.Body = body
			}
		}
	}
	for _, comp := range pkg.Components {
		if comp == nil || comp.PlatformBodies == nil {
			continue
		}
		if body, ok := comp.PlatformBodies[opts.Platform]; ok {
			comp.Body = body
		}
	}
	return nil
}
