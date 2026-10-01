package android

import (
	"slices"

	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/ir"
)

// viewStmts is what MainScreen draws: the package body, which is the
// application's view and holds the Screen a window is on this platform -- or,
// for a harness that isolated a component, that component's body.
func viewStmts(ctx *codegen.CodegenCtx) []ir.Stmt {
	if wins := ctx.Windows(); len(wins) > 0 {
		return wins[0].Body
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
		cc.kc.RequireImport("androidx.compose.foundation.layout.safeDrawingPadding")
		cc.line("Column(modifier = Modifier.safeDrawingPadding()) {")
		cc.indent++
		outerAxis, outerRoot := cc.parentAxis, cc.atRoot
		cc.parentAxis, cc.atRoot = "Column", false
		for _, s := range stmts {
			cc.renderStmt(s)
		}
		cc.parentAxis, cc.atRoot = outerAxis, outerRoot
		cc.indent--
		cc.line("}")
		return
	}
	if len(stmts) == 1 {
		cc.renderStmt(stmts[0])
		return
	}
	cc.line("Column {")
	cc.indent++
	for _, s := range stmts {
		cc.renderStmt(s)
	}
	cc.indent--
	cc.line("}")
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
	cc.kc.RequireImport("androidx.activity.compose.BackHandler")
	cc.kc.RequireImport("androidx.compose.ui.platform.LocalContext")
	cc.kc.RequireImport("android.app.Activity")
	cc.line("val __activity = LocalContext.current as? Activity")
	cc.line("BackHandler {")
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
	cc.renderContent(n.Children)
	if gated {
		cc.indent--
		cc.line("}")
	}
}
