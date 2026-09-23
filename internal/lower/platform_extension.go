package lower

import "git.duckfam.us/jonathan/sngl/ir"

// passPlatformExtensionBody specializes each *ir.Component whose
// PlatformOverrides map has an entry for the active platform by swapping
// that entry into Component.Body, and its PlatformOverrides entry into
// Component.Vars. The checker is platform-agnostic and
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
	enabled: func(c Features) bool { return true },
	apply:   lowerPlatformExtensionBody,
}

func lowerPlatformExtensionBody(pkg *ir.Package, _ Features, opts Options) error {
	ir.SpecializeForTarget(pkg, opts.Platform, opts.Language)
	return nil
}
