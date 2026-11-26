package lower

import (
	"strings"

	"duckfam.us/sngl/ir"
)

// passForeignPrimitive rejects a platform primitive this build's target cannot
// render.
//
// An #[intrinsic] on a component names the emitting codegen's dispatch key,
// namespaced by the platform that answers to it ("android:Column"). The
// namespace is what makes one platform's primitive meaningless to another: it
// is not a component with a portable meaning, it is the widget one backend
// emits. Reaching it from a build targeting something else has no answer, and
// producing one anyway is how `android.Switch(...)` under --platform html
// became `<Switch onCheckedChange="...">`.
//
// So the build fails, naming both platforms -- unless the target says it
// implements the id, which is what Options.ClaimsIntrinsic is for. A platform
// is free to render another's primitive; it just has to say so rather than have
// it assumed.
var passForeignPrimitive = pass{
	name: "ForeignPrimitive",
	// Not capability-gated: this is about what the target can name at all,
	// not about a language feature it lacks.
	enabled: func(Features) bool { return true },
	apply: func(pkg *ir.Package, _ Features, opts Options) error {
		if opts.Platform == "" {
			// No target, so no target's namespace to be foreign to. The
			// platform-agnostic readers (LSP, fmt, doc) see every primitive.
			return nil
		}
		var bad error
		ir.WalkStmts(pkg, func(s ir.Stmt) error {
			n, ok := s.(*ir.NodeInst)
			if !ok || n.Component == nil || bad != nil {
				return nil
			}
			if f := n.Component.Foreign; n.Component.IsNative() {
				// A host's own widget is the same case from the other side:
				// only the platform built on that host can call it.
				if opts.RendersNative == nil || !opts.RendersNative(f.Scheme) {
					bad = ir.Decline(opts.Platform, "component %q is %s's %s (#[%s.native]) and this build targets %s, which renders no %s widget",
						n.Component.Name, f.Scheme, f.Name, f.Scheme, opts.Platform, f.Scheme)
				}
				return nil
			}
			ns, _, ok := strings.Cut(n.Component.Intrinsic, ":")
			if !ok {
				// A component of another platform's package that is no
				// primitive -- html's wildcard `element`, `html.div` -- is
				// that platform's all the same.
				if plat := platformPackage(n.Component.PkgPath()); plat != "" && plat != opts.Platform {
					bad = ir.UnimplementedNode(n.Component, n.AST, n.Component.Name, opts.Platform)
				}
				return nil
			}
			if ns == opts.Platform {
				return nil
			}
			if opts.ClaimsIntrinsic != nil && opts.ClaimsIntrinsic(n.Component.Intrinsic) {
				return nil
			}
			bad = ir.Decline(opts.Platform, "component %q is %s's platform primitive (#[intrinsic(%q)]) and this build targets %s, which declares no implementation of it",
				n.Component.Name, ns, n.Component.Intrinsic, opts.Platform)
			return nil
		})
		if bad != nil {
			return bad
		}
		return declineForeignPlatformCalls(pkg, opts)
	},
}

// declineForeignPlatformCalls declines a call of a function another platform's
// package declares -- `gtk4.quit()`, `gtk4.pressKey(w, …)` in a bubbletea
// build. The same case as a primitive from the side of its functions: what
// they do is what that platform's runtime does, and their bodies hand its
// widgets to its C.
func declineForeignPlatformCalls(pkg *ir.Package, opts Options) error {
	var bad error
	_ = ir.Walk(pkg, func(n ir.Node) error {
		c, ok := n.(*ir.Call)
		if !ok || c.Func == nil {
			return nil
		}
		plat := platformPackage(c.Func.PkgPath())
		if plat == "" || plat == opts.Platform {
			return nil
		}
		at := ""
		if c.AST != nil && c.AST.Pos.IsValid() {
			at = c.AST.Pos.String() + ": "
		}
		bad = ir.Decline(opts.Platform, "%s%s is declared by sngl:platform/%s, and this build targets %s", at, c.Func.Name, plat, opts.Platform)
		return ir.SkipAll
	})
	return bad
}

// platformPackage is the platform a package of sngl:platform/ belongs to --
// "win32" for sngl:platform/win32/api -- and "" for any other package.
func platformPackage(path string) string {
	rest, ok := strings.CutPrefix(path, "sngl:platform/")
	if !ok {
		return ""
	}
	plat, _, _ := strings.Cut(rest, "/")
	return plat
}
