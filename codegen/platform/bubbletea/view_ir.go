package bubbletea

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"duckfam.us/sngl/codegen"
	"duckfam.us/sngl/codegen/lang/golang"
	"duckfam.us/sngl/internal/htmlutil"
	"duckfam.us/sngl/ir"
)

// irViewContext tracks state during IR-based View() code generation.
type irViewContext struct {
	gc          *golang.GoIRContext
	ctx         *codegen.CodegenCtx
	widgetCount int
	widgets     []widgetInfo
	buf         *strings.Builder
	indent      int
	vertical    bool
	horizontal  bool
	inComponent bool

	// overlays accumulates modal/drawer Overlay primitives encountered while
	// rendering the body. They are NOT joined inline; instead each records the
	// Go variable holding its rendered box plus its placement/dim, and
	// emitIRView composites them over the joined content at the end.
	overlays []pendingOverlay

	focusPos  map[string]bool
	canvasSeq map[string]bool

	// availW and availH are Go ints holding the extent the node rendered next
	// is placed in, "" where nothing knows it; sizeTo is the share of a
	// Layout's free space it grows into.
	availW, availH string
	sizeTo         *growSize

	// joinGap and joinAt are the Layout being rendered's spacing and where it
	// puts its children across it, as Go: what a loop's copies and a branch's
	// nodes among its children are joined with, being its parts too.
	joinGap, joinAt string
	// pictureLook names the look of the box a picture is shown in, whose size
	// the picture is fitted to; "" where it is in no box.
	pictureLook string
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
	// A nested ternary's inner temp is declared inside the outer one's
	// branches, and an unbound enum-typed temp is spelled as the member of its
	// own name.
	_ = ir.WalkStmts(s, func(st ir.Stmt) error {
		if lv, ok := st.(*ir.LocalVar); ok {
			vc.gc = vc.gc.WithLocal(lv.Name)
		}
		return nil
	})
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

	bodyStmts := viewStmts(ctx)
	// No window, which only a package that is not a program has, draws
	// nothing; a harness's isolated component is drawn as the window.
	if len(bodyStmts) == 0 || info.screen == nil && ctx.Harness() == nil {
		b.WriteString("\treturn tea.NewView(\"\")\n")
		b.WriteString("}\n\n")
		return
	}

	vc := &irViewContext{
		gc:      gc,
		ctx:     ctx,
		widgets: info.widgets,
		availW:  "tui.Avail(m.width)",
		availH:  "m.height",
		buf:     &strings.Builder{},
		indent:  1,
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
		params = append(params, bindName(p.Name)+" "+goType)
	}
	params = append(params, slotParams(cc.Component)...)
	availW := ""
	if componentGrows(cc.Component, nil) {
		availW = availParam
		params = append(params, availParam+" int")
	}

	fmt.Fprintf(b, "func (m Model) %s(%s) string {\n", methodName, strings.Join(params, ", "))

	compGC := gc.ForComponent(cc.Component)
	for _, p := range cc.Props {
		compGC = withBinding(compGC, p.Name)
	}

	vc := &irViewContext{
		gc:          compGC,
		ctx:         ctx,
		buf:         &strings.Builder{},
		indent:      1,
		inComponent: true,
		availW:      availW,
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
	vc.appendPart(child, childVar, childrenVar)
}

// appendPart appends part, which child rendered, to list, leaving it out where
// it is empty and child may draw nothing: joining an empty string puts a blank
// line in the box.
func (vc *irViewContext) appendPart(child ir.Stmt, part, list string) {
	if vc.mayDrawNothing(child) {
		vc.line("if %s != \"\" {", part)
		vc.line("\t%s = append(%s, %s)", list, list, part)
		vc.line("}")
		return
	}
	vc.line("%s = append(%s, %s)", list, list, part)
}

// mayDrawNothing reports whether child may render an empty string: a branch
// not taken, a loop over nothing, a node its Style may display none, or a
// component rendered by a method of its own, whose root may be any of those.
// A body rendering no part at all, nil here, draws nothing.
func (vc *irViewContext) mayDrawNothing(child ir.Stmt) bool {
	switch n := child.(type) {
	case nil, *ir.If, *ir.Match, *ir.For:
		return true
	case *ir.NodeInst:
		return hides(n) || n.Component != nil && vc.isUserComponent(n.Component)
	}
	return false
}

// hides reports whether n's fit may hide it: a literal setting `hidden`, or
// one the build cannot read.
func hides(n *ir.NodeInst) bool {
	f := codegen.NodeProp(n, "fit")
	lit, ok := f.(*ir.StructLit)
	if !ok {
		return f != nil
	}
	for _, fi := range lit.Fields {
		if zero, known := ir.ZeroKnown(fi.Value); fi.Spread || fi.Name == "hidden" && (!zero || !known) {
			return true
		}
	}
	return false
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
	case *ir.If, *ir.Match:
		return !ir.ComputesValue(n)
	case *ir.For:
		return !computesOnly(n)
	case *ir.Assign, *ir.CallStmt, *ir.Return, *ir.Emit, *ir.Toggle, *ir.CanvasRedrawStmt,
		*ir.Break, *ir.Continue:
		return false
	}
	return true
}

// computesOnly reports whether a view-body loop draws nothing -- the
// accumulator NoListLambdas hoists ahead of the widget reading `xs.map(f)`,
// which runs as statements rather than rendering.
func computesOnly(f *ir.For) bool {
	return len(f.Body) > 0 && !slices.ContainsFunc(f.Body, draws) && !slices.ContainsFunc(f.Else, draws)
}

func draws(s ir.Stmt) bool {
	switch n := s.(type) {
	case *ir.If, *ir.Match:
		return !ir.ComputesValue(n) && slices.ContainsFunc(ir.TransparentBlocks(n), func(b *[]ir.Stmt) bool {
			return slices.ContainsFunc(*b, draws)
		})
	case *ir.For:
		return !computesOnly(n)
	case *ir.NodeInst:
		// A timer draws nothing either, but as a statement a loop of them is a
		// loop over nothing whose variable Go refuses as unused.
		return true
	}
	return rendersPart(s)
}

func (vc *irViewContext) renderStmt(stmt ir.Stmt, resultVar string) {
	switch s := stmt.(type) {
	case *ir.NodeInst:
		if ir.IsTimerPrimitive(s.Component) {
			return // see rendersPart
		}
		if btIntrinsic(s) == "Screen" {
			// The window draws its content as the view, and no box of its own,
			// while it is visible; what it holds lives on while it is not.
			if v := codegen.NodeProp(s, "visible"); v != nil {
				vc.line("if %s {", vc.gc.EvalExpr(v))
				vc.indent++
				vc.renderBranch(s.Children, resultVar)
				vc.indent--
				vc.line("}")
				return
			}
			vc.renderBranch(s.Children, resultVar)
			return
		}
		vc.renderNode(s, resultVar)
		vc.countFocusPos(s)
	case *ir.If, *ir.Match:
		if ir.ComputesValue(s) {
			// NoTernary hoists `var __ltN` + this value-only If or Match
			// (Assign bodies, no NodeInst children) before the widget whose
			// prop reads __ltN. Emit it as imperative Go so the temp is
			// assigned in scope; the structural path would drop the Assign
			// bodies.
			vc.emitIRStmt(s)
			return
		}
		if m, ok := s.(*ir.Match); ok {
			vc.renderMatch(m, resultVar)
			return
		}
		vc.renderIf(s.(*ir.If), resultVar)
	case *ir.For:
		if computesOnly(s) {
			vc.emitIRStmt(s)
			return
		}
		vc.renderFor(s, resultVar)
	case *ir.SlotInst:
		vc.renderSlotInst(s, resultVar)
	case *ir.ErrorBoundary:
		vc.renderBranch(s.Children, resultVar)
	case *ir.LocalVar:
		// A LocalVar in the view body is the `var __ltN` decl NoTernary hoists
		// before the widget consuming it (its FromTernary If assigns it). Emit
		// so the temp is declared in the view scope.
		vc.emitIRStmt(s)
	case *ir.Assign:
		// A write of a local the lowering synthesized -- a step of the counter
		// passIndexedIter puts beside a loop -- which the view reads; any other
		// write has no visual rendering.
		if id, ok := s.Target.(*ir.Ident); ok {
			if v, ok := id.Sym.(*ir.Var); ok && v.Synthesized {
				vc.emitIRStmt(s)
			}
		}
	case *ir.CallStmt, *ir.Return, *ir.Emit, *ir.Toggle, *ir.CanvasRedrawStmt,
		*ir.Break, *ir.Continue:
		// Imperative stmts have no visual rendering — skipped.
	default:
		panic(fmt.Sprintf("bubbletea.renderStmt: unhandled ir.Stmt %T", s))
	}
}

func (vc *irViewContext) renderIf(s *ir.If, resultVar string) {
	// Defer the conditional syntax to the Go language driver.
	vc.line("%s", vc.gc.IfHead(s, vc.gc.EvalExpr(s.Cond)))
	vc.indent++
	vc.renderBranch(s.Body, resultVar)
	vc.indent--
	if len(s.Else) > 0 {
		vc.line("%s", vc.gc.ElseHead())
		vc.indent++
		vc.renderBranch(s.Else, resultVar)
		vc.indent--
	}
	vc.line("%s", vc.gc.BlockEnd())
}

// renderMatch is a switch in the view, each arm rendering what it holds, as
// renderIf is an if.
func (vc *irViewContext) renderMatch(s *ir.Match, resultVar string) {
	vc.line("%s", vc.gc.SwitchHead(&s.MatchArms))
	for i, arm := range s.Arms {
		vc.line("%s", vc.gc.SwitchCase(&s.MatchArms, i))
		vc.indent++
		vc.renderBranch(arm.Body, resultVar)
		vc.indent--
	}
	vc.line("%s", vc.gc.BlockEnd())
}

func (vc *irViewContext) renderBranch(stmts []ir.Stmt, resultVar string) {
	if countParts(stmts) <= 1 {
		for _, child := range stmts {
			vc.renderStmt(child, resultVar)
		}
		return
	}
	list := resultVar + "Parts"
	vc.line("var %s []string", list)
	vc.renderSiblings(stmts, resultVar+"Part", list)
	vc.join(resultVar, list)
}

// join writes the parts in list into resultVar as the Layout they are in
// joins its own: along its axis, its spacing apart, where it puts them
// across it, leaving out a part that draws nothing.
func (vc *irViewContext) join(resultVar, list string) {
	vc.requireImport(tuiImportPath)
	gap, at := vc.joinGap, vc.joinAt
	if gap == "" {
		gap = "0"
	}
	if at == "" {
		at = "lipgloss.Left"
		if vc.horizontal {
			at = "lipgloss.Top"
		}
	}
	vc.line(`%s = tui.Spaced(%t, %s, %s, %s...)`, resultVar, vc.horizontal, at, gap, list)
}

func (vc *irViewContext) renderFor(s *ir.For, resultVar string) {
	vc.declareLoopCounters(s)
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
	// The view body may not reference the loop vars; suppress unused errors
	// for the ones the head declared.
	kept := golang.WithUnreadVarsDropped(s)
	if kept.Key != "" && kept.Key != "_" {
		vc.line("_ = %s", kept.Key)
	}
	if kept.Value != "" && kept.Value != "_" {
		vc.line("_ = %s", kept.Value)
	}

	innerVar := resultVar + "Item"
	vc.renderSiblings(s.Body, innerVar, loopVar)

	vc.indent--
	vc.line("}")
	vc.join(resultVar, loopVar)

	if len(s.Else) > 0 {
		vc.line("if len(%s) == 0 {", iterExpr)
		vc.indent++
		if countParts(s.Else) > 1 {
			elseVar := resultVar + "Else"
			vc.line("var %s []string", elseVar)
			vc.renderSiblings(s.Else, resultVar+"Empty", elseVar)
			vc.join(resultVar, elseVar)
		} else {
			for _, child := range s.Else {
				vc.renderStmt(child, resultVar)
			}
		}
		vc.indent--
		vc.line("}")
	}

	vc.gc = savedGC
}

func countParts(stmts []ir.Stmt) int {
	n := 0
	for _, s := range codegen.WithoutSchedules(stmts) {
		if rendersPart(s) {
			n++
		}
	}
	return n
}

// renderSiblings appends what stmts render to list, each part its own entry,
// so a loop body of several nodes gives the container each of them to join.
func (vc *irViewContext) renderSiblings(stmts []ir.Stmt, partVar, list string) {
	if countParts(stmts) <= 1 {
		vc.line("var %s string", partVar)
		var drawn ir.Stmt
		for _, child := range stmts {
			vc.renderStmt(child, partVar)
			if rendersPart(child) {
				drawn = child
			}
		}
		vc.appendPart(drawn, partVar, list)
		return
	}
	for i, child := range codegen.WithoutSchedules(stmts) {
		vc.renderChild(child, fmt.Sprintf("%s_%d", partVar, i), list)
	}
}

func (vc *irViewContext) renderNode(n *ir.NodeInst, resultVar string) {
	// Canvas2D node — rasterize inline each frame into a terminal string.
	if c := vc.ctx.Canvases.ForNode(n); c != nil {
		vc.renderCanvas(n, c, resultVar)
		return
	}

	// User component — call render method
	if n.Component != nil && vc.isUserComponent(n.Component) {
		vc.renderUserComponent(n, resultVar)
		return
	}

	if btIntrinsic(n) == pictureIntrinsic {
		vc.renderPicture(n, resultVar)
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
		case *ir.If, *ir.Match:
			for _, b := range ir.TransparentBlocks(n) {
				if expr := tooltipFocusExpr(*b, gc); expr != "" {
					return expr
				}
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
	if g := codegen.NodeProp(n, "grow"); g != nil && vc.sizeTo == nil {
		// Not a direct child of a Layout sharing its space, so the share is unread.
		vc.discard(g)
	}
	style := vc.look(n)

	switch bp.Kind {
	case bpFlow:
		vc.renderFlow(n, resultVar, style)

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
		prevVertical, prevHorizontal := vc.vertical, vc.horizontal
		prevGap, prevAt := vc.joinGap, vc.joinAt
		vc.vertical, vc.horizontal = true, false
		vc.joinGap, vc.joinAt = "", ""
		savedW, savedH := vc.availW, vc.availH
		vc.availW, vc.availH = "", ""
		for i, child := range n.Children {
			vc.renderChild(child, fmt.Sprintf("%s_%d", boxVar, i), childrenVar)
		}
		vc.availW, vc.availH = savedW, savedH
		vc.vertical, vc.horizontal = prevVertical, prevHorizontal
		vc.joinGap, vc.joinAt = prevGap, prevAt
		vc.line(`%s = lipgloss.JoinVertical(lipgloss.Left, %s...)`, boxVar, childrenVar)
		if style != plainLook {
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
		vc.renderLayout(n, bp, resultVar, style)

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
			vc.line(`%s = %s`, resultVar, vc.render(n, style, resultVar+`Prefix + " " + fmt.Sprint(`+content+`)`))
		} else {
			vc.line(`%s = %s`, resultVar, vc.render(n, style, "fmt.Sprint("+content+")"))
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
		view := "m." + w.fieldName + w.model.View
		if w.loop != nil {
			view = copyCall(w, vc.gc) + w.model.View
		}
		if style != plainLook || vc.fits(n) {
			view = vc.render(n, style, view)
		}
		vc.line(`%s = %s`, resultVar, view)
	}
}

// plainLook is the look of a primitive whose override handed it none.
const plainLook = "lipgloss.NewStyle()"

// fits reports whether n's override handed it a tui.Fit that may say anything:
// one the build cannot read, or a literal setting a field. tui.Fit is the
// host's struct, which ir.ZeroKnown does not answer for, but its literal's
// fields left out are Go's zero.
func (vc *irViewContext) fits(n *ir.NodeInst) bool {
	lit, ok := codegen.NodeProp(n, "fit").(*ir.StructLit)
	if !ok {
		return codegen.NodeProp(n, "fit") != nil
	}
	for _, f := range lit.Fields {
		if zero, known := ir.ZeroKnown(f.Value); f.Spread || !zero || !known {
			return true
		}
	}
	return false
}

// render is the Go rendering text, a Go string, in look as n's fit says:
// look's own Render where the fit says nothing.
func (vc *irViewContext) render(n *ir.NodeInst, look, text string) string {
	if !vc.fits(n) {
		if f := codegen.NodeProp(n, "fit"); f != nil {
			vc.discard(f)
		}
		return look + ".Render(" + text + ")"
	}
	vc.requireImport(tuiImportPath)
	return fmt.Sprintf("tui.Render(%s, %s, %s)", look, vc.gc.EvalExpr(codegen.NodeProp(n, "fit")), text)
}

// look is the lipgloss style a primitive renders with: what its override built
// from the node's Style, sized to the share of its parent it grows into.
func (vc *irViewContext) look(n *ir.NodeInst) string {
	look := plainLook
	if v := codegen.NodeProp(n, "look"); v != nil {
		look = vc.gc.EvalExpr(v)
	}
	if s := vc.sizeTo; s != nil {
		vc.sizeTo = nil
		look = fmt.Sprintf("tui.Sized(%s, %t, %s)", look, s.horizontal, s.cells)
	}
	return look
}

// growSize is the share of a Layout's free space the primitive rendered next
// grows into: the Go int holding it, and the axis it is along.
type growSize struct {
	horizontal bool
	cells      string
}

// grows reports whether n is a primitive its override handed a share of its
// parent's free space.
func grows(n *ir.NodeInst) bool {
	g := codegen.NodeProp(n, "grow")
	if g == nil || btIntrinsic(n) == "" {
		return false
	}
	if v, ok := codegen.IRLiteralNumber(g); ok {
		return v > 0
	}
	return true
}

func growsWithin(s ir.Stmt) bool {
	found := false
	_ = ir.Walk([]ir.Stmt{s}, func(nd ir.Node) error {
		if n, ok := nd.(*ir.NodeInst); ok && (grows(n) || componentGrows(n.Component, nil)) {
			found = true
			return ir.SkipAll
		}
		return nil
	})
	return found
}

// directlyGrows reports whether s is a child its Layout renders once the
// fixed ones are done. One that counts a loop's focus stops or canvas copies
// as it renders is joined at its own size, since its count would come after
// its later siblings'.
func (vc *irViewContext) directlyGrows(s ir.Stmt) bool {
	n, ok := s.(*ir.NodeInst)
	if !ok || !grows(n) {
		return false
	}
	ordered := false
	_ = ir.Walk([]ir.Stmt{n}, func(nd ir.Node) error {
		m, ok := nd.(*ir.NodeInst)
		if !ok {
			return nil
		}
		if _, ok := nodeLoopFocus(m); ok {
			ordered = true
		} else if c := vc.ctx.Canvases.ForNode(m); c != nil && vc.canvasSeq[c.Name] {
			ordered = true
		}
		if ordered {
			return ir.SkipAll
		}
		return nil
	})
	return !ordered
}

// renderLayout joins a Layout's children along its axis.
//
// A child that grows is rendered in a closure once the fixed ones have said
// how much of the Layout they leave, which needs the Layout's own extent: its
// look's, or what its parent gave it, the terminal's at the top. A child that
// grows under an `if` or a `for` is joined at its own size, being one of the
// parts that statement renders rather than a part of its own.
func (vc *irViewContext) renderLayout(n *ir.NodeInst, bp blueprint, resultVar, style string) {
	horizontal := bp.Join != joinVertical
	savedW, savedH := vc.availW, vc.availH
	inner := ""
	outer := vc.buf
	justify := "0"
	if v := codegen.NodeProp(n, "justify"); v != nil {
		if c, ok := codegen.IRLiteralNumber(v); !ok || c != 0 {
			justify = vc.gc.EvalExpr(v)
		}
	}
	// A box placing its children along it needs its extent, as one whose
	// child grows does: what they leave of it is what it places them in.
	if justify != "0" || slices.ContainsFunc(n.Children, growsWithin) {
		vc.requireImport(tuiImportPath)
		look := resultVar + "Look"
		vc.line("%s := %s", look, style)
		style = look
		inner = fmt.Sprintf("tui.Inner(%s, %s, %s)", look, extent(vc.availW), extent(vc.availH))
		vc.availW, vc.availH = resultVar+"W", resultVar+"H"
		// A child that does not grow has no extent along the axis, as in CSS:
		// what grows inside it takes its own size. renderPart gives a grower one.
		if horizontal {
			vc.availW = ""
		} else {
			vc.availH = ""
		}
		// Declared once the children say which of the two they read.
		vc.buf = &strings.Builder{}
	}
	// A picture is fitted to the box it is shown in, where that box's look
	// gives it a size.
	prevPicture := vc.pictureLook
	vc.pictureLook = ""
	if slices.ContainsFunc(n.Children, isPicture) {
		if style == plainLook {
			vc.pictureLook = plainLook
		} else {
			if inner == "" {
				look := resultVar + "Look"
				vc.line("%s := %s", look, style)
				style = look
			}
			vc.pictureLook = style
		}
	}
	pos := vc.position(n, horizontal)
	spacing := "0"
	if v := codegen.NodeProp(n, "spacing"); v != nil {
		if c, ok := codegen.IRLiteralNumber(v); !ok || c != 0 {
			spacing = vc.gc.EvalExpr(v)
		}
	}
	prevGap, prevAt := vc.joinGap, vc.joinAt
	vc.joinGap, vc.joinAt = spacing, pos
	parts := justify != "0" || slices.ContainsFunc(n.Children, vc.directlyGrows)
	childrenVar := resultVar + "Children"
	if parts {
		vc.line("var %s []tui.Part", childrenVar)
	} else {
		vc.line("var %s []string", childrenVar)
	}
	prevVertical, prevHorizontal := vc.vertical, vc.horizontal
	vc.vertical, vc.horizontal = !horizontal, horizontal
	for i, child := range n.Children {
		childVar := fmt.Sprintf("%s_%d", resultVar, i)
		if parts {
			vc.renderPart(child, childVar, childrenVar, horizontal)
		} else {
			vc.renderChild(child, childVar, childrenVar)
		}
	}
	vc.vertical, vc.horizontal = prevVertical, prevHorizontal
	vc.joinGap, vc.joinAt = prevGap, prevAt
	vc.pictureLook = prevPicture
	// tooltip: reveal the body text (dim) below the trigger while the wrapped
	// focusable descendant is focused. The `tooltip` prop carries the text;
	// the focus expression comes from the focusable descendant's injected
	// __focused prop. If no static focus expression is available (e.g. the
	// trigger sits inside a for-loop, whose key isn't in scope here), the
	// tooltip degrades to never showing rather than emitting invalid Go.
	if tip := codegen.NodeProp(n, "tooltip"); tip != nil {
		if focusExpr := tooltipFocusExpr(n.Children, vc.gc); focusExpr != "" {
			vc.requireImport("fmt")
			text := fmt.Sprintf("lipgloss.NewStyle().Faint(true).Render(fmt.Sprint(%s))", vc.gc.EvalExpr(tip))
			if parts {
				text = "tui.Fixed(" + text + ")"
			}
			vc.line(`if %s {`, focusExpr)
			vc.line(`%s = append(%s, %s)`, childrenVar, childrenVar, text)
			vc.line(`}`)
		}
	}
	main := ""
	if inner != "" {
		main = resultVar + "H"
		if horizontal {
			main = resultVar + "W"
		}
	}
	switch {
	case parts:
		vc.line(`%s = tui.Join(%t, %s, %s, %s, %s, %s)`, resultVar, horizontal, pos, justify, spacing, extent(main), childrenVar)
	case spacing != "0":
		vc.requireImport(tuiImportPath)
		vc.line(`%s = tui.Spaced(%t, %s, %s, %s...)`, resultVar, horizontal, pos, spacing, childrenVar)
	case horizontal:
		vc.line(`%s = lipgloss.JoinHorizontal(%s, %s...)`, resultVar, pos, childrenVar)
	default:
		vc.line(`%s = lipgloss.JoinVertical(%s, %s...)`, resultVar, pos, childrenVar)
	}
	if inner != "" {
		body := vc.buf.String()
		vc.buf = outer
		w, h := resultVar+"W", resultVar+"H"
		if !readsName(body, w) {
			w = "_"
		}
		if !readsName(body, h) {
			h = "_"
		}
		if w != "_" || h != "_" {
			vc.line("%s, %s := %s", w, h, inner)
		}
		vc.buf.WriteString(body)
	}
	vc.availW, vc.availH = savedW, savedH
	// A box wider or taller than its children puts them where it joins them
	// across it: a card's names centered in its width, where they sat at its
	// left edge.
	if !strings.HasSuffix(pos, ".Left") && !strings.HasSuffix(pos, ".Top") {
		if horizontal {
			style += ".AlignVertical(" + pos + ")"
		} else {
			style += ".AlignHorizontal(" + pos + ")"
		}
	}
	if style != plainLook || vc.fits(n) {
		vc.line(`%s = %s`, resultVar, vc.render(n, style, resultVar))
	}
}

// isPicture reports whether s is a picture a URL names.
func isPicture(s ir.Stmt) bool {
	n, ok := s.(*ir.NodeInst)
	return ok && btIntrinsic(n) == pictureIntrinsic
}

// availParam is a component's render method's parameter for the width it is
// placed in, which a growing child inside it is handed a share of.
const availParam = "__availW"

// componentGrows reports whether c's body has a child growing into its
// Layout's free space, which needs the width c is placed in. seen holds the
// components already asked, for a recursive one.
func componentGrows(c *ir.Component, seen map[*ir.Component]bool) bool {
	if c == nil || seen[c] {
		return false
	}
	if seen == nil {
		seen = map[*ir.Component]bool{}
	}
	seen[c] = true
	found := false
	_ = ir.Walk(c.Body.Stmts, func(nd ir.Node) error {
		if n, ok := nd.(*ir.NodeInst); ok && (grows(n) || componentGrows(n.Component, seen)) {
			found = true
			return ir.SkipAll
		}
		return nil
	})
	return found
}

// readsName reports whether the Go in body reads the local name: a selector
// of the same spelling, `m.contentW`, is not it.
func readsName(body, name string) bool {
	for i := strings.Index(body, name); i >= 0; {
		end := i + len(name)
		if (i == 0 || !isIdentByte(body[i-1]) && body[i-1] != '.') && (end == len(body) || !isIdentByte(body[end])) {
			return true
		}
		next := strings.Index(body[end:], name)
		if next < 0 {
			break
		}
		i = end + next
	}
	return false
}

func isIdentByte(c byte) bool {
	return c == '_' || '0' <= c && c <= '9' || 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z'
}

// renderPart is renderChild for a Layout one of whose children grows.
func (vc *irViewContext) renderPart(child ir.Stmt, childVar, partsVar string, horizontal bool) {
	if !vc.directlyGrows(child) {
		if !rendersPart(child) {
			vc.renderStmt(child, "")
			return
		}
		vc.line("var %s string", childVar)
		vc.renderStmt(child, childVar)
		switch child.(type) {
		case *ir.If, *ir.For:
			vc.line("if %s != \"\" {", childVar)
			vc.line("\t%s = append(%s, tui.Fixed(%s))", partsVar, partsVar, childVar)
			vc.line("}")
		default:
			vc.line("%s = append(%s, tui.Fixed(%s))", partsVar, partsVar, childVar)
		}
		return
	}
	n := child.(*ir.NodeInst)
	cells := childVar + "Cells"
	vc.line("%s = append(%s, tui.Grow(%s, func(%s int) string {", partsVar, partsVar, vc.gc.EvalExpr(codegen.NodeProp(n, "grow")), cells)
	vc.indent++
	savedW, savedH := vc.availW, vc.availH
	if horizontal {
		vc.availW = cells
	} else {
		vc.availH = cells
	}
	vc.sizeTo = &growSize{horizontal: horizontal, cells: cells}
	vc.line("var %s string", childVar)
	vc.renderStmt(child, childVar)
	vc.sizeTo = nil
	vc.availW, vc.availH = savedW, savedH
	vc.line("return %s", childVar)
	vc.indent--
	vc.line("}))")
}

// position is where a Layout's children sit across its axis.
func (vc *irViewContext) position(n *ir.NodeInst, horizontal bool) string {
	start, end := "lipgloss.Left", "lipgloss.Right"
	if horizontal {
		start, end = "lipgloss.Top", "lipgloss.Bottom"
	}
	a := codegen.NodeProp(n, "align")
	if a == nil {
		return start
	}
	v, ok := codegen.IRLiteralNumber(a)
	switch {
	case !ok:
		return "lipgloss.Position(" + vc.gc.EvalExpr(a) + ")"
	case v == 0:
		return start
	case v == 0.5:
		return "lipgloss.Center"
	case v == 1:
		return end
	}
	return fmt.Sprintf("lipgloss.Position(%v)", v)
}

// extent is a Go int holding a Layout's extent along one axis, 0 where nothing
// knows it.
func extent(v string) string {
	if v == "" {
		return "0"
	}
	return v
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

	if n.Component != nil {
		args = append(args, vc.populationArgs(n, resultVar)...)
	}
	if componentGrows(n.Component, nil) {
		args = append(args, extent(vc.availW))
	}

	vc.line(`%s = m.%s(%s)`, resultVar, methodName, strings.Join(args, ", "))
}

func (vc *irViewContext) renderRawTerminal(n *ir.NodeInst, resultVar string) {
	// A component the build emptied has no override to translate its Style.
	vc.discardStyle(codegen.NodeStyleFields(n))
	style := vc.look(n)

	// Join layout
	if joinExpr := codegen.NodeProp(n, "join"); joinExpr != nil {
		if s, ok := codegen.IRLiteralString(joinExpr); ok {
			childrenVar := resultVar + "Children"
			vc.line("var %s []string", childrenVar)
			prevVertical, prevHorizontal := vc.vertical, vc.horizontal
			prevGap, prevAt := vc.joinGap, vc.joinAt
			vc.vertical = s == "vertical"
			vc.horizontal = !vc.vertical
			vc.joinGap, vc.joinAt = "", ""
			for i, child := range n.Children {
				vc.renderChild(child, fmt.Sprintf("%s_%d", resultVar, i), childrenVar)
			}
			vc.vertical, vc.horizontal = prevVertical, prevHorizontal
			vc.joinGap, vc.joinAt = prevGap, prevAt
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

func (vc *irViewContext) discardStyle(styles []codegen.StyleField) {
	for _, sf := range styles {
		vc.discard(sf.Value)
	}
}

// discard marks a view-local temp read by a field this target does not draw.
// NoTernary hoists `fontSize = on ? 20px : 10px` into a `var __ltN` ahead of
// the node whether or not anything reads it, and Go refuses one nobody reads.
func (vc *irViewContext) discard(e ir.Expr) {
	seen := map[string]bool{}
	_ = ir.WalkExprs(e, func(x ir.Expr) error {
		id, ok := x.(*ir.Ident)
		if !ok || id.Member != "" || seen[id.Name] {
			return nil
		}
		if _, kind := vc.gc.Ctx.Resolve(id.Name); kind == codegen.NameLocal {
			seen[id.Name] = true
			vc.line("_ = %s", vc.gc.EvalExpr(id))
		}
		return nil
	})
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

// viewStmts is what View draws: the package body, which is the application's
// view and holds the Screen a window is on this platform -- or, for a harness
// that isolated a component, that component's body.
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
