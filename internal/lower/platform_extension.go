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
	composeOverriddenBuiltins(pkg, opts)
	return nil
}

// composeOverriddenBuiltins makes a builtin node this target overrides an
// ordinary component: the override is the body the target renders, so the
// node is composed away like any other component with one, rather than handed
// to a backend as the construct its mark names. On gtk4 and fyne that is
// `ui.window`, whose override is the platform's Toplevel primitive.
//
// The mark is taken off a per-build copy of the declaration, not the
// declaration: library IR is shared between the builds one process runs, and
// another target may render the same builtin itself. The copy keeps the
// override body, the methods and everything else, so the only thing a node
// pointed at it loses is the kind.
//
// A window at the root of a file is held in pkg.Windows rather than in the
// body, so once it is an ordinary node it joins the body it stood in, ahead of
// the statements there: the package body is the application's view, and a
// root node is attached to the application (ir.NodeAppendChild with
// ir.AppParent), which is each platform's to answer. Phase C3 deletes
// pkg.Windows and this with it.
func composeOverriddenBuiltins(pkg *ir.Package, opts Options) {
	if pkg == nil || opts.Platform == "" {
		return
	}
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
	var repoint func(n ir.Node) error
	repoint = func(n ir.Node) error {
		if inst, ok := n.(*ir.NodeInst); ok {
			if cp := composed(inst.Component); cp != nil {
				inst.Component = cp
				catchInBody(inst)
				populateRestSlot(inst)
			}
		}
		return nil
	}
	var windows []ir.Stmt
	var kept []*ir.Window
	for _, w := range pkg.Windows {
		if composed(w.Component) != nil {
			windows = append(windows, w)
			continue
		}
		kept = append(kept, w)
	}
	for _, o := range ir.Owners(pkg) {
		if o.Win != nil {
			_ = ir.WalkStmts([]ir.Stmt{o.Win}, func(s ir.Stmt) error { return repoint(s) })
		}
		_ = ir.WalkStmts(o.Stmts(), func(s ir.Stmt) error { return repoint(s) })
	}
	if len(windows) == 0 {
		return
	}
	pkg.Windows = kept
	pkg.Body = append(windows, pkg.Body...)
}

// populateRestSlot puts a window's body where a component's population goes.
// The checker hands a window's `component content(v)` over as NodeInst.Params
// beside the children, because a window is its own construct there; composed
// as an ordinary component, the body is the population of its rest slot, which
// is what a splice binds the argument of.
func populateRestSlot(inst *ir.NodeInst) {
	if inst.Params == nil {
		return
	}
	rest := inst.Component.RestSlot()
	if rest == nil {
		return
	}
	if inst.Slots == nil {
		inst.Slots = map[string]*ir.SlotContent{}
	}
	inst.Slots[rest.Name] = &ir.SlotContent{Params: []*ir.Param{inst.Params}, Body: inst.Children}
	inst.Params, inst.Children = nil, nil
}

// catchInBody keeps a window's `@error` the outermost boundary of what it
// renders. The checker held the handler back as NodeInst.ErrorHandler rather
// than an event of the node, because a window was its own construct; composed
// as an ordinary component the node goes, so the handler is put where a
// boundary's is, around the body, and passErrorScope resolves a raise from a
// component the window renders to it as it did to the window.
func catchInBody(inst *ir.NodeInst) {
	if inst.ErrorHandler == nil {
		return
	}
	inst.Children = []ir.Stmt{&ir.ErrorBoundary{Handler: inst.ErrorHandler, Children: inst.Children}}
	inst.ErrorHandler = nil
}
