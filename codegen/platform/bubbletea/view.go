package bubbletea

import (
	"fmt"
	"maps"
	"strings"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/codegen"
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
	components     []*ast.ComponentDecl         // user-defined components for param lookup
	inComponent    bool                        // true when rendering inside a component method
	vertical       bool                        // true when inside a vertical container (vbox)
	slotVar        string                      // variable holding pre-rendered slot content (for abstract components)
	forIndexVar    string                      // current for-loop index variable (for cursor-aware rendering)
	doc            *ast.Document               // for FindComponent
	slotChildren   []*ast.VisualNode           // caller's children for inline component expansion
	slotStack      [][]*ast.VisualNode         // stack of outer slot children for nested expansions
	callerEvents   map[string]ast.EventHandler // caller's event handlers (for event propagation)
	componentDepth int                         // recursion guard
}

func (vc *viewContext) line(format string, args ...any) {
	fmt.Fprintf(vc.buf, "%s"+format+"\n", append([]any{strings.Repeat("\t", vc.indent)}, args...)...)
}

// renderNode generates Go code that renders a VisualNode and assigns the result
// to the variable named resultVar.
func (vc *viewContext) renderNode(vn *ast.VisualNode, resultVar string) {
	// Handle if
	hasIf := vn.If != nil
	if hasIf {
		cond := exprToGoCond(*vn.If, vc.ec)
		vc.line("if %s {", cond)
		vc.indent++
	}

	// Handle for
	hasFor := vn.For != nil
	if hasFor {
		iterVar := vn.For.Variable
		iterExpr := exprToGoValue(vn.For.Iterable, vc.ec)
		loopVar := resultVar + "Items"
		indexVar := "_"
		if vn.For.IndexVar != "" {
			indexVar = vn.For.IndexVar
			vc.ec.LocalVars[indexVar] = true
			defer func() { delete(vc.ec.LocalVars, indexVar) }()
		}
		vc.line("var %s []string", loopVar)
		vc.line("for %s, %s := range %s {", indexVar, iterVar, iterExpr)
		vc.indent++
		if indexVar != "_" {
			vc.line("_ = %s", indexVar)
		}
		// Push local var
		vc.ec.LocalVars[iterVar] = true
		defer func() { delete(vc.ec.LocalVars, iterVar) }()

		// Track for-loop index var for cursor-aware rendering
		prevForIndexVar := vc.forIndexVar
		if indexVar != "_" {
			vc.forIndexVar = indexVar
		}

		innerVar := resultVar + "Item"
		vc.line("var %s string", innerVar)
		vc.renderNodeInner(vn, innerVar)
		vc.forIndexVar = prevForIndexVar
		vc.line("%s = append(%s, %s)", loopVar, loopVar, innerVar)

		vc.indent--
		vc.line("}")
		sep := `""`
		if vc.vertical {
			sep = `"\n"`
		}
		vc.line(`%s = strings.Join(%s, %s)`, resultVar, loopVar, sep)
		if len(vn.For.Else) > 0 {
			vc.line("if len(%s) == 0 {", iterExpr)
			vc.indent++
			for _, elseNode := range vn.For.Else {
				vc.renderNodeInner(elseNode, resultVar)
			}
			vc.indent--
			vc.line("}")
		}
	} else {
		vc.renderNodeInner(vn, resultVar)
	}

	if hasIf {
		vc.indent--
		vc.line("}")
	}
}

func (vc *viewContext) renderNodeInner(vn *ast.VisualNode, resultVar string) {
	if vn.Component == "slot" {
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
				vc.renderNode(child, childVar)
				vc.line(`%s += %s`, resultVar, childVar)
			}
			// Restore (the expandComponent restore will handle the full reset)
		} else if vc.slotVar != "" {
			vc.line(`%s = %s`, resultVar, vc.slotVar)
		}
		return
	}

	// Look up component definition (user-defined or abstract/override)
	comp := vc.findComponent(vn.Component)
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
		return vc.doc.FindComponent(name)
	}
	for _, c := range vc.components {
		if c.Name == name {
			return c
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
	for _, p := range comp.Params {
		vc.ec.LocalVars[p.Name] = true
		if expr, ok := vn.Props[p.Name]; ok {
			overrides[p.Name] = exprToGoValue(expr, vc.ec)
		} else if expr, ok := vn.Bindings[p.Name]; ok {
			overrides[p.Name] = exprToGoValue(expr, vc.ec)
		} else if p.Default.Literal != nil {
			overrides[p.Name] = literalToGo(p.Default)
		} else if p.Resolved != nil && p.Resolved.Type != "" {
			overrides[p.Name] = zeroValueGo(p.Resolved.Type)
		} else {
			overrides[p.Name] = `""`
		}
	}
	vc.ec.PropOverrides = overrides

	// Push current slot children onto the stack so nested slot nodes
	// within the new children can resolve to the outer level.
	vc.slotStack = append(vc.slotStack, vc.slotChildren)
	vc.slotChildren = vn.Children
	vc.callerEvents = vn.Events

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
	style := buildStyleExpr(vn.StyleFields(), vc.ec, vc.scaleFactor)

	// Check for join layout
	if join, ok := vn.Props["join"]; ok {
		if s, ok := join.Literal.(string); ok {
			childrenVar := resultVar + "Children"
			vc.line("var %s []string", childrenVar)
			// Expand slot children directly so each becomes a separate join entry
			children := vn.Children
			if len(children) == 1 && children[0].Component == "slot" && len(vc.slotChildren) > 0 {
				children = vc.slotChildren
			}
			prevVertical := vc.vertical
			vc.vertical = s == "vertical"
			for i, child := range children {
				childVar := fmt.Sprintf("%s_%d", resultVar, i)
				vc.line("var %s string", childVar)
				vc.renderNode(child, childVar)
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
	if modelView, ok := vn.Props["modelView"]; ok {
		if viewMethod, ok := modelView.Literal.(string); ok {
			idx := vc.inputCount
			vc.inputCount++
			vc.focusIndex++
			vc.line(`%s = m.input%d%s`, resultVar, idx, viewMethod)
			return
		}
	}

	// Default: styled content render
	content := `""`
	if v, ok := vn.Props["content"]; ok {
		content = exprToGoValue(v, vc.ec)
	}

	if _, hasFocusable := vn.Props["focusable"]; hasFocusable {
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
	methodName := "render" + exportName(vn.Component)

	var comp *ast.Component
	for _, c := range vc.components {
		if c.Name == vn.Component {
			comp = c
			break
		}
	}

	var args []string
	if comp != nil {
		for _, p := range comp.Params {
			if expr, ok := vn.Props[p.Name]; ok {
				args = append(args, exprToGoValue(expr, vc.ec))
			} else {
				args = append(args, literalToGo(p.Default))
			}
		}
	} else {
		for _, expr := range vn.Props {
			args = append(args, exprToGoValue(expr, vc.ec))
		}
	}

	if comp != nil && comp.ChildrenType != "" && len(vn.Children) > 0 {
		slotVar := resultVar + "Slot"
		vc.renderChildren(vn.Children, slotVar)
		args = append(args, slotVar)
	}

	vc.line(`%s = m.%s(%s)`, resultVar, methodName, strings.Join(args, ", "))
}

func (vc *viewContext) renderChildren(children []*ast.VisualNode, resultVar string) {
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
	if m := vn.StyleFields(); m != nil {
		if gapExpr, ok := m["gap"]; ok {
			if gapExpr.Literal != nil {
				switch v := gapExpr.Literal.(type) {
				case int:
					return max(1, v/8)
				case float64:
					return max(1, int(v)/8)
				}
			}
		}
	}
	return 0
}
