package android

import (
	"fmt"
	"slices"
	"strings"

	"duckfam.us/sngl/codegen"
	"duckfam.us/sngl/ir"
)

// viewStmts is what MainScreen draws: the package body, which is the
// application's view and holds the Screen a window is on this platform -- or,
// for a harness that isolated a component, that component's body.
func viewStmts(ctx *codegen.CodegenCtx) []ir.Stmt {
	if h := ctx.Harness(); h != nil {
		return h.Body
	}
	if ctx.Pkg == nil {
		return nil
	}
	return ctx.Pkg.Body
}

// screenPos is where a window was written, for a diagnostic about it.
func screenPos(n *ir.NodeInst) string {
	if p := ir.NodePos(n); p.IsValid() {
		return p.String()
	}
	return "window"
}

// renderContent draws a window's content: its one node, or its nodes in a
// Column.
//
// Content that arrives through a slot -- a window that is an instance of its
// own, under a reactive `if`, handed its body by the caller -- is rendered in
// the caller's lambda, where nothing is the window's root, so the inset is
// put on a Column around the insertion here instead. It is also what lays out
// a body of several nodes there: the lambda hands them to the Screen with no
// layout of their own.
func (cc *irComposeContext) renderContent(stmts []ir.Stmt) {
	if len(stmts) == 0 {
		return
	}
	if slices.ContainsFunc(stmts, func(s ir.Stmt) bool { _, ok := s.(*ir.SlotInst); return ok }) {
		outerRoot := cc.atRoot
		cc.atRoot = false
		cc.writtenColumn(fmt.Sprintf("modifier = %s.%s()", cc.kc.Name(ktModifier), cc.kc.Name(ktSafeDrawingPadding)), stmts)
		cc.atRoot = outerRoot
		return
	}
	if len(stmts) == 1 {
		cc.renderStmt(stmts[0])
		return
	}
	cc.writtenColumn("", stmts)
}

// writtenColumn lays stmts out in a Column no vbox wrote, with its arguments.
// It provides the share a child's flex takes there, as a vbox's content
// provides flexShare: through Compose (LocalFlexShare, flexshare.kt), since
// a slot's content is composed where the slot is invoked. Without it a
// flexing child read the screen's share and filled it.
func (cc *irComposeContext) writtenColumn(args string, stmts []ir.Stmt) {
	cc.line("%s%s {", cc.kc.Name(ktColumn), parenthesized(args))
	cc.indent++
	_, called := shippedUses(cc.ctx.Pkg)
	shared := called[flexShareName]
	if shared {
		cc.line("%s(%s provides { w: Float -> %s.weight(w).%s() }) {", cc.kc.Name("androidx.compose.runtime.CompositionLocalProvider"), localFlexShare, cc.kc.Name(ktModifier), cc.kc.Name("androidx.compose.foundation.layout.fillMaxWidth"))
		cc.indent++
	}
	for _, s := range stmts {
		cc.renderStmt(s)
	}
	if shared {
		cc.indent--
		cc.line("}")
	}
	cc.indent--
	cc.line("}")
}

// parenthesized is a call's argument list, or nothing where it has none.
func parenthesized(args string) string {
	if args == "" {
		return ""
	}
	return "(" + args + ")"
}

// renderScreen draws the window's content while it is visible, and answers
// the system back at the root as the window's close: `onBack` runs -- the
// override reports `visible = false` before the window's own `@closed` -- and
// the activity finishes when the window is off screen after it. A hidden
// window is not drawn, so its BackHandler is gone and the back finishes the
// activity as it would with nothing to close.
func (cc *irComposeContext) renderScreen(n *ir.NodeInst) {
	var body []string
	if lam, ok := codegen.NodeProp(n, "onBack").(*ir.Lambda); ok && lam.Func != nil {
		for _, stmt := range lam.Func.Block {
			body = append(body, cc.kc.EvalStmt(stmt)...)
		}
	}
	gated := false
	if v := cc.screen.Visible(); v != nil {
		cc.line("if (%s) {", cc.kc.EvalExpr(v))
		cc.indent++
		gated = true
	}
	cc.line("val __activity = %s.current as? %s", cc.kc.Name(ktLocalContext), cc.kc.Name("android.app.Activity"))
	cc.line("%s {", cc.kc.Name("androidx.activity.compose.BackHandler"))
	for _, l := range body {
		cc.line("    %s", l)
	}
	if shown := cc.screen.Shown(cc.kc.EvalExpr); shown != "" {
		cc.line("    if (!(%s)) {", shown)
		cc.line("        __activity?.finish()")
		cc.line("    }")
	} else {
		cc.line("    __activity?.finish()")
	}
	cc.line("}")
	if !screenDrawsBar(n) {
		cc.renderContent(n.Children)
		if gated {
			cc.indent--
			cc.line("}")
		}
		return
	}
	title := `""`
	if t := codegen.NodeProp(n, "title"); t != nil {
		title = cc.kc.EvalExpr(t)
	}
	can := "false"
	if c := screenCanGoBack(n); c != nil {
		can = cc.kc.EvalExpr(c)
	}
	var up []string
	if lam, ok := codegen.NodeProp(n, "onUp").(*ir.Lambda); ok && lam.Func != nil {
		for _, stmt := range lam.Func.Block {
			up = append(up, cc.kc.EvalStmt(stmt)...)
		}
	}
	upTag := ""
	if id := userTestTag(n); id != "" {
		upTag = id + "__back"
	}
	cc.line("SnglAppBar(title = %s, canGoBack = %s, upTag = %q, onUp = {", title, can, upTag)
	for _, l := range up {
		cc.line("    %s", l)
	}
	cc.line("}, icon = %s, actions = {", cc.screenIcon(n))
	cc.indent++
	if sc := n.Slots["actions"]; sc != nil {
		outerRoot := cc.atRoot
		cc.atRoot = false
		for _, s := range sc.Body {
			cc.renderStmt(s)
		}
		cc.atRoot = outerRoot
	}
	cc.indent--
	cc.line("}) {")
	cc.indent++
	cc.renderContent(n.Children)
	cc.indent--
	cc.line("}")
	// After the content, so it is asked before a NavHost's own, which the
	// content composes: the dispatcher asks the last added first.
	if can != "false" {
		cc.line("%s(enabled = %s) {", cc.kc.Name("androidx.activity.compose.BackHandler"), can)
		for _, l := range up {
			cc.line("    %s", l)
		}
		cc.line("}")
	}
	if gated {
		cc.indent--
		cc.line("}")
	}
}

// screenIcon is the bar's icon: the window's favicon, drawn by the Screen's
// icon slot, or the launcher's where the window gives none and the output
// has one.
func (cc *irComposeContext) screenIcon(n *ir.NodeInst) string {
	if sc := n.Slots["icon"]; sc != nil && len(sc.Body) > 0 {
		var b strings.Builder
		sub := *cc
		sub.buf, sub.indent, sub.atRoot = &b, cc.indent+1, false
		for _, s := range sc.Body {
			sub.renderStmt(s)
		}
		return "{\n" + b.String() + strings.Repeat("    ", cc.indent) + "}"
	}
	if cc.icon != "" {
		return fmt.Sprintf("{ %s(%s(%s), contentDescription = null, modifier = %s.%s(24.dp)) }",
			cc.kc.Name("androidx.compose.foundation.Image"), cc.kc.Name("androidx.compose.ui.res.painterResource"), cc.icon,
			cc.kc.Name(ktModifier), cc.kc.Name("androidx.compose.foundation.layout.size"))
	}
	return "null"
}

// screenDrawsBar reports whether a Screen draws its app bar: given a way back
// that is more than the literal `false` a window's default is, or actions.
func screenDrawsBar(n *ir.NodeInst) bool {
	if screenCanGoBack(n) != nil {
		return true
	}
	sc := n.Slots["actions"]
	return sc != nil && len(sc.Body) > 0
}

// screenCanGoBack is a Screen's `canGoBack`, or nil where it is the literal
// `false`.
func screenCanGoBack(n *ir.NodeInst) ir.Expr {
	c := codegen.NodeProp(n, "canGoBack")
	if lit, ok := c.(*ir.Literal); c == nil || ok && lit.Value == "false" {
		return nil
	}
	return c
}
