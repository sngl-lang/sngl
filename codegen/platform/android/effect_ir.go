package android

import (
	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/ir"
)

// renderEffect emits an `effect` node as Compose's own lifetime bracket.
//
// Android is the platform that asks for the node rather than the calls
// passEffect lowers it to (Features.Effects), because DisposableEffect is the
// same construct: a block that runs when it enters the composition, an
// onDispose that runs when it leaves, and a key that ends one lifetime and
// begins the next when it changes. Nothing here schedules or cancels anything
// -- Compose does, and it does it against a composition the framework owns
// rather than a teardown an exit path has to remember to call.
//
// A key of `Unit` is the idiom for "once, for as long as this composition
// lives", which is what an effect with no `on` means.
//
// onDispose is mandatory in the API, so a bracket with only an @mount still
// emits an empty one.
func (cc *irComposeContext) renderEffect(n *ir.NodeInst) {
	mount := codegen.NodeHandler(n, "mount")
	unmount := codegen.NodeHandler(n, "unmount")
	if (mount == nil || mount.Func == nil) && (unmount == nil || unmount.Func == nil) {
		return
	}

	key := "Unit"
	if on := effectKeyArg(n); on != nil {
		key = cc.kc.EvalExpr(on)
	}

	cc.line("DisposableEffect(%s) {", key)
	cc.indent++
	// Bound once, on the way in, and read by both halves. onDispose runs when
	// the composition leaves -- by which time the state the key reads already
	// holds the next value, so a handler evaluating the key expression there
	// would see what it is not tearing down. Capturing it here is what makes
	// `@unmount(v)` the outgoing key, which is the rule the lowered form keeps
	// with a saved var.
	if wantsKey(mount) || wantsKey(unmount) {
		cc.line("val %s = %s", effectKeyLocal, key)
	}
	cc.emitEffectBody(mount)
	cc.line("onDispose {")
	cc.indent++
	cc.emitEffectBody(unmount)
	cc.indent--
	cc.line("}")
	cc.indent--
	cc.line("}")
}

// effectKeyLocal holds the value the running lifetime is keyed on, for as long
// as that lifetime lasts.
const effectKeyLocal = "__snglEffectKey"

// wantsKey reports whether a handler declared a parameter to receive the key.
func wantsKey(h *ir.EventHandler) bool {
	return h != nil && h.Func != nil && len(h.Func.Params) > 0
}

// emitEffectBody writes one side of the bracket, aliasing the captured key to
// the handler's parameter when it declared one.
//
// The mount body goes in a block of its own so two handlers naming their
// parameter the same thing do not shadow one another: onDispose nests inside
// this scope, and Kotlin warns on every effect that used one name twice, which
// is the obvious thing to write.
func (cc *irComposeContext) emitEffectBody(h *ir.EventHandler) {
	if h == nil || h.Func == nil {
		return
	}
	block := wantsKey(h)
	if block {
		cc.line("run {")
		cc.indent++
		cc.line("val %s = %s", h.Func.Params[0].Name, effectKeyLocal)
	}
	for _, stmt := range h.Func.Block {
		for _, line := range cc.kc.EvalStmt(stmt) {
			cc.line("%s", line)
		}
	}
	if block {
		cc.indent--
		cc.line("}")
	}
}

// effectKeyArg is the `on` argument, or nil when the call site wrote none.
func effectKeyArg(n *ir.NodeInst) ir.Expr {
	for _, arg := range n.Props {
		if arg.Name == "on" {
			return arg.Value
		}
	}
	return nil
}

// isEffectNode reports whether a node is a lifetime bracket, by the kind its
// declaration carries rather than by its name.
func isEffectNode(n *ir.NodeInst) bool {
	return n != nil && n.Component != nil && n.Component.Builtin == ir.BuiltinEffect
}
