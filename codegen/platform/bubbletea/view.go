package bubbletea

import (
	"fmt"
	"maps"
	"strings"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/ir"
)

// viewContext tracks state during View() code generation.
type viewContext struct {
	ec             *exprContext
	scaleFactor    int
	inputIndex     map[string]int // input node key → index in focusable list
	focusIndex     int            // next focusable index
	inputCount     int            // next input-specific index (for m.inputN field names)
	forCursors     []forLoopCursor
	buf            *strings.Builder
	indent         int
	components     []*ir.Component               // user-defined components for param lookup
	inComponent    bool                         // true when rendering inside a component method
	vertical       bool                         // true when inside a vertical container (vbox)
	slotVar        string                       // variable holding pre-rendered slot content (for abstract components)
	forIndexVar    string                       // current for-loop index variable (for cursor-aware rendering)
	doc            *ast.Document                // for FindComponent
	slotChildren   []ast.Stmt                   // caller's children for inline component expansion
	slotStack      [][]ast.Stmt                 // stack of outer slot children for nested expansions
	callerEvents   map[string]*ast.EventHandler // caller's event handlers (for event propagation)
	componentDepth int                          // recursion guard
}

func (vc *viewContext) line(format string, args ...any) {
	fmt.Fprintf(vc.buf, "%s"+format+"\n", append([]any{strings.Repeat("\t", vc.indent)}, args...)...)
}

// renderStmt dispatches a top-level statement (VisualNode, IfStmt, ForStmt).
func (vc *viewContext) renderStmt(stmt ast.Stmt, resultVar string) {
	switch s := stmt.(type) {
	case *ast.VisualNode:
		vc.renderNode(s, resultVar)
	case *ast.IfStmt:
		vc.renderIfStmt(s, resultVar)
	case *ast.ForStmt:
		vc.renderForStmt(s, resultVar)
	case *ast.PlatformStmt:
		for _, bs := range s.Body.Stmts {
			vc.renderStmt(bs, resultVar)
		}
	}
}

// renderIfStmt generates Go code for an IfStmt.
func (vc *viewContext) renderIfStmt(s *ast.IfStmt, resultVar string) {
	cond := exprToGoCond(s.Cond, vc.ec)
	vc.line("if %s {", cond)
	vc.indent++
	for _, child := range s.Body.Stmts {
		vc.renderStmt(child, resultVar)
	}
	vc.indent--
	if len(s.Else.Stmts) > 0 {
		vc.line("} else {")
		vc.indent++
		for _, child := range s.Else.Stmts {
			vc.renderStmt(child, resultVar)
		}
		vc.indent--
	}
	vc.line("}")
}

// renderForStmt generates Go code for a ForStmt.
func (vc *viewContext) renderForStmt(s *ast.ForStmt, resultVar string) {
	iterExpr := exprToGoValue(s.Iter, vc.ec)
	iterVar := s.Key
	indexVar := "_"
	if s.Value != "" {
		// for index, item = list: Key is index, Value is item
		indexVar = s.Key
		iterVar = s.Value
		vc.ec.LocalVars[indexVar] = true
		defer func() { delete(vc.ec.LocalVars, indexVar) }()
	}

	loopVar := resultVar + "Items"
	vc.line("var %s []string", loopVar)
	vc.line("for %s, %s := range %s {", indexVar, iterVar, iterExpr)
	vc.indent++
	if indexVar != "_" {
		vc.line("_ = %s", indexVar)
	}
	vc.ec.LocalVars[iterVar] = true
	defer func() { delete(vc.ec.LocalVars, iterVar) }()

	prevForIndexVar := vc.forIndexVar
	if indexVar != "_" {
		vc.forIndexVar = indexVar
	}

	innerVar := resultVar + "Item"
	vc.line("var %s string", innerVar)
	for _, child := range s.Body.Stmts {
		vc.renderStmt(child, innerVar)
	}
	vc.forIndexVar = prevForIndexVar
	vc.line("%s = append(%s, %s)", loopVar, loopVar, innerVar)

	vc.indent--
	vc.line("}")
	sep := `""`
	if vc.vertical {
		sep = `"\n"`
	}
	vc.line(`%s = strings.Join(%s, %s)`, resultVar, loopVar, sep)
	if len(s.Else.Stmts) > 0 {
		vc.line("if len(%s) == 0 {", iterExpr)
		vc.indent++
		for _, child := range s.Else.Stmts {
			vc.renderStmt(child, resultVar)
		}
		vc.indent--
		vc.line("}")
	}
}

// renderNode generates Go code that renders a VisualNode and assigns the result
// to the variable named resultVar.
func (vc *viewContext) renderNode(vn *ast.VisualNode, resultVar string) {
	vc.renderNodeInner(vn, resultVar)
}

func (vc *viewContext) renderNodeInner(vn *ast.VisualNode, resultVar string) {
	name := vnName(vn)
	if name == "slot" {
		if len(vc.slotChildren) > 0 {
			// Pop one level: while rendering slot children, any nested
			// slot nodes should resolve to the outer level's children.
			expanded := vc.slotChildren
			if len(vc.slotStack) > 0 {
				vc.slotChildren = vc.slotStack[len(vc.slotStack)-1]
				vc.slotStack = vc.slotStack[:len(vc.slotStack)-1]
			} else {
				vc.slotChildren = nil
			}
			for i, child := range expanded {
				childVar := fmt.Sprintf("%sSlot%d", resultVar, i)
				vc.line("var %s string", childVar)
				vc.renderStmt(child, childVar)
				vc.line(`%s += %s`, resultVar, childVar)
			}
		} else if vc.slotVar != "" {
			vc.line(`%s = %s`, resultVar, vc.slotVar)
		}
		return
	}

	// Look up component definition (user-defined or abstract/override)
	comp := vc.findComponent(name)
	if comp != nil {
		body := codegen.ResolveComponentBody(comp, "bubbletea")
		if len(body) > 0 {
			vc.expandComponent(comp, vn, body, resultVar)
		} else {
			vc.renderUserComponent(vn, resultVar)
		}
		return
	}

	// Raw terminal component — interpret metadata props
	vc.renderRawTerminal(vn, resultVar)
}

func (vc *viewContext) findComponent(name string) *ast.ComponentDecl {
	if vc.doc != nil {
		return codegen.FindComponent(vc.doc, name)
	}
	for _, c := range vc.components {
		if c.Name == name {
			return c.AST
		}
	}
	return nil
}

// expandComponent inlines a component override body at the call site.
func (vc *viewContext) expandComponent(comp *ast.ComponentDecl, vn *ast.VisualNode, body []*ast.VisualNode, resultVar string) {
	vc.componentDepth++
	if vc.componentDepth > 10 {
		vc.componentDepth--
		return
	}
	defer func() { vc.componentDepth-- }()

	savedLocals := maps.Clone(vc.ec.LocalVars)
	savedSlot := vc.slotChildren
	savedStack := vc.slotStack
	savedEvents := vc.callerEvents

	// Bind component params via propOverrides
	savedOverrides := vc.ec.PropOverrides
	overrides := make(map[string]string)
	if savedOverrides != nil {
		maps.Copy(overrides, savedOverrides)
	}
	props := vnProps(vn)
	for _, p := range compParams(comp) {
		vc.ec.LocalVars[p.Name] = true
		if expr, ok := props[p.Name]; ok {
			overrides[p.Name] = exprToGoValue(expr, vc.ec)
		} else if p.Default != nil {
			overrides[p.Name] = literalToGo(p.Default)
		} else {
			overrides[p.Name] = `""`
		}
	}
	vc.ec.PropOverrides = overrides

	// Push current slot children onto the stack so nested slot nodes
	// within the new children can resolve to the outer level.
	vc.slotStack = append(vc.slotStack, vc.slotChildren)
	vc.slotChildren = vnChildren(vn)
	vc.callerEvents = vnEvents(vn)

	for _, child := range body {
		vc.renderNode(child, resultVar)
	}

	vc.ec.LocalVars = savedLocals
	vc.ec.PropOverrides = savedOverrides
	vc.slotChildren = savedSlot
	vc.slotStack = savedStack
	vc.callerEvents = savedEvents
}

// renderRawTerminal renders an implicit terminal component by interpreting
// its metadata props. Recognized component patterns:
//   - join="vertical|horizontal" — layout join
//   - content=expr — lipgloss styled text
//   - focusable=true — focus indicator
//   - modelView=".View()" — stateful model widget
func (vc *viewContext) renderRawTerminal(vn *ast.VisualNode, resultVar string) {
	style := buildStyleExpr(vnStyleFields(vn), vc.ec, vc.scaleFactor)
	props := vnProps(vn)

	// Check for join layout
	if join, ok := props["join"]; ok {
		if s, ok := codegen.ExprLiteralString(join); ok {
			childrenVar := resultVar + "Children"
			vc.line("var %s []string", childrenVar)
			// Expand slot children directly so each becomes a separate join entry
			children := vnChildren(vn)
			if len(children) == 1 {
				if childVN, ok := children[0].(*ast.VisualNode); ok && vnName(childVN) == "slot" && len(vc.slotChildren) > 0 {
					children = vc.slotChildren
				}
			}
			prevVertical := vc.vertical
			vc.vertical = s == "vertical"
			for i, child := range children {
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

	// Check for model widget (textinput, etc.)
	if modelView, ok := props["modelView"]; ok {
		if viewMethod, ok := codegen.ExprLiteralString(modelView); ok {
			idx := vc.inputCount
			vc.inputCount++
			vc.focusIndex++
			vc.line(`%s = m.input%d%s`, resultVar, idx, viewMethod)
			return
		}
	}

	// Default: styled content render
	content := `""`
	if v, ok := props["content"]; ok {
		content = exprToGoValue(v, vc.ec)
	}

	if _, hasFocusable := props["focusable"]; hasFocusable {
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

func (vc *viewContext) renderUserComponent(vn *ast.VisualNode, resultVar string) {
	methodName := "render" + exportName(vnName(vn))

	var comp *ast.ComponentDecl
	for _, c := range vc.components {
		if c.Name == vnName(vn) {
			comp = c.AST
			break
		}
	}

	props := vnProps(vn)
	var args []string
	if comp != nil {
		for _, p := range compParams(comp) {
			if expr, ok := props[p.Name]; ok {
				args = append(args, exprToGoValue(expr, vc.ec))
			} else {
				args = append(args, literalToGo(p.Default))
			}
		}
	} else {
		for _, expr := range props {
			args = append(args, exprToGoValue(expr, vc.ec))
		}
	}

	if comp != nil && compHasChildren(comp) {
		children := vnChildNodes(vn)
		if len(children) > 0 {
			slotVar := resultVar + "Slot"
			vc.renderChildrenVN(children, slotVar)
			args = append(args, slotVar)
		}
	}

	vc.line(`%s = m.%s(%s)`, resultVar, methodName, strings.Join(args, ", "))
}

func (vc *viewContext) renderChildrenVN(children []*ast.VisualNode, resultVar string) {
	if len(children) == 1 {
		vc.line("var %s string", resultVar)
		vc.renderNode(children[0], resultVar)
	} else {
		childrenParts := resultVar + "Parts"
		vc.line("var %s []string", childrenParts)
		for i, child := range children {
			childVar := fmt.Sprintf("%sPart%d", resultVar, i)
			vc.line("var %s string", childVar)
			vc.renderNode(child, childVar)
			vc.line("%s = append(%s, %s)", childrenParts, childrenParts, childVar)
		}
		vc.line(`%s := lipgloss.JoinVertical(lipgloss.Left, %s...)`, resultVar, childrenParts)
	}
}

func (vc *viewContext) getGap(vn *ast.VisualNode) int {
	if m := vnStyleFields(vn); m != nil {
		if gapExpr, ok := m["gap"]; ok {
			if v, ok := codegen.ExprLiteralInt(gapExpr); ok {
				return max(1, v/8)
			}
			if f, ok := codegen.ExprLiteralFloat(gapExpr); ok {
				return max(1, int(f)/8)
			}
		}
	}
	return 0
}
