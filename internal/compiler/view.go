package compiler

import (
	"fmt"
	"strings"

	"git.duckfam.us/jonathan/sngl/ast"
)

// viewContext tracks state during View() code generation.
type viewContext struct {
	ec          *exprContext
	scaleFactor int
	inputIndex  map[string]int // input node key → index in focusable list
	focusIndex  int            // next focusable index
	forCursors  []forLoopCursor
	buf         *strings.Builder
	indent      int
	components  []*ast.Component // user-defined components for param lookup
	inComponent bool             // true when rendering inside a component method
	vertical    bool             // true when inside a vertical container (vbox)
	forIndexVar string           // current for-loop index variable (for cursor-aware rendering)
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
			vc.ec.localVars[indexVar] = true
			defer func() { delete(vc.ec.localVars, indexVar) }()
		}
		vc.line("var %s []string", loopVar)
		vc.line("for %s, %s := range %s {", indexVar, iterVar, iterExpr)
		vc.indent++
		if indexVar != "_" {
			vc.line("_ = %s", indexVar)
		}
		// Push local var
		vc.ec.localVars[iterVar] = true
		defer func() { delete(vc.ec.localVars, iterVar) }()

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
	} else {
		vc.renderNodeInner(vn, resultVar)
	}

	if hasIf {
		vc.indent--
		vc.line("}")
	}
}

func (vc *viewContext) renderNodeInner(vn *ast.VisualNode, resultVar string) {
	switch vn.Component {
	case "vbox":
		vc.renderBox(vn, resultVar, true)
	case "hbox":
		vc.renderBox(vn, resultVar, false)
	case "text":
		vc.renderText(vn, resultVar)
	case "button":
		vc.renderButton(vn, resultVar)
	case "checkbox":
		vc.renderCheckbox(vn, resultVar)
	case "input":
		vc.renderInput(vn, resultVar)
	case "spacer":
		vc.renderSpacer(vn, resultVar)
	case "image":
		vc.renderImage(vn, resultVar)
	case "stack":
		// Stub: render children vertically
		vc.renderBox(vn, resultVar, true)
	case "scroll":
		// Stub: render child directly
		if len(vn.Children) > 0 {
			vc.renderNode(vn.Children[0], resultVar)
		} else {
			vc.line(`%s := ""`, resultVar)
		}
	default:
		// User-defined component
		vc.renderUserComponent(vn, resultVar)
	}
}

func (vc *viewContext) renderBox(vn *ast.VisualNode, resultVar string, vertical bool) {
	style := buildStyleExpr(vn.StyleAttrs, vn.StyleBlock, vc.ec, vc.scaleFactor)
	hasStyle := style != "lipgloss.NewStyle()"

	childrenVar := resultVar + "Children"
	vc.line("var %s []string", childrenVar)

	// Determine gap
	gap := vc.getGap(vn)

	prevVertical := vc.vertical
	vc.vertical = vertical
	for i, child := range vn.Children {
		childVar := fmt.Sprintf("%s%d", resultVar, i)
		// Declare childVar before the if/for so it's in scope after
		vc.line("var %s string", childVar)
		vc.renderNode(child, childVar)
		vc.line("%s = append(%s, %s)", childrenVar, childrenVar, childVar)
	}
	vc.vertical = prevVertical

	if vertical {
		if gap > 0 {
			spacerVar := resultVar + "Gap"
			vc.line(`%s := strings.Repeat("\n", %d)`, spacerVar, gap)
			joinedVar := resultVar + "Joined"
			vc.line(`%s := strings.Join(%s, %s)`, joinedVar, childrenVar, spacerVar)
			if hasStyle {
				vc.line(`%s = %s.Render(%s)`, resultVar, style, joinedVar)
			} else {
				vc.line(`%s = %s`, resultVar, joinedVar)
			}
		} else {
			if hasStyle {
				vc.line(`%s = %s.Render(lipgloss.JoinVertical(lipgloss.Left, %s...))`, resultVar, style, childrenVar)
			} else {
				vc.line(`%s = lipgloss.JoinVertical(lipgloss.Left, %s...)`, resultVar, childrenVar)
			}
		}
	} else {
		if gap > 0 {
			spacerVar := resultVar + "Gap"
			vc.line(`%s := strings.Repeat(" ", %d)`, spacerVar, gap)
			joinedVar := resultVar + "Joined"
			vc.line(`%s := strings.Join(%s, %s)`, joinedVar, childrenVar, spacerVar)
			if hasStyle {
				vc.line(`%s = %s.Render(%s)`, resultVar, style, joinedVar)
			} else {
				vc.line(`%s = %s`, resultVar, joinedVar)
			}
		} else {
			if hasStyle {
				vc.line(`%s = %s.Render(lipgloss.JoinHorizontal(lipgloss.Top, %s...))`, resultVar, style, childrenVar)
			} else {
				vc.line(`%s = lipgloss.JoinHorizontal(lipgloss.Top, %s...)`, resultVar, childrenVar)
			}
		}
	}
}

func (vc *viewContext) renderText(vn *ast.VisualNode, resultVar string) {
	style := buildStyleExpr(vn.StyleAttrs, vn.StyleBlock, vc.ec, vc.scaleFactor)
	val := `""`
	if v, ok := vn.Props["value"]; ok {
		val = exprToGoValue(v, vc.ec)
	}
	vc.line(`%s = %s.Render(%s)`, resultVar, style, val)
}

func (vc *viewContext) renderButton(vn *ast.VisualNode, resultVar string) {
	style := buildStyleExpr(vn.StyleAttrs, vn.StyleBlock, vc.ec, vc.scaleFactor)
	text := `""`
	if v, ok := vn.Props["text"]; ok {
		text = exprToGoValue(v, vc.ec)
	}

	if vc.inComponent {
		// Component buttons are display-only, no focus tracking
		vc.line(`%s = %s.Render(%s)`, resultVar, style, text)
		return
	}

	// Add focus indicator
	focusIdx := vc.focusIndex
	vc.focusIndex++
	vc.line(`%sFocused := m.focus == %d`, resultVar, focusIdx)
	vc.line(`%sPrefix := " "`, resultVar)
	vc.line(`if %sFocused { %sPrefix = ">" }`, resultVar, resultVar)
	vc.line(`%s = %s.Render(%sPrefix + " " + %s)`, resultVar, style, resultVar, text)
}

func (vc *viewContext) renderCheckbox(vn *ast.VisualNode, resultVar string) {
	style := buildStyleExpr(vn.StyleAttrs, vn.StyleBlock, vc.ec, vc.scaleFactor)
	checked := "false"
	if v, ok := vn.Props["checked"]; ok {
		checked = exprToGoValue(v, vc.ec)
	}
	label := `""`
	if v, ok := vn.Props["label"]; ok {
		label = exprToGoValue(v, vc.ec)
	}

	if vc.inComponent {
		vc.line(`%s = %s.Render(ternary(%s, "[x] ", "[ ] ") + %s)`, resultVar, style, checked, label)
		return
	}

	focusIdx := vc.focusIndex
	vc.focusIndex++

	// For-looped checkboxes use cursor for per-item focus
	if vc.forIndexVar != "" {
		var fc *forLoopCursor
		for i := range vc.forCursors {
			if vc.forCursors[i].focusIdx == focusIdx {
				fc = &vc.forCursors[i]
				break
			}
		}
		if fc != nil {
			vc.line(`%sFocused := m.focus == %d && m.%s == %s`, resultVar, focusIdx, fc.cursorField, vc.forIndexVar)
			vc.line(`%sPrefix := " "`, resultVar)
			vc.line(`if %sFocused { %sPrefix = ">" }`, resultVar, resultVar)
			vc.line(`%s = %s.Render(%sPrefix + " " + ternary(%s, "[x] ", "[ ] ") + %s)`, resultVar, style, resultVar, checked, label)
			return
		}
	}

	vc.line(`%sFocused := m.focus == %d`, resultVar, focusIdx)
	vc.line(`%sPrefix := " "`, resultVar)
	vc.line(`if %sFocused { %sPrefix = ">" }`, resultVar, resultVar)
	vc.line(`%s = %s.Render(%sPrefix + " " + ternary(%s, "[x] ", "[ ] ") + %s)`, resultVar, style, resultVar, checked, label)
}

func (vc *viewContext) renderInput(vn *ast.VisualNode, resultVar string) {
	idx := vc.focusIndex
	vc.focusIndex++
	vc.line(`%s = m.input%d.View()`, resultVar, idx)
}

func (vc *viewContext) renderSpacer(vn *ast.VisualNode, resultVar string) {
	style := buildStyleExpr(vn.StyleAttrs, vn.StyleBlock, vc.ec, vc.scaleFactor)
	vc.line(`%s = %s.Render("")`, resultVar, style)
}

func (vc *viewContext) renderImage(vn *ast.VisualNode, resultVar string) {
	style := buildStyleExpr(vn.StyleAttrs, vn.StyleBlock, vc.ec, vc.scaleFactor)
	alt := `"image"`
	if v, ok := vn.Props["alt"]; ok {
		alt = exprToGoValue(v, vc.ec)
	}
	vc.line(`%s = %s.Render("[image: " + %s + "]")`, resultVar, style, alt)
}

func (vc *viewContext) renderUserComponent(vn *ast.VisualNode, resultVar string) {
	methodName := "render" + exportName(vn.Component)

	// Find the component definition to get param order and defaults
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
	vc.line(`%s = m.%s(%s)`, resultVar, methodName, strings.Join(args, ", "))
}

func (vc *viewContext) getGap(vn *ast.VisualNode) int {
	// Check style attrs and block for gap
	for _, m := range []map[string]ast.Expr{vn.StyleBlock, vn.StyleAttrs} {
		if m == nil {
			continue
		}
		if gapExpr, ok := m["gap"]; ok {
			if gapExpr.Literal != nil {
				switch v := gapExpr.Literal.(type) {
				case int:
					return max(1, v/8) // approximate: pixel gap → lines/spaces
				case float64:
					return max(1, int(v)/8)
				}
			}
		}
	}
	return 0
}

// exprToGoCond converts an ast.Expr to a Go boolean expression string.
func exprToGoCond(expr ast.Expr, ec *exprContext) string {
	if expr.Literal != nil {
		if v, ok := expr.Literal.(bool); ok {
			if v {
				return "true"
			}
			return "false"
		}
	}
	if expr.SNGL != nil && ec != nil {
		return ec.translateExpr(expr.SNGL)
	}
	return "true"
}
