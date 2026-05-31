package bubbletea

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/codegen/lang/golang"
	"git.duckfam.us/jonathan/sngl/ir"
)

// irViewContext tracks state during IR-based View() code generation.
type irViewContext struct {
	gc           *golang.GoIRContext
	ctx          *codegen.CodegenCtx
	scaleFactor  int
	focusIndex   int
	inputCount   int
	buf          *strings.Builder
	indent       int
	vertical     bool
	inComponent  bool
	slotVar      string
	slotChildren []ir.Stmt          // caller's children for stdlib component slot expansion
	propVals     map[string]ir.Expr // prop overrides during stdlib component expansion
}

func (vc *irViewContext) line(format string, args ...any) {
	// Register imports at the emit site: a rendered line that calls fmt.* (the
	// fmt.Sprint value wrapper) needs the "fmt" import. tea/lipgloss are
	// required structurally; "fmt" is conditional, so it's required here only
	// when actually emitted.
	if vc.gc != nil && strings.Contains(format, "fmt.") {
		vc.gc.RequireImport("fmt")
	}
	fmt.Fprintf(vc.buf, "%s"+format+"\n", append([]any{strings.Repeat("\t", vc.indent)}, args...)...)
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
		buf:         &strings.Builder{},
		indent:      1,
	}

	if len(bodyStmts) == 1 {
		vc.line("var content string")
		vc.renderStmt(bodyStmts[0], "content")
		b.WriteString(vc.buf.String())
	} else {
		vc.line("var parts []string")
		for i, child := range bodyStmts {
			childVar := fmt.Sprintf("part%d", i)
			vc.line("var %s string", childVar)
			vc.renderStmt(child, childVar)
			vc.line("parts = append(parts, %s)", childVar)
		}
		vc.line(`content := lipgloss.JoinVertical(lipgloss.Left, parts...)`)
		b.WriteString(vc.buf.String())
	}

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

	b.WriteString("\tv := tea.NewView(content)\n")
	b.WriteString("\tv.AltScreen = true\n")
	b.WriteString("\treturn v\n")
	b.WriteString("}\n\n")
}

func emitIRComponentMethod(b *strings.Builder, cc *codegen.ComponentCtx, ctx *codegen.CodegenCtx, gc *golang.GoIRContext, cfg Config) {
	methodName := "render" + golang.ExportName(cc.Component.Name)

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

	if len(cc.Body) == 1 {
		vc.line("var result string")
		vc.renderStmt(cc.Body[0], "result")
		b.WriteString(vc.buf.String())
		b.WriteString("\treturn result\n")
	} else {
		vc.line("var parts []string")
		for i, child := range cc.Body {
			childVar := fmt.Sprintf("part%d", i)
			vc.line("var %s string", childVar)
			vc.renderStmt(child, childVar)
			vc.line("parts = append(parts, %s)", childVar)
		}
		vc.line(`result := lipgloss.JoinVertical(lipgloss.Left, parts...)`)
		b.WriteString(vc.buf.String())
		b.WriteString("\treturn result\n")
	}

	b.WriteString("}\n\n")
}

// --- IR view rendering ---

func (vc *irViewContext) renderStmt(stmt ir.Stmt, resultVar string) {
	switch s := stmt.(type) {
	case *ir.NodeInst:
		vc.renderNode(s, resultVar)
	case *ir.If:
		vc.renderIf(s, resultVar)
	case *ir.For:
		vc.renderFor(s, resultVar)
	case *ir.PlatformFilter:
		for _, bs := range s.Body {
			vc.renderStmt(bs, resultVar)
		}
	case *ir.SlotInst:
		if len(vc.slotChildren) > 0 {
			for i, child := range vc.slotChildren {
				childVar := fmt.Sprintf("%sSlot%d", resultVar, i)
				vc.line("var %s string", childVar)
				vc.renderStmt(child, childVar)
				vc.line(`%s += %s`, resultVar, childVar)
			}
		} else if vc.slotVar != "" {
			vc.line(`%s = %s`, resultVar, vc.slotVar)
		}
	case *ir.ErrorBoundary:
		for _, child := range s.Children {
			vc.renderStmt(child, resultVar)
		}
	case *ir.Window:
		// Window only appears at top-level; nested Window in view tree is unexpected.
		panic(fmt.Sprintf("bubbletea: unexpected nested Window in view tree: %#v", s))
	case *ir.Assign, *ir.CallStmt, *ir.LocalVar, *ir.Return, *ir.Emit, *ir.Toggle:
		// Imperative stmts have no visual rendering — skipped.
	case *ir.ContextProvider:
		panic(fmt.Sprintf("bubbletea: ContextProvider should be lowered before view emission: %#v", s))
	default:
		panic(fmt.Sprintf("bubbletea.renderStmt: unhandled ir.Stmt %T", s))
	}
}

func (vc *irViewContext) renderIf(s *ir.If, resultVar string) {
	cond := vc.gc.EvalExpr(s.Cond)
	vc.line("if %s {", cond)
	vc.indent++
	for _, child := range s.Body {
		vc.renderStmt(child, resultVar)
	}
	vc.indent--
	if len(s.Else) > 0 {
		vc.line("} else {")
		vc.indent++
		for _, child := range s.Else {
			vc.renderStmt(child, resultVar)
		}
		vc.indent--
	}
	vc.line("}")
}

func (vc *irViewContext) renderFor(s *ir.For, resultVar string) {
	iterExpr := vc.gc.EvalExpr(s.Iter)
	iterVar := s.Key
	indexVar := "_"
	if s.Value != "" {
		indexVar = s.Key
		iterVar = s.Value
	}

	loopGC := vc.gc.WithLocal(iterVar)
	if indexVar != "_" {
		loopGC = loopGC.WithLocal(indexVar)
	}
	savedGC := vc.gc
	vc.gc = loopGC

	loopVar := resultVar + "Items"
	vc.line("var %s []string", loopVar)
	vc.line("for %s, %s := range %s {", indexVar, iterVar, iterExpr)
	vc.indent++
	if indexVar != "_" {
		vc.line("_ = %s", indexVar)
	}
	if iterVar != "_" {
		vc.line("_ = %s", iterVar)
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
	// User component — call render method
	if n.Component != nil && vc.isUserComponent(n.Component) {
		vc.renderUserComponent(n, resultVar)
		return
	}

	// Stdlib component — map to terminal rendering based on name
	if n.Component != nil {
		vc.renderStdlibComponent(n, resultVar)
		return
	}

	// Raw terminal element
	vc.renderRawTerminal(n, resultVar)
}

func (vc *irViewContext) isUserComponent(comp *ir.Component) bool {
	return slices.Contains(vc.ctx.Pkg.Components, comp)
}

// renderStdlibComponent maps stdlib component names to their bubbletea terminal
// rendering. This is equivalent to the platform package override bodies in
// bubbletea.sngl but done directly in Go to avoid IR body expansion complexity.
func (vc *irViewContext) renderStdlibComponent(n *ir.NodeInst, resultVar string) {
	styleFields := codegen.NodeStyleFields(n)
	style := buildIRStyleExpr(styleFields, vc.gc, vc.scaleFactor)

	switch n.Name {
	case "vbox", "stack", "scroll", "card", "radio",
		"drawer", "tooltip", "popover", "table", "tree", "menu":
		// Vertical join layout
		childrenVar := resultVar + "Children"
		vc.line("var %s []string", childrenVar)
		prevVertical := vc.vertical
		vc.vertical = true
		for i, child := range n.Children {
			childVar := fmt.Sprintf("%s_%d", resultVar, i)
			vc.line("var %s string", childVar)
			vc.renderStmt(child, childVar)
			vc.line("%s = append(%s, %s)", childrenVar, childrenVar, childVar)
		}
		vc.vertical = prevVertical
		vc.line(`%s = lipgloss.JoinVertical(lipgloss.Left, %s...)`, resultVar, childrenVar)
		if style != "lipgloss.NewStyle()" {
			vc.line(`%s = %s.Render(%s)`, resultVar, style, resultVar)
		}

	case "hbox", "tabs", "splitview", "menubar", "toolbar":
		// Horizontal join layout
		childrenVar := resultVar + "Children"
		vc.line("var %s []string", childrenVar)
		for i, child := range n.Children {
			childVar := fmt.Sprintf("%s_%d", resultVar, i)
			vc.line("var %s string", childVar)
			vc.renderStmt(child, childVar)
			vc.line("%s = append(%s, %s)", childrenVar, childrenVar, childVar)
		}
		vc.line(`%s = lipgloss.JoinHorizontal(lipgloss.Top, %s...)`, resultVar, childrenVar)
		if style != "lipgloss.NewStyle()" {
			vc.line(`%s = %s.Render(%s)`, resultVar, style, resultVar)
		}

	case "text", "badge", "link", "image", "progress", "spinner", "divider", "avatar":
		// Styled content
		content := `""`
		if v := codegen.NodeProp(n, "value"); v != nil {
			content = vc.gc.EvalExpr(v)
		} else if v := codegen.NodeProp(n, "text"); v != nil {
			content = vc.gc.EvalExpr(v)
		} else if v := codegen.NodeProp(n, "label"); v != nil {
			content = vc.gc.EvalExpr(v)
		} else if v := codegen.NodeProp(n, "initials"); v != nil {
			content = vc.gc.EvalExpr(v)
		}
		vc.line(`%s = %s.Render(fmt.Sprint(%s))`, resultVar, style, content)

	case "button", "checkbox", "toggle", "select", "textarea", "chip":
		// Styled focusable content
		content := `""`
		if v := codegen.NodeProp(n, "text"); v != nil {
			content = vc.gc.EvalExpr(v)
		} else if v := codegen.NodeProp(n, "label"); v != nil {
			content = vc.gc.EvalExpr(v)
		} else if v := codegen.NodeProp(n, "value"); v != nil {
			content = vc.gc.EvalExpr(v)
		}
		focusIdx := vc.focusIndex
		vc.focusIndex++
		vc.line(`%sFocused := m.focus == %d`, resultVar, focusIdx)
		vc.line(`%sPrefix := " "`, resultVar)
		vc.line(`if %sFocused { %sPrefix = ">" }`, resultVar, resultVar)
		vc.line(`%s = %s.Render(%sPrefix + " " + fmt.Sprint(%s))`, resultVar, style, resultVar, content)

	case "input":
		// Text input model widget
		idx := vc.inputCount
		vc.inputCount++
		vc.focusIndex++
		vc.line(`%s = m.input%d.View()`, resultVar, idx)

	case "spacer":
		vc.line(`%s = ""`, resultVar)

	case "modal":
		// Conditional container
		if openExpr := codegen.NodeProp(n, "open"); openExpr != nil {
			cond := vc.gc.EvalExpr(openExpr)
			vc.line("if %s {", cond)
			vc.indent++
			childrenVar := resultVar + "Children"
			vc.line("var %s []string", childrenVar)
			for i, child := range n.Children {
				childVar := fmt.Sprintf("%s_%d", resultVar, i)
				vc.line("var %s string", childVar)
				vc.renderStmt(child, childVar)
				vc.line("%s = append(%s, %s)", childrenVar, childrenVar, childVar)
			}
			vc.line(`%s = lipgloss.JoinVertical(lipgloss.Left, %s...)`, resultVar, childrenVar)
			vc.indent--
			vc.line("}")
		}

	case "datepicker":
		content := `""`
		if v := codegen.NodeProp(n, "value"); v != nil {
			content = vc.gc.EvalExpr(v)
		}
		vc.line(`%s = %s.Render(fmt.Sprint(%s))`, resultVar, style, content)

	default:
		// Unknown stdlib component — render children vertically
		if len(n.Children) > 0 {
			childrenVar := resultVar + "Children"
			vc.line("var %s []string", childrenVar)
			for i, child := range n.Children {
				childVar := fmt.Sprintf("%s_%d", resultVar, i)
				vc.line("var %s string", childVar)
				vc.renderStmt(child, childVar)
				vc.line("%s = append(%s, %s)", childrenVar, childrenVar, childVar)
			}
			vc.line(`%s = lipgloss.JoinVertical(lipgloss.Left, %s...)`, resultVar, childrenVar)
		} else {
			vc.line(`%s = %s.Render("")`, resultVar, style)
		}
	}
}

// expandStdlibComponent inlines a stdlib component's platform override body,
// substituting props from the caller and threading slot children.
func (vc *irViewContext) expandStdlibComponent(n *ir.NodeInst, resultVar string) {
	comp := n.Component

	// Build prop value map from caller
	propVals := make(map[string]ir.Expr)
	for _, a := range n.Props {
		propVals[a.Name] = a.Value
	}

	// Save and set slot children
	savedSlot := vc.slotChildren
	vc.slotChildren = n.Children

	// Walk override body, substituting prop references
	savedPropVals := vc.propVals
	vc.propVals = propVals
	for _, stmt := range comp.Body {
		vc.renderStmt(stmt, resultVar)
	}
	vc.propVals = savedPropVals
	vc.slotChildren = savedSlot
}

func (vc *irViewContext) renderUserComponent(n *ir.NodeInst, resultVar string) {
	methodName := "render" + golang.ExportName(n.Name)

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

// resolveProp returns the expression for a prop, checking propVals overrides first.
func (vc *irViewContext) resolveProp(n *ir.NodeInst, name string) ir.Expr {
	expr := codegen.NodeProp(n, name)
	if expr == nil {
		return nil
	}
	// During stdlib expansion, prop references (ir.Ident) may point to component
	// params. Substitute with caller's actual values.
	if vc.propVals != nil {
		if ident, ok := expr.(*ir.Ident); ok {
			if val, ok := vc.propVals[ident.Name]; ok {
				return val
			}
		}
	}
	return expr
}

func (vc *irViewContext) renderRawTerminal(n *ir.NodeInst, resultVar string) {
	// During stdlib expansion, merge caller's style with override's style
	styleFields := codegen.NodeStyleFields(n)
	if vc.propVals != nil {
		if callerStyle, ok := vc.propVals["style"]; ok {
			if sl, ok2 := callerStyle.(*ir.StructLit); ok2 {
				if styleFields == nil {
					styleFields = make(map[string]ir.Expr)
				}
				for _, f := range sl.Fields {
					styleFields[f.Name] = f.Value
				}
			}
		}
	}
	style := buildIRStyleExpr(styleFields, vc.gc, vc.scaleFactor)

	// Join layout
	if joinExpr := vc.resolveProp(n, "join"); joinExpr != nil {
		if s, ok := codegen.IRLiteralString(joinExpr); ok {
			childrenVar := resultVar + "Children"
			vc.line("var %s []string", childrenVar)
			prevVertical := vc.vertical
			vc.vertical = s == "vertical"
			for i, child := range n.Children {
				childVar := fmt.Sprintf("%s_%d", resultVar, i)
				vc.line("var %s string", childVar)
				vc.renderStmt(child, childVar)
				vc.line("%s = append(%s, %s)", childrenVar, childrenVar, childVar)
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

	// Model widget (textinput)
	if modelView := vc.resolveProp(n, "modelView"); modelView != nil {
		if viewMethod, ok := codegen.IRLiteralString(modelView); ok {
			idx := vc.inputCount
			vc.inputCount++
			vc.focusIndex++
			vc.line(`%s = m.input%d%s`, resultVar, idx, viewMethod)
			return
		}
	}

	// Default: styled content
	content := `""`
	if v := vc.resolveProp(n, "content"); v != nil {
		content = vc.gc.EvalExpr(v)
	}

	if vc.resolveProp(n, "focusable") != nil {
		focusIdx := vc.focusIndex
		vc.focusIndex++
		vc.line(`%sFocused := m.focus == %d`, resultVar, focusIdx)
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
func buildIRStyleExpr(styles map[string]ir.Expr, gc *golang.GoIRContext, scaleFactor int) string {
	var chain []string
	chain = append(chain, "lipgloss.NewStyle()")

	if styles == nil {
		return chain[0]
	}

	for _, prop := range slices.Sorted(maps.Keys(styles)) {
		if call := irStyleCall(prop, styles[prop], gc, scaleFactor); call != "" {
			chain = append(chain, call)
		}
	}

	return strings.Join(chain, ".\n")
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
	val := gc.EvalExpr(expr)

	switch prop {
	case "padding":
		return fmt.Sprintf("Padding(%s)", scaleVal(val, scaleFactor))
	case "paddingTop":
		return fmt.Sprintf("PaddingTop(%s)", scaleVal(val, scaleFactor))
	case "paddingRight":
		return fmt.Sprintf("PaddingRight(%s)", scaleVal(val, scaleFactor))
	case "paddingBottom":
		return fmt.Sprintf("PaddingBottom(%s)", scaleVal(val, scaleFactor))
	case "paddingLeft":
		return fmt.Sprintf("PaddingLeft(%s)", scaleVal(val, scaleFactor))
	case "margin":
		return fmt.Sprintf("Margin(%s)", scaleVal(val, scaleFactor))
	case "marginTop":
		return fmt.Sprintf("MarginTop(%s)", scaleVal(val, scaleFactor))
	case "marginRight":
		return fmt.Sprintf("MarginRight(%s)", scaleVal(val, scaleFactor))
	case "marginBottom":
		return fmt.Sprintf("MarginBottom(%s)", scaleVal(val, scaleFactor))
	case "marginLeft":
		return fmt.Sprintf("MarginLeft(%s)", scaleVal(val, scaleFactor))
	case "width":
		return fmt.Sprintf("Width(%s)", scaleVal(val, scaleFactor))
	case "height":
		return fmt.Sprintf("Height(%s)", scaleVal(val, scaleFactor))
	case "maxWidth":
		return fmt.Sprintf("MaxWidth(%s)", scaleVal(val, scaleFactor))
	case "maxHeight":
		return fmt.Sprintf("MaxHeight(%s)", scaleVal(val, scaleFactor))
	case "color":
		return fmt.Sprintf("Foreground(lipgloss.Color(%s))", val)
	case "background":
		return fmt.Sprintf("Background(lipgloss.Color(%s))", val)
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
	case "borderColor":
		return fmt.Sprintf("BorderForeground(lipgloss.Color(%s))", val)
	case "opacity":
		return "Faint(true)"
	}
	return ""
}
