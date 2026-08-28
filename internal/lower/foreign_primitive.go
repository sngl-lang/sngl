package lower

import (
	"fmt"
	"strings"

	"git.duckfam.us/jonathan/sngl/ir"
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
	enabled: func(Caps) bool { return true },
	apply: func(pkg *ir.Package, _ Caps, opts Options) error {
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
			ns, _, ok := strings.Cut(n.Component.Intrinsic, ":")
			if !ok || ns == opts.Platform {
				return nil
			}
			if opts.ClaimsIntrinsic != nil && opts.ClaimsIntrinsic(n.Component.Intrinsic) {
				return nil
			}
			bad = fmt.Errorf("component %q is a %s platform primitive (#[intrinsic(%q)]) and this build targets %s, which declares no implementation of it",
				n.Component.Name, ns, n.Component.Intrinsic, opts.Platform)
			return nil
		})
		return bad
	},
}
