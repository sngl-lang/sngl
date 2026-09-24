package bubbletea

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/codegen/lang/golang"
	"git.duckfam.us/jonathan/sngl/internal/htmlutil"
	"git.duckfam.us/jonathan/sngl/ir"
)

// irViewContext tracks state during IR-based View() code generation.
type irViewContext struct {
	gc          *golang.GoIRContext
	ctx         *codegen.CodegenCtx
	scaleFactor int
	widgetCount int
	widgets     []widgetInfo
	buf         *strings.Builder
	indent      int
	vertical    bool
	inComponent bool
	slotVar     string

	// overlays accumulates modal/drawer Overlay primitives encountered while
	// rendering the body. They are NOT joined inline; instead each records the
	// Go variable holding its rendered box plus its placement/dim, and
	// emitIRView composites them over the joined content at the end.
	overlays []pendingOverlay
}

// pendingOverlay records one Overlay primitive deferred out of the inline join
// for post-content compositing. boxVar is the Go string var holding the
// rendered overlay box (empty when the overlay's `if open` gate is false).
// placementExpr is a Go expression yielding the placement string ("center" for
// modals; the drawer's `side` for drawers).
type pendingOverlay struct {
	boxVar string
	// placementExpr is the Go expression for the placement. placementLit is the
	// statically-known placement string when placement is a literal (modal:
	// "center"), or "" when dynamic (drawer side may be a runtime prop) — the
	// compositing emitter uses it to pick a single branch and avoid a constant
	// `if "center" == "center"` comparison.
	placementExpr string
	placementLit  string
	dim           bool
}

func (vc *irViewContext) line(format string, args ...any) {
	fmt.Fprintf(vc.buf, "%s"+format+"\n", append([]any{strings.Repeat("\t", vc.indent)}, args...)...)
}

// emitIRStmt renders an imperative IR statement (e.g. a NoTernary-hoisted
// `var __ltN` decl or its value-only If) to Go via the shared Go IR context,
// honoring the current indent.
func (vc *irViewContext) emitIRStmt(s ir.Stmt) {
	for _, l := range vc.gc.EvalStmt(s) {
		vc.line("%s", l)
	}
}

// requireImport registers a Go import on the context's gc; nil-safe.
func (vc *irViewContext) requireImport(path string) {
	if vc.gc != nil {
		vc.gc.RequireImport(path)
	}
}

func emitIRView(b *strings.Builder, info *irAnalysis, ctx *codegen.CodegenCtx, gc *golang.GoIRContext, cfg Config) {
	b.WriteString("func (m Model) View() tea.View {\n")

	wins := ctx.Windows()
	if len(wins) == 0 || len(wins[0].Body) == 0 {
		b.WriteString("\treturn tea.NewView(\"\")\n")
		b.WriteString("}\n\n")
		return
	}

	bodyStmts := wins[0].Body

	vc := &irViewContext{
		gc:          gc,
		ctx:         ctx,
		scaleFactor: cfg.ScaleFactor,
		widgets:     info.widgets,
		buf:         &strings.Builder{},
		indent:      1,
	}

	vc.renderBody(bodyStmts, "content")

	// Overlay (modal/drawer) box vars are assigned inside their `if open { ... }`
	// gate during the body render, but composited after content — so declare
	// them at view scope BEFORE the body so they're in scope and default "".
	for _, ov := range vc.overlays {
		fmt.Fprintf(b, "\tvar %s string\n", ov.boxVar)
	}
	b.WriteString(vc.buf.String())

	// Toast overlay
	if info.NeedsToast {
		b.WriteString("\tif len(m.toasts) > 0 {\n")
		b.WriteString("\t\tt := m.toasts[0]\n")
		b.WriteString("\t\tvar bg string\n")
		b.WriteString("\t\tswitch t.variant {\n")
		b.WriteString("\t\tcase \"success\": bg = \"#2e7d32\"\n")
		b.WriteString("\t\tcase \"error\": bg = \"#c62828\"\n")
		b.WriteString("\t\tcase \"warn\", \"warning\": bg = \"#f57f17\"\n")
		b.WriteString("\t\tdefault: bg = \"#1565c0\"\n")
		b.WriteString("\t\t}\n")
		b.WriteString("\t\ttoastStyle := lipgloss.NewStyle().Padding(0, 1).Background(lipgloss.Color(bg)).Foreground(lipgloss.Color(\"#ffffff\"))\n")
		b.WriteString("\t\tcontent = lipgloss.JoinVertical(lipgloss.Left, content, toastStyle.Render(t.message))\n")
		b.WriteString("\t}\n")
	}

	// Overlay compositing. Each modal/drawer box is composited over the joined
	// content here, AFTER the body and toast, so it overlays the whole display
	// rather than joining inline. A "center" placement centers (modal, dimming
	// the background); any other placement names a drawer side. The tui helpers
	// no-op when the box var is "" (overlay closed), so this stays correct
	// without threading the `open` expr — the `if open` gate leaves boxVar "".
	for _, ov := range vc.overlays {
		gc.RequireImport(tuiImportPath)
		dim := "false"
		if ov.dim {
			dim = "true"
		}
		switch ov.placementLit {
		case "center":
			fmt.Fprintf(b, "\tcontent = tui.OverlayCenter(content, %s, m.width, m.height, %s)\n", ov.boxVar, dim)
		case "":
			// Dynamic placement (drawer side may be a runtime value): branch at
			// runtime between center and side.
			fmt.Fprintf(b, "\tif %s == \"center\" {\n", ov.placementExpr)
			fmt.Fprintf(b, "\t\tcontent = tui.OverlayCenter(content, %s, m.width, m.height, %s)\n", ov.boxVar, dim)
			b.WriteString("\t} else {\n")
			fmt.Fprintf(b, "\t\tcontent = tui.OverlaySide(content, %s, %s, m.width, m.height)\n", ov.boxVar, ov.placementExpr)
			b.WriteString("\t}\n")
		default:
			// Known side literal (drawer left/right/top/bottom).
			fmt.Fprintf(b, "\tcontent = tui.OverlaySide(content, %s, %s, m.width, m.height)\n", ov.boxVar, ov.placementExpr)
		}
	}

	b.WriteString("\tv := tea.NewView(content)\n")
	b.WriteString("\tv.AltScreen = true\n")
	b.WriteString("\treturn v\n")
	b.WriteString("}\n\n")
}

func emitIRComponentMethod(b *strings.Builder, cc *codegen.ComponentCtx, ctx *codegen.CodegenCtx, gc *golang.GoIRContext, cfg Config) {
	methodName := golang.ComponentRenderMethod(cc.Component.Name)

	var params []string
	for _, p := range cc.Props {
		goType := golang.IRTypeToGo(p.Type)
		params = append(params, p.Name+" "+goType)
	}
	hasSlot := cc.Component.ChildrenType != nil
	if hasSlot {
		params = append(params, "slotContent string")
	}

	fmt.Fprintf(b, "func (m Model) %s(%s) string {\n", methodName, strings.Join(params, ", "))

	compGC := gc.ForComponent(cc.Component)
	for _, p := range cc.Props {
		compGC = compGC.WithLocal(p.Name)
	}

	var slotVar string
	if hasSlot {
		slotVar = "slotContent"
	}

	vc := &irViewContext{
		gc:          compGC,
		ctx:         ctx,
		scaleFactor: cfg.ScaleFactor,
		buf:         &strings.Builder{},
		indent:      1,
		inComponent: true,
		slotVar:     slotVar,
	}

	vc.renderBody(cc.Body, "result")
	b.WriteString(vc.buf.String())
	b.WriteString("\treturn result\n")

	b.WriteString("}\n\n")
}

// --- IR view rendering ---

// renderChild renders one child of a joining container into its own part var,
// or emits it where it stands when it renders nothing to join.
func (vc *irViewContext) renderChild(child ir.Stmt, childVar, childrenVar string) {
	if !rendersPart(child) {
		vc.renderStmt(child, "")
		return
	}
	vc.line("var %s string", childVar)
	vc.renderStmt(child, childVar)
	vc.line("%s = append(%s, %s)", childrenVar, childrenVar, childVar)
}

// renderBody emits a body as one value in `single`, or as a list of parts
// joined into it.
//
// The schedules are dropped first. A timer primitive stands in the tree where
// it was written -- which is what answers the branch and the component
// boundary around it, and what AnalyzeCommon reads to arm it from Init() --
// but it draws nothing, so a body is one widget or several by what it renders
// and not by how many schedules it also placed.
func (vc *irViewContext) renderBody(stmts []ir.Stmt, single string) {
	stmts = codegen.WithoutSchedules(stmts)
	if len(stmts) == 1 {
		vc.line("var %s string", single)
		vc.renderStmt(stmts[0], single)
		return
	}
	vc.line("var parts []string")
	for i, child := range stmts {
		vc.renderChild(child, fmt.Sprintf("part%d", i), "parts")
	}
	vc.line(`%s := lipgloss.JoinVertical(lipgloss.Left, parts...)`, single)
}

// rendersPart reports whether a view statement produces a string for its
// parent to join. The rest are emitted as imperative Go where they stand — a
// hoisted `var __ltN` and its value-only If, a synthesized counter — and
// joining an empty part for one puts a blank line in the rendered box.
func rendersPart(s ir.Stmt) bool {
	switch n := s.(type) {
	case *ir.NodeInst:
		// A schedule is not a widget. The timer primitive stays where it was
		// written so that the branch and the component boundary around it are
		// answered by the tree; what reads it is AnalyzeCommon, and Init()
		// arms what it found. Nothing is drawn for it here, and a part joined
		// for one is a blank line in the box.
		return !ir.IsTimerPrimitive(n.Component)
	case *ir.LocalVar:
		return false
	case *ir.If:
		return !n.FromTernary
	case *ir.Assign, *ir.CallStmt, *ir.Return, *ir.Emit, *ir.Toggle, *ir.CanvasRedrawStmt,
		*ir.Break, *ir.Continue:
		return false
	}
	return true
}

func (vc *irViewContext) renderStmt(stmt ir.Stmt, resultVar string) {
	switch s := stmt.(type) {
	case *ir.NodeInst:
		if ir.IsWindowNode(s) {
			// A window only appears at top level; one in a view tree is unexpected.
			panic(fmt.Sprintf("bubbletea: unexpected nested Window in view tree: %#v", s))
		}
		if ir.IsTimerPrimitive(s.Component) {
			return // see rendersPart
		}
		vc.renderNode(s, resultVar)
	case *ir.If:
		if s.FromTernary {
			// NoTernary hoists `var __ltN` + this value-only If (Assign bodies,
			// no NodeInst children) before the widget whose prop reads __ltN.
			// Emit it as imperative Go so the temp is assigned in scope; the
			// structural renderIf path would drop the Assign bodies.
			vc.emitIRStmt(s)
			return
		}
		vc.renderIf(s, resultVar)
	case *ir.For:
		vc.renderFor(s, resultVar)
	case *ir.SlotInst:
		// Slot in a user component body — substitute the caller's joined
		// children, threaded in as slotVar when the view function was opened.
		if vc.slotVar != "" {
			vc.line(`%s = %s`, resultVar, vc.slotVar)
		}
	case *ir.ErrorBoundary:
		for _, child := range s.Children {
			vc.renderStmt(child, resultVar)
		}
	case *ir.LocalVar:
		// A LocalVar in the view body is the `var __ltN` decl NoTernary hoists
		// before the widget consuming it (its FromTernary If assigns it). Emit
		// so the temp is declared in the view scope.
		vc.emitIRStmt(s)
	case *ir.Assign, *ir.CallStmt, *ir.Return, *ir.Emit, *ir.Toggle, *ir.CanvasRedrawStmt,
		*ir.Break, *ir.Continue:
		// Imperative stmts have no visual rendering — skipped.
	case *ir.ContextProvider:
		panic(fmt.Sprintf("bubbletea: ContextProvider should be lowered before view emission: %#v", s))
	default:
		panic(fmt.Sprintf("bubbletea.renderStmt: unhandled ir.Stmt %T", s))
	}
}

func (vc *irViewContext) renderIf(s *ir.If, resultVar string) {
	// Defer the conditional syntax to the Go language driver.
	vc.line("%s", vc.gc.IfHead(s, vc.gc.EvalExpr(s.Cond)))
	vc.indent++
	for _, child := range s.Body {
		vc.renderStmt(child, resultVar)
	}
	vc.indent--
	if len(s.Else) > 0 {
		vc.line("%s", vc.gc.ElseHead())
		vc.indent++
		for _, child := range s.Else {
			vc.renderStmt(child, resultVar)
		}
		vc.indent--
	}
	vc.line("%s", vc.gc.BlockEnd())
}

func (vc *irViewContext) renderFor(s *ir.For, resultVar string) {
	iterExpr := vc.gc.EvalExpr(s.Iter)

	// Defer the loop header to the Go language driver so loop semantics
	// (single/two-var ordering, &-bound element index loops) live in one place
	// rather than being re-implemented per platform.
	loopGC := vc.gc
	if s.Key != "" && s.Key != "_" {
		loopGC = loopGC.WithLocal(s.Key)
	}
	if s.Value != "" && s.Value != "_" {
		loopGC = loopGC.WithLocal(s.Value)
	}
	savedGC := vc.gc
	vc.gc = loopGC

	loopVar := resultVar + "Items"
	vc.line("var %s []string", loopVar)
	vc.line("%s", vc.gc.ForHead(s, iterExpr))
	vc.indent++
	// The view body may not reference the loop vars; suppress unused errors.
	if s.Key != "" && s.Key != "_" {
		vc.line("_ = %s", s.Key)
	}
	if s.Value != "" && s.Value != "_" {
		vc.line("_ = %s", s.Value)
	}

	innerVar := resultVar + "Item"
	vc.line("var %s string", innerVar)
	for _, child := range s.Body {
		vc.renderStmt(child, innerVar)
	}
	vc.line("%s = append(%s, %s)", loopVar, loopVar, innerVar)

	vc.indent--
	vc.line("}")
	sep := `""`
	if vc.vertical {
		sep = `"\n"`
	}
	vc.requireImport("strings")
	vc.line(`%s = strings.Join(%s, %s)`, resultVar, loopVar, sep)

	if len(s.Else) > 0 {
		vc.line("if len(%s) == 0 {", iterExpr)
		vc.indent++
		for _, child := range s.Else {
			vc.renderStmt(child, resultVar)
		}
		vc.indent--
		vc.line("}")
	}

	vc.gc = savedGC
}

func (vc *irViewContext) renderNode(n *ir.NodeInst, resultVar string) {
	// Canvas2D node — rasterise inline each frame into a terminal string.
	if c := vc.ctx.Canvases.ForNode(n); c != nil {
		vc.renderCanvas(n, c, resultVar)
		return
	}

	// User component — call render method
	if n.Component != nil && vc.isUserComponent(n.Component) {
		vc.renderUserComponent(n, resultVar)
		return
	}

	// Inlined blueprint primitive — render off its blueprint record. Every
	// stdlib wrapper inlines to one of these at lower time, and the #[intrinsic]
	// id on the declaration is what identifies one; see bubbletea.sngl +
	// blueprint.go.
	if btIntrinsic(n) != "" {
		vc.renderBlueprint(n, resultVar)
		return
	}

	if codegen.DeclinesNode(n.Component) {
		vc.ctx.Fail(codegen.UnimplementedNode(n.Component, n.AST, n.Name, "bubbletea"))
		return
	}
	// A user component pruned from Pkg.Components for an empty body.
	vc.renderRawTerminal(n, resultVar)
}

func (vc *irViewContext) isUserComponent(comp *ir.Component) bool {
	return slices.Contains(vc.ctx.Pkg.Components, comp)
}

// tooltipFocusExpr finds the first focusable descendant within a tooltip's
// children and returns a Go boolean expression that is true when that node is
// focused (read off the __focused prop passFocusOrder injected). It returns ""
// when there is no focusable descendant, or when the descendant's focus
// expression is not statically available (a loop-body trigger), so the caller
// can skip emitting a tooltip reveal rather than produce invalid Go.
func tooltipFocusExpr(stmts []ir.Stmt, gc *golang.GoIRContext) string {
	for _, s := range stmts {
		switch n := s.(type) {
		case *ir.NodeInst:
			if codegen.NodeProp(n, "__focused") != nil {
				if expr := nodeStaticFocusExpr(n, gc); expr != "" {
					return expr
				}
			}
			if expr := tooltipFocusExpr(n.Children, gc); expr != "" {
				return expr
			}
		case *ir.SlotInst:
			if expr := tooltipFocusExpr(n.Children, gc); expr != "" {
				return expr
			}
		case *ir.If:
			if expr := tooltipFocusExpr(n.Body, gc); expr != "" {
				return expr
			}
			if expr := tooltipFocusExpr(n.Else, gc); expr != "" {
				return expr
			}
		}
	}
	return ""
}

// renderBlueprint renders one inlined blueprint primitive (Layout, Styled, or
// Widget) off the blueprint record extracted from its props, rather than keying
// on a stdlib component name. The primitives are produced when a stdlib
// component's bubbletea override body is inlined (see bubbletea.sngl).
func (vc *irViewContext) renderBlueprint(n *ir.NodeInst, resultVar string) {
	bp := extractBlueprint(n)
	styleFields := codegen.NodeStyleFields(n)
	style := buildIRStyleExpr(styleFields, vc.gc, vc.scaleFactor)

	switch bp.Kind {
	case bpFlow:
		vc.renderFlow(n, resultVar)

	case bpSpan:
		// A Span outside a Flow: the family's own membership rule makes that
		// impossible from source, so reaching here means one was rendered
		// without its flow. Render it as its own flow rather than dropping it.
		vc.line(`%s = ""`, resultVar)
		vc.renderSpan(n, resultVar, spanCascade{})

	case bpOverlay:
		// An Overlay (modal/drawer body) must NOT join inline into resultVar.
		// Render its children (joined vertically, then styled) into a private
		// box var and record a pending overlay; emitIRView composites it over
		// the whole content after the body is built. resultVar is left as the
		// caller initialized it ("") so the overlay contributes nothing inline.
		//
		// Because the Overlay sits under `if open { ... }`, this block only runs
		// when open — so boxVar stays "" when closed and the tui overlay helper
		// no-ops on an empty box. We declare boxVar at view scope (hoisted by
		// emitIRView) and assign it here.
		boxVar := fmt.Sprintf("overlay%d", len(vc.overlays))
		childrenVar := boxVar + "Children"
		vc.line("var %s []string", childrenVar)
		prevVertical := vc.vertical
		vc.vertical = true
		for i, child := range n.Children {
			vc.renderChild(child, fmt.Sprintf("%s_%d", boxVar, i), childrenVar)
		}
		vc.vertical = prevVertical
		vc.line(`%s = lipgloss.JoinVertical(lipgloss.Left, %s...)`, boxVar, childrenVar)
		if style != "lipgloss.NewStyle()" {
			vc.line(`%s = %s.Render(%s)`, boxVar, style, boxVar)
		}
		placementExpr := `"center"`
		placementLit := "center"
		if bp.Placement != nil {
			placementExpr = vc.gc.EvalExpr(bp.Placement)
			if lit, ok := codegen.IRLiteralString(bp.Placement); ok {
				placementLit = lit
			} else {
				placementLit = ""
			}
		}
		vc.overlays = append(vc.overlays, pendingOverlay{
			boxVar:        boxVar,
			placementExpr: placementExpr,
			placementLit:  placementLit,
			dim:           bp.Dim,
		})

	case bpLayout:
		// Join children vertically/horizontally, then apply style if present.
		childrenVar := resultVar + "Children"
		vc.line("var %s []string", childrenVar)
		prevVertical := vc.vertical
		vc.vertical = bp.Join == joinVertical
		for i, child := range n.Children {
			vc.renderChild(child, fmt.Sprintf("%s_%d", resultVar, i), childrenVar)
		}
		vc.vertical = prevVertical
		// tooltip: reveal the body text (dim) below the trigger while the wrapped
		// focusable descendant is focused. The `tooltip` prop carries the text;
		// the focus expression comes from the focusable descendant's injected
		// __focused prop. If no static focus expression is available (e.g. the
		// trigger sits inside a for-loop, whose key isn't in scope here), the
		// tooltip degrades to never showing rather than emitting invalid Go.
		if tip := codegen.NodeProp(n, "tooltip"); tip != nil {
			if focusExpr := tooltipFocusExpr(n.Children, vc.gc); focusExpr != "" {
				vc.requireImport("fmt")
				vc.line(`if %s {`, focusExpr)
				vc.line(`%s = append(%s, lipgloss.NewStyle().Faint(true).Render(fmt.Sprint(%s)))`, childrenVar, childrenVar, vc.gc.EvalExpr(tip))
				vc.line(`}`)
			}
		}
		if bp.Join == joinVertical {
			vc.line(`%s = lipgloss.JoinVertical(lipgloss.Left, %s...)`, resultVar, childrenVar)
		} else {
			vc.line(`%s = lipgloss.JoinHorizontal(lipgloss.Top, %s...)`, resultVar, childrenVar)
		}
		if style != "lipgloss.NewStyle()" {
			vc.line(`%s = %s.Render(%s)`, resultVar, style, resultVar)
		}

	case bpStyled:
		// Render the content expression through lipgloss. A present `__focused`
		// prop (injected by passFocusOrder for focusable primitives) adds the
		// cursor prefix.
		content := `""`
		if bp.Content != nil {
			content = vc.gc.EvalExpr(bp.Content)
		}
		vc.requireImport("fmt")
		if fp := codegen.NodeProp(n, "__focused"); fp != nil {
			vc.line(`%sFocused := %s`, resultVar, vc.gc.EvalExpr(fp))
			vc.line(`%sPrefix := " "`, resultVar)
			vc.line(`if %sFocused { %sPrefix = ">" }`, resultVar, resultVar)
			vc.line(`%s = %s.Render(%sPrefix + " " + fmt.Sprint(%s))`, resultVar, style, resultVar, content)
		} else {
			vc.line(`%s = %s.Render(fmt.Sprint(%s))`, resultVar, style, content)
		}

	case bpWidget:
		// Render the bubbles model's view. The widget's field name + view method
		// come from the widgetInfo collected during analyzeIR; the view walk and
		// the analyze walk traverse the window body in the same order, so the
		// per-Widget counter stays in lockstep with the allocated field names.
		idx := vc.widgetCount
		vc.widgetCount++
		if idx >= len(vc.widgets) {
			// The analyze walk and this view walk traverse the window body in the
			// same order, so the per-Widget counter must stay in lockstep with the
			// allocated field names. Running past the end means the two walks
			// desynced — a codegen bug, not a recoverable state.
			panic(fmt.Sprintf("bubbletea: widget index %d out of range (%d widgets analyzed); view/analyze walk desync", idx, len(vc.widgets)))
		}
		w := vc.widgets[idx]
		vc.line(`%s = m.%s%s`, resultVar, w.fieldName, w.model.View)
	}
}

func (vc *irViewContext) renderUserComponent(n *ir.NodeInst, resultVar string) {
	methodName := golang.ComponentRenderMethod(n.Name)

	var args []string
	if n.Component != nil {
		for _, p := range n.Component.Props {
			propVal := codegen.NodeProp(n, p.Name)
			if propVal != nil {
				args = append(args, vc.gc.EvalExpr(propVal))
			} else if p.Default != nil {
				args = append(args, golang.IRLiteralToGo(p.Default))
			} else {
				args = append(args, `""`)
			}
		}
	}

	if n.Component != nil && n.Component.ChildrenType != nil && len(n.Children) > 0 {
		slotVar := resultVar + "Slot"
		vc.renderChildrenNodes(n.Children, slotVar)
		args = append(args, slotVar)
	}

	vc.line(`%s = m.%s(%s)`, resultVar, methodName, strings.Join(args, ", "))
}

func (vc *irViewContext) renderRawTerminal(n *ir.NodeInst, resultVar string) {
	styleFields := codegen.NodeStyleFields(n)
	style := buildIRStyleExpr(styleFields, vc.gc, vc.scaleFactor)

	// Join layout
	if joinExpr := codegen.NodeProp(n, "join"); joinExpr != nil {
		if s, ok := codegen.IRLiteralString(joinExpr); ok {
			childrenVar := resultVar + "Children"
			vc.line("var %s []string", childrenVar)
			prevVertical := vc.vertical
			vc.vertical = s == "vertical"
			for i, child := range n.Children {
				vc.renderChild(child, fmt.Sprintf("%s_%d", resultVar, i), childrenVar)
			}
			vc.vertical = prevVertical
			if s == "vertical" {
				vc.line(`%s = lipgloss.JoinVertical(lipgloss.Left, %s...)`, resultVar, childrenVar)
			} else {
				vc.line(`%s = lipgloss.JoinHorizontal(lipgloss.Top, %s...)`, resultVar, childrenVar)
			}
			if style != "lipgloss.NewStyle()" {
				vc.line(`%s = %s.Render(%s)`, resultVar, style, resultVar)
			}
			return
		}
	}

	// Default: styled content
	content := `""`
	if v := codegen.NodeProp(n, "content"); v != nil {
		content = vc.gc.EvalExpr(v)
	}

	vc.requireImport("fmt")
	if fp := codegen.NodeProp(n, "__focused"); fp != nil {
		vc.line(`%sFocused := %s`, resultVar, vc.gc.EvalExpr(fp))
		vc.line(`%sPrefix := " "`, resultVar)
		vc.line(`if %sFocused { %sPrefix = ">" }`, resultVar, resultVar)
		vc.line(`%s = %s.Render(%sPrefix + " " + fmt.Sprint(%s))`, resultVar, style, resultVar, content)
	} else {
		vc.line(`%s = %s.Render(fmt.Sprint(%s))`, resultVar, style, content)
	}
}

func (vc *irViewContext) renderChildrenNodes(children []ir.Stmt, resultVar string) {
	var nodes []*ir.NodeInst
	for _, s := range children {
		if n, ok := s.(*ir.NodeInst); ok {
			nodes = append(nodes, n)
		}
	}
	if len(nodes) == 1 {
		vc.line("var %s string", resultVar)
		vc.renderNode(nodes[0], resultVar)
	} else if len(nodes) > 1 {
		childrenParts := resultVar + "Parts"
		vc.line("var %s []string", childrenParts)
		for i, child := range nodes {
			childVar := fmt.Sprintf("%sPart%d", resultVar, i)
			vc.line("var %s string", childVar)
			vc.renderNode(child, childVar)
			vc.line("%s = append(%s, %s)", childrenParts, childrenParts, childVar)
		}
		vc.line(`%s := lipgloss.JoinVertical(lipgloss.Left, %s...)`, resultVar, childrenParts)
	} else {
		vc.line(`%s := ""`, resultVar)
	}
}

// buildIRStyleExpr builds a Go lipgloss style chain from IR style fields.
func buildIRStyleExpr(styles []codegen.StyleField, gc *golang.GoIRContext, scaleFactor int) string {
	chain := []string{"lipgloss.NewStyle()"}
	// Sort by property name: a lipgloss builder chain is order-independent, and
	// a stable key order keeps the emitted source reproducible.
	sorted := append([]codegen.StyleField(nil), styles...)
	slices.SortFunc(sorted, func(a, b codegen.StyleField) int { return strings.Compare(a.Name, b.Name) })
	for _, sf := range sorted {
		if call := irStyleCall(sf.Name, sf.Value, gc, scaleFactor); call != "" {
			chain = append(chain, call)
		}
	}
	return strings.Join(chain, ".\n")
}

// lipglossColor renders a color style value for `lipgloss.Color(...)`. A
// constant `#rrggbb` literal folds to its quoted CSS string at compile time.
// A non-constant color expression evaluates to a snglcolor.Color, which
// lipgloss can't consume — call its String() to get the CSS form. Anything
// else (a string literal or expression already yielding a CSS color) passes
// through unchanged.
func lipglossColor(expr ir.Expr, gc *golang.GoIRContext) string {
	if css, ok := htmlutil.ColorExprToCSS(expr); ok {
		// Constant fold — never evaluate expr, so a color struct-lit doesn't
		// side-effect an (unused) snglcolor import via gc.EvalExpr.
		return strconv.Quote(css)
	}
	val := gc.EvalExpr(expr)
	if expr != nil && ir.IsColorStruct(expr.ExprType()) {
		return val + ".String()"
	}
	return val
}

// cellVal is a style value in terminal cells.
//
// A measurement is a magnitude per base, and a terminal has a size for exactly
// one of them: a cell is so many pixels wide, while an em is a font the host
// chose and a pct is a fraction of a box lipgloss never reports. So the first
// declared base is the one read and the rest contribute nothing -- which is
// the same answer `2rem` and `50pct` already got, arrived at deliberately.
//
// Reading the magnitude at all is what was missing: a unit value is a record
// in Go, so `paddingLeft=20px` came out as `max(1, Measurement{Px: 20}/8)` and
// no generated program carrying a padding, margin, width or height has ever
// compiled. Nothing in lib/ rendered a body until now, which is why the one
// spelling every document uses did not reach it sooner.
func cellVal(expr ir.Expr, gc *golang.GoIRContext, scaleFactor int) string {
	val := gc.EvalExpr(expr)
	ud := ir.UnitDeclOf(exprType(expr))
	if ud == nil {
		return scaleVal(val, scaleFactor)
	}
	bases := ud.Bases()
	if len(bases) == 0 {
		return scaleVal(val, scaleFactor)
	}
	if lit, ok := expr.(*ir.Literal); ok {
		mag, base, ok := ir.UnitMagnitude(lit)
		if ok && base != bases[0].Name {
			return "0"
		}
		if ok {
			return scaleVal(strconv.Itoa(int(mag)), scaleFactor)
		}
	}
	if ud.IsSingleBase() {
		return scaleVal("int("+val+")", scaleFactor)
	}
	return scaleVal("int("+val+"."+golang.ExportName(bases[0].Name)+")", scaleFactor)
}

// exprType is expr's type, nil-safely: a style field may carry no expression
// at all.
func exprType(expr ir.Expr) *ir.Type {
	if expr == nil {
		return nil
	}
	return expr.ExprType()
}

// scaleVal wraps a numeric value expression with pixel-to-cell scaling.
func scaleVal(val string, scaleFactor int) string {
	var n int
	if _, err := fmt.Sscanf(val, "%d", &n); err == nil {
		if n == 0 {
			return "0"
		}
		scaled := max(n/scaleFactor, 1)
		return fmt.Sprintf("%d", scaled)
	}
	return fmt.Sprintf("max(1, %s / %d)", val, scaleFactor)
}

func irStyleCall(prop string, expr ir.Expr, gc *golang.GoIRContext, scaleFactor int) string {
	// color/background fold a constant #rrggbb to a CSS string without
	// evaluating expr; handle them before the eager EvalExpr below so a
	// constant color struct-lit doesn't register an unused snglcolor import.
	switch prop {
	case "color":
		return fmt.Sprintf("Foreground(lipgloss.Color(%s))", lipglossColor(expr, gc))
	case "background":
		return fmt.Sprintf("Background(lipgloss.Color(%s))", lipglossColor(expr, gc))
	case "borderColor":
		return fmt.Sprintf("BorderForeground(lipgloss.Color(%s))", lipglossColor(expr, gc))
	}

	val := gc.EvalExpr(expr)

	switch prop {
	case "padding":
		return fmt.Sprintf("Padding(%s)", cellVal(expr, gc, scaleFactor))
	case "paddingTop":
		return fmt.Sprintf("PaddingTop(%s)", cellVal(expr, gc, scaleFactor))
	case "paddingRight":
		return fmt.Sprintf("PaddingRight(%s)", cellVal(expr, gc, scaleFactor))
	case "paddingBottom":
		return fmt.Sprintf("PaddingBottom(%s)", cellVal(expr, gc, scaleFactor))
	case "paddingLeft":
		return fmt.Sprintf("PaddingLeft(%s)", cellVal(expr, gc, scaleFactor))
	case "margin":
		return fmt.Sprintf("Margin(%s)", cellVal(expr, gc, scaleFactor))
	case "marginTop":
		return fmt.Sprintf("MarginTop(%s)", cellVal(expr, gc, scaleFactor))
	case "marginRight":
		return fmt.Sprintf("MarginRight(%s)", cellVal(expr, gc, scaleFactor))
	case "marginBottom":
		return fmt.Sprintf("MarginBottom(%s)", cellVal(expr, gc, scaleFactor))
	case "marginLeft":
		return fmt.Sprintf("MarginLeft(%s)", cellVal(expr, gc, scaleFactor))
	case "width":
		return fmt.Sprintf("Width(%s)", cellVal(expr, gc, scaleFactor))
	case "height":
		return fmt.Sprintf("Height(%s)", cellVal(expr, gc, scaleFactor))
	case "maxWidth":
		return fmt.Sprintf("MaxWidth(%s)", cellVal(expr, gc, scaleFactor))
	case "maxHeight":
		return fmt.Sprintf("MaxHeight(%s)", cellVal(expr, gc, scaleFactor))
	case "fontWeight":
		if val == `"bold"` {
			return "Bold(true)"
		}
	case "fontStyle":
		if val == `"italic"` {
			return "Italic(true)"
		}
	case "textAlign":
		switch val {
		case `"center"`:
			return "AlignHorizontal(lipgloss.Center)"
		case `"right"`:
			return "AlignHorizontal(lipgloss.Right)"
		case `"left"`:
			return "AlignHorizontal(lipgloss.Left)"
		}
	case "borderWidth":
		return "Border(lipgloss.NormalBorder())"
	case "opacity":
		return "Faint(true)"
	}
	return ""
}
