package lower

import "duckfam.us/sngl/ir"

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
	if opts.Platform == "" {
		return nil
	}
	surfaces, err := findSurfaces(pkg, opts)
	if err != nil {
		return err
	}
	if surfaces != nil && surfaces.doc != nil {
		// The target writes its document from this one; it reads the mark
		// rather than deciding again after inlining.
		surfaces.doc.Document = true
	}
	composeOverriddenBuiltins(pkg, opts, surfaces)
	return nil
}

// composeOverriddenBuiltins makes a builtin node this target overrides an
// ordinary component: the override is the body the target renders, so the
// node is composed away like any other component with one, rather than handed
// to a backend as the construct its mark names. On html that is sngl:ui/nav's
// stack and page, whose overrides are html's own primitives.
//
// The mark is taken off a per-build copy of the declaration, not the
// declaration: library IR is shared between the builds one process runs, and
// another target may render the same builtin itself. The copy keeps the
// override body, the methods and everything else, so the only thing a node
// pointed at it loses is the kind.
//
// The nav nodes inside a surface other than the document are left the
// builtins they are, for passNavigation: a stack there navigates in place, and
// the target's own navigation is the document's.
func composeOverriddenBuiltins(pkg *ir.Package, opts Options, surfaces *surfaceSet) {
	copies := map[*ir.Component]*ir.Component{}
	composed := func(c *ir.Component) *ir.Component {
		if c == nil || c.Builtin == "" {
			return nil
		}
		if cp, ok := copies[c]; ok {
			return cp
		}
		var cp *ir.Component
		if _, ok := ir.ComponentOverride(c, opts.Platform, opts.Language); ok {
			dup := *c
			dup.Builtin = ""
			cp = &dup
		}
		copies[c] = cp
		return cp
	}
	for _, o := range ir.Owners(pkg) {
		_ = ir.WalkStmts(o.Stmts(), func(s ir.Stmt) error {
			inst, ok := s.(*ir.NodeInst)
			if !ok || (surfaces.inSurface(inst) && isAnyNavNode(inst)) {
				return nil
			}
			if cp := composed(inst.Component); cp != nil {
				inst.Component = cp
			}
			return nil
		})
	}
}

// isAnyNavNode reports whether n is one of sngl:ui/nav's builtin nodes.
func isAnyNavNode(n *ir.NodeInst) bool {
	if n.Component == nil {
		return false
	}
	switch n.Component.Builtin {
	case ir.BuiltinNavStack, ir.BuiltinNavPage, ir.BuiltinNavLink:
		return true
	}
	return false
}
