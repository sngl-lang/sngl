package bubbletea

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
	inputCount  int            // next input-specific index (for m.inputN field names)
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
	case "scroll", "tooltip":
		// Pass-through: render child directly
		if len(vn.Children) > 0 {
			vc.renderNode(vn.Children[0], resultVar)
		} else {
			vc.line(`%s = ""`, resultVar)
		}
	case "radio":
		vc.renderRadio(vn, resultVar)
	case "toggle":
		vc.renderToggle(vn, resultVar)
	case "select":
		vc.renderSelectComp(vn, resultVar)
	case "textarea":
		vc.renderTextarea(vn, resultVar)
	case "progress":
		vc.renderProgress(vn, resultVar)
	case "spinner":
		vc.renderSpinner(vn, resultVar)
	case "badge":
		vc.renderBadge(vn, resultVar)
	case "tabs":
		vc.renderTabs(vn, resultVar)
	case "link":
		vc.renderLink(vn, resultVar)
	case "divider":
		vc.renderDivider(vn, resultVar)
	case "modal", "drawer":
		vc.renderConditionalContainer(vn, resultVar)
	case "popover":
		vc.renderConditionalContainer(vn, resultVar)
	case "accordion":
		vc.renderAccordion(vn, resultVar)
	case "splitview":
		vc.renderSplitview(vn, resultVar)
	case "table":
		vc.renderTable(vn, resultVar)
	case "tree":
		vc.renderTree(vn, resultVar)
	case "menu":
		vc.renderMenu(vn, resultVar)
	case "menubar":
		vc.renderMenubar(vn, resultVar)
	case "toolbar":
		vc.renderBox(vn, resultVar, false) // toolbar = horizontal layout
	case "datepicker":
		vc.renderDatepicker(vn, resultVar)
	case "chip":
		vc.renderChip(vn, resultVar)
	case "avatar":
		vc.renderAvatar(vn, resultVar)
	case "card":
		vc.renderCard(vn, resultVar)
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
		childVar := fmt.Sprintf("%s_%d", resultVar, i)
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
	inputIdx := vc.inputCount
	vc.inputCount++
	vc.focusIndex++
	vc.line(`%s = m.input%d.View()`, resultVar, inputIdx)
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

// --- New component renderers ---

func (vc *viewContext) renderRadio(vn *ast.VisualNode, resultVar string) {
	style := buildStyleExpr(vn.StyleAttrs, vn.StyleBlock, vc.ec, vc.scaleFactor)
	value := `""`
	if v, ok := vn.Props["value"]; ok {
		value = exprToGoValue(v, vc.ec)
	}
	options := `[]any{}`
	if v, ok := vn.Props["options"]; ok {
		options = exprToGoValue(v, vc.ec)
	}
	if vc.inComponent {
		vc.line(`{`)
		vc.indent++
		vc.line(`var %sParts []string`, resultVar)
		vc.line(`for _, opt := range %s {`, options)
		vc.line(`	s := fmt.Sprint(opt)`)
		vc.line(`	if s == fmt.Sprint(%s) { %sParts = append(%sParts, "(•) "+s) } else { %sParts = append(%sParts, "( ) "+s) }`, value, resultVar, resultVar, resultVar, resultVar)
		vc.line(`}`)
		vc.line(`%s = %s.Render(strings.Join(%sParts, "  "))`, resultVar, style, resultVar)
		vc.indent--
		vc.line(`}`)
		return
	}
	focusIdx := vc.focusIndex
	vc.focusIndex++
	vc.line(`{`)
	vc.indent++
	vc.line(`var %sParts []string`, resultVar)
	vc.line(`for _, opt := range %s {`, options)
	vc.line(`	s := fmt.Sprint(opt)`)
	vc.line(`	if s == fmt.Sprint(%s) { %sParts = append(%sParts, "(•) "+s) } else { %sParts = append(%sParts, "( ) "+s) }`, value, resultVar, resultVar, resultVar, resultVar)
	vc.line(`}`)
	vc.line(`%sPrefix := " "`, resultVar)
	vc.line(`if m.focus == %d { %sPrefix = ">" }`, focusIdx, resultVar)
	vc.line(`%s = %s.Render(%sPrefix + " " + strings.Join(%sParts, "  "))`, resultVar, style, resultVar, resultVar)
	vc.indent--
	vc.line(`}`)
}

func (vc *viewContext) renderToggle(vn *ast.VisualNode, resultVar string) {
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
		vc.line(`%s = %s.Render(ternary(%s, "[ON] ", "[OFF]") + " " + %s)`, resultVar, style, checked, label)
		return
	}
	focusIdx := vc.focusIndex
	vc.focusIndex++
	vc.line(`%sPrefix := " "`, resultVar)
	vc.line(`if m.focus == %d { %sPrefix = ">" }`, focusIdx, resultVar)
	vc.line(`%s = %s.Render(%sPrefix + " " + ternary(%s, "[ON] ", "[OFF]") + " " + %s)`, resultVar, style, resultVar, checked, label)
}

func (vc *viewContext) renderSelectComp(vn *ast.VisualNode, resultVar string) {
	style := buildStyleExpr(vn.StyleAttrs, vn.StyleBlock, vc.ec, vc.scaleFactor)
	value := `""`
	if v, ok := vn.Props["value"]; ok {
		value = exprToGoValue(v, vc.ec)
	}
	placeholder := `""`
	if v, ok := vn.Props["placeholder"]; ok {
		placeholder = exprToGoValue(v, vc.ec)
	}
	if vc.inComponent {
		vc.line(`%s = %s.Render("[▼ " + ternary(fmt.Sprint(%s) != "", fmt.Sprint(%s), %s) + "]")`, resultVar, style, value, value, placeholder)
		return
	}
	focusIdx := vc.focusIndex
	vc.focusIndex++
	vc.line(`%sPrefix := " "`, resultVar)
	vc.line(`if m.focus == %d { %sPrefix = ">" }`, focusIdx, resultVar)
	vc.line(`%s = %s.Render(%sPrefix + " [▼ " + ternary(fmt.Sprint(%s) != "", fmt.Sprint(%s), %s) + "]")`, resultVar, style, resultVar, value, value, placeholder)
}

func (vc *viewContext) renderTextarea(vn *ast.VisualNode, resultVar string) {
	// Treat like input for now — use View() from a textinput model
	style := buildStyleExpr(vn.StyleAttrs, vn.StyleBlock, vc.ec, vc.scaleFactor)
	value := `""`
	if v, ok := vn.Props["value"]; ok {
		value = exprToGoValue(v, vc.ec)
	}
	placeholder := `""`
	if v, ok := vn.Props["placeholder"]; ok {
		placeholder = exprToGoValue(v, vc.ec)
	}
	if vc.inComponent {
		vc.line(`%s = %s.Render(ternary(fmt.Sprint(%s) != "", fmt.Sprint(%s), %s))`, resultVar, style, value, value, placeholder)
		return
	}
	focusIdx := vc.focusIndex
	vc.focusIndex++
	vc.line(`%sPrefix := " "`, resultVar)
	vc.line(`if m.focus == %d { %sPrefix = ">" }`, focusIdx, resultVar)
	vc.line(`%s = %s.Render(%sPrefix + " " + ternary(fmt.Sprint(%s) != "", fmt.Sprint(%s), %s))`, resultVar, style, resultVar, value, value, placeholder)
}

func (vc *viewContext) renderProgress(vn *ast.VisualNode, resultVar string) {
	style := buildStyleExpr(vn.StyleAttrs, vn.StyleBlock, vc.ec, vc.scaleFactor)
	value := `0.0`
	if v, ok := vn.Props["value"]; ok {
		value = exprToGoValue(v, vc.ec)
	}
	maxVal := `1.0`
	if v, ok := vn.Props["max"]; ok {
		maxVal = exprToGoValue(v, vc.ec)
	}
	vc.line(`{`)
	vc.indent++
	vc.line(`%sWidth := 20`, resultVar)
	vc.line(`%sRatio := float64(%s) / float64(%s)`, resultVar, value, maxVal)
	vc.line(`if %sRatio > 1 { %sRatio = 1 }`, resultVar, resultVar)
	vc.line(`if %sRatio < 0 { %sRatio = 0 }`, resultVar, resultVar)
	vc.line(`%sFilled := int(float64(%sWidth) * %sRatio)`, resultVar, resultVar, resultVar)
	vc.line(`%s = %s.Render("[" + strings.Repeat("█", %sFilled) + strings.Repeat("░", %sWidth-%sFilled) + "]")`, resultVar, style, resultVar, resultVar, resultVar)
	vc.indent--
	vc.line(`}`)
}

func (vc *viewContext) renderSpinner(vn *ast.VisualNode, resultVar string) {
	style := buildStyleExpr(vn.StyleAttrs, vn.StyleBlock, vc.ec, vc.scaleFactor)
	label := `""`
	if v, ok := vn.Props["label"]; ok {
		label = exprToGoValue(v, vc.ec)
	}
	vc.line(`%s = %s.Render("⠋ " + %s)`, resultVar, style, label)
}

func (vc *viewContext) renderBadge(vn *ast.VisualNode, resultVar string) {
	style := buildStyleExpr(vn.StyleAttrs, vn.StyleBlock, vc.ec, vc.scaleFactor)
	value := `""`
	if v, ok := vn.Props["value"]; ok {
		value = exprToGoValue(v, vc.ec)
	}
	vc.line(`%s = %s.Render("[" + %s + "]")`, resultVar, style, value)
}

func (vc *viewContext) renderTabs(vn *ast.VisualNode, resultVar string) {
	style := buildStyleExpr(vn.StyleAttrs, vn.StyleBlock, vc.ec, vc.scaleFactor)
	items := `[]any{}`
	if v, ok := vn.Props["items"]; ok {
		items = exprToGoValue(v, vc.ec)
	}
	selected := `0`
	if v, ok := vn.Props["selected"]; ok {
		selected = exprToGoValue(v, vc.ec)
	}
	vc.line(`{`)
	vc.indent++
	vc.line(`var %sTabs []string`, resultVar)
	vc.line(`for i, item := range %s {`, items)
	vc.line(`	s := fmt.Sprint(item)`)
	vc.line(`	if i == %s { %sTabs = append(%sTabs, "["+s+"]") } else { %sTabs = append(%sTabs, " "+s+" ") }`, selected, resultVar, resultVar, resultVar, resultVar)
	vc.line(`}`)
	vc.line(`%s = %s.Render(strings.Join(%sTabs, " "))`, resultVar, style, resultVar)
	vc.indent--
	vc.line(`}`)
}

func (vc *viewContext) renderLink(vn *ast.VisualNode, resultVar string) {
	style := buildStyleExpr(vn.StyleAttrs, vn.StyleBlock, vc.ec, vc.scaleFactor)
	text := `""`
	if v, ok := vn.Props["text"]; ok {
		text = exprToGoValue(v, vc.ec)
	}
	vc.line(`%s = %s.Underline(true).Render(%s)`, resultVar, style, text)
}

func (vc *viewContext) renderDivider(vn *ast.VisualNode, resultVar string) {
	style := buildStyleExpr(vn.StyleAttrs, vn.StyleBlock, vc.ec, vc.scaleFactor)
	label := `""`
	if v, ok := vn.Props["label"]; ok {
		label = exprToGoValue(v, vc.ec)
	}
	vc.line(`if %s != "" {`, label)
	vc.line(`	%s = %s.Render("── " + %s + " ──")`, resultVar, style, label)
	vc.line(`} else {`)
	vc.line(`	%s = %s.Render("────────────────")`, resultVar, style)
	vc.line(`}`)
}

func (vc *viewContext) renderConditionalContainer(vn *ast.VisualNode, resultVar string) {
	// For modal, drawer, popover: render children when open, empty when closed
	style := buildStyleExpr(vn.StyleAttrs, vn.StyleBlock, vc.ec, vc.scaleFactor)
	open := "false"
	if v, ok := vn.Props["open"]; ok {
		open = exprToGoValue(v, vc.ec)
	}
	if v, ok := vn.Props["visible"]; ok {
		open = exprToGoValue(v, vc.ec)
	}
	vc.line(`if %s {`, open)
	vc.indent++
	if len(vn.Children) > 0 {
		childrenVar := resultVar + "Children"
		vc.line("var %s []string", childrenVar)
		prevVertical := vc.vertical
		vc.vertical = true
		for i, child := range vn.Children {
			childVar := fmt.Sprintf("%s_%d", resultVar, i)
			vc.line("var %s string", childVar)
			vc.renderNode(child, childVar)
			vc.line("%s = append(%s, %s)", childrenVar, childrenVar, childVar)
		}
		vc.vertical = prevVertical
		// Modal gets a border
		if vn.Component == "modal" {
			title := `""`
			if v, ok := vn.Props["title"]; ok {
				title = exprToGoValue(v, vc.ec)
			}
			vc.line(`%s = %s.Border(lipgloss.RoundedBorder()).Render(%s + "\n" + lipgloss.JoinVertical(lipgloss.Left, %s...))`, resultVar, style, title, childrenVar)
		} else {
			vc.line(`%s = %s.Render(lipgloss.JoinVertical(lipgloss.Left, %s...))`, resultVar, style, childrenVar)
		}
	}
	vc.indent--
	vc.line(`}`)
}

func (vc *viewContext) renderAccordion(vn *ast.VisualNode, resultVar string) {
	style := buildStyleExpr(vn.StyleAttrs, vn.StyleBlock, vc.ec, vc.scaleFactor)
	items := `[]any{}`
	if v, ok := vn.Props["items"]; ok {
		items = exprToGoValue(v, vc.ec)
	}
	expanded := `[]any{}`
	if v, ok := vn.Props["expanded"]; ok {
		expanded = exprToGoValue(v, vc.ec)
	}
	vc.line(`{`)
	vc.indent++
	vc.line(`var %sParts []string`, resultVar)
	vc.line(`%sExpanded := %s`, resultVar, expanded)
	vc.line(`for i, item := range %s {`, items)
	vc.line(`	isOpen := false`)
	vc.line(`	for _, e := range %sExpanded { if fmt.Sprint(e) == fmt.Sprint(i) { isOpen = true } }`, resultVar)
	vc.line(`	if isOpen { %sParts = append(%sParts, "▼ "+fmt.Sprint(item)) } else { %sParts = append(%sParts, "▶ "+fmt.Sprint(item)) }`, resultVar, resultVar, resultVar, resultVar)
	vc.line(`}`)
	vc.line(`%s = %s.Render(strings.Join(%sParts, "\n"))`, resultVar, style, resultVar)
	vc.indent--
	vc.line(`}`)
}

func (vc *viewContext) renderSplitview(vn *ast.VisualNode, resultVar string) {
	style := buildStyleExpr(vn.StyleAttrs, vn.StyleBlock, vc.ec, vc.scaleFactor)
	if len(vn.Children) < 2 {
		if len(vn.Children) == 1 {
			vc.renderNode(vn.Children[0], resultVar)
		} else {
			vc.line(`%s = ""`, resultVar)
		}
		return
	}
	left := resultVar + "Left"
	right := resultVar + "Right"
	vc.line("var %s, %s string", left, right)
	vc.renderNode(vn.Children[0], left)
	vc.renderNode(vn.Children[1], right)
	// Check direction prop
	direction := `"horizontal"`
	if v, ok := vn.Props["direction"]; ok {
		direction = exprToGoValue(v, vc.ec)
	}
	vc.line(`if %s == "vertical" {`, direction)
	vc.line(`	%s = %s.Render(lipgloss.JoinVertical(lipgloss.Left, %s, %s))`, resultVar, style, left, right)
	vc.line(`} else {`)
	vc.line(`	%s = %s.Render(lipgloss.JoinHorizontal(lipgloss.Top, %s, " │ ", %s))`, resultVar, style, left, right)
	vc.line(`}`)
}

func (vc *viewContext) renderTable(vn *ast.VisualNode, resultVar string) {
	style := buildStyleExpr(vn.StyleAttrs, vn.StyleBlock, vc.ec, vc.scaleFactor)
	columns := `[]any{}`
	if v, ok := vn.Props["columns"]; ok {
		columns = exprToGoValue(v, vc.ec)
	}
	rows := `[]any{}`
	if v, ok := vn.Props["rows"]; ok {
		rows = exprToGoValue(v, vc.ec)
	}
	vc.line(`{`)
	vc.indent++
	vc.line(`var %sLines []string`, resultVar)
	// Header
	vc.line(`var %sHeader []string`, resultVar)
	vc.line(`for _, col := range %s { %sHeader = append(%sHeader, fmt.Sprint(col)) }`, columns, resultVar, resultVar)
	vc.line(`%sLines = append(%sLines, strings.Join(%sHeader, " | "))`, resultVar, resultVar, resultVar)
	vc.line(`%sLines = append(%sLines, strings.Repeat("─", len(strings.Join(%sHeader, " | "))))`, resultVar, resultVar, resultVar)
	// Rows
	vc.line(`for _, row := range %s {`, rows)
	vc.line(`	if cells, ok := row.([]any); ok {`)
	vc.line(`		var %sCells []string`, resultVar)
	vc.line(`		for _, c := range cells { %sCells = append(%sCells, fmt.Sprint(c)) }`, resultVar, resultVar)
	vc.line(`		%sLines = append(%sLines, strings.Join(%sCells, " | "))`, resultVar, resultVar, resultVar)
	vc.line(`	}`)
	vc.line(`}`)
	vc.line(`%s = %s.Render(strings.Join(%sLines, "\n"))`, resultVar, style, resultVar)
	vc.indent--
	vc.line(`}`)
}

func (vc *viewContext) renderTree(vn *ast.VisualNode, resultVar string) {
	style := buildStyleExpr(vn.StyleAttrs, vn.StyleBlock, vc.ec, vc.scaleFactor)
	items := `[]any{}`
	if v, ok := vn.Props["items"]; ok {
		items = exprToGoValue(v, vc.ec)
	}
	vc.line(`{`)
	vc.indent++
	vc.line(`var %sLines []string`, resultVar)
	vc.line(`for _, item := range %s { %sLines = append(%sLines, "▶ "+fmt.Sprint(item)) }`, items, resultVar, resultVar)
	vc.line(`%s = %s.Render(strings.Join(%sLines, "\n"))`, resultVar, style, resultVar)
	vc.indent--
	vc.line(`}`)
}

func (vc *viewContext) renderMenu(vn *ast.VisualNode, resultVar string) {
	style := buildStyleExpr(vn.StyleAttrs, vn.StyleBlock, vc.ec, vc.scaleFactor)
	items := `[]any{}`
	if v, ok := vn.Props["items"]; ok {
		items = exprToGoValue(v, vc.ec)
	}
	open := "false"
	if v, ok := vn.Props["open"]; ok {
		open = exprToGoValue(v, vc.ec)
	}
	vc.line(`if %s {`, open)
	vc.indent++
	vc.line(`var %sLines []string`, resultVar)
	vc.line(`for _, item := range %s { %sLines = append(%sLines, "  "+fmt.Sprint(item)) }`, items, resultVar, resultVar)
	vc.line(`%s = %s.Border(lipgloss.NormalBorder()).Render(strings.Join(%sLines, "\n"))`, resultVar, style, resultVar)
	vc.indent--
	vc.line(`}`)
}

func (vc *viewContext) renderMenubar(vn *ast.VisualNode, resultVar string) {
	style := buildStyleExpr(vn.StyleAttrs, vn.StyleBlock, vc.ec, vc.scaleFactor)
	items := `[]any{}`
	if v, ok := vn.Props["items"]; ok {
		items = exprToGoValue(v, vc.ec)
	}
	vc.line(`{`)
	vc.indent++
	vc.line(`var %sTabs []string`, resultVar)
	vc.line(`for _, item := range %s { %sTabs = append(%sTabs, "["+fmt.Sprint(item)+"]") }`, items, resultVar, resultVar)
	vc.line(`%s = %s.Render(strings.Join(%sTabs, " "))`, resultVar, style, resultVar)
	vc.indent--
	vc.line(`}`)
}

func (vc *viewContext) renderDatepicker(vn *ast.VisualNode, resultVar string) {
	style := buildStyleExpr(vn.StyleAttrs, vn.StyleBlock, vc.ec, vc.scaleFactor)
	value := `""`
	if v, ok := vn.Props["value"]; ok {
		value = exprToGoValue(v, vc.ec)
	}
	placeholder := `"YYYY-MM-DD"`
	if v, ok := vn.Props["placeholder"]; ok {
		placeholder = exprToGoValue(v, vc.ec)
	}
	if vc.inComponent {
		vc.line(`%s = %s.Render(ternary(fmt.Sprint(%s) != "", fmt.Sprint(%s), %s))`, resultVar, style, value, value, placeholder)
		return
	}
	focusIdx := vc.focusIndex
	vc.focusIndex++
	vc.line(`%sPrefix := " "`, resultVar)
	vc.line(`if m.focus == %d { %sPrefix = ">" }`, focusIdx, resultVar)
	vc.line(`%s = %s.Render(%sPrefix + " " + ternary(fmt.Sprint(%s) != "", fmt.Sprint(%s), %s))`, resultVar, style, resultVar, value, value, placeholder)
}

func (vc *viewContext) renderChip(vn *ast.VisualNode, resultVar string) {
	style := buildStyleExpr(vn.StyleAttrs, vn.StyleBlock, vc.ec, vc.scaleFactor)
	label := `""`
	if v, ok := vn.Props["label"]; ok {
		label = exprToGoValue(v, vc.ec)
	}
	removable := "false"
	if v, ok := vn.Props["removable"]; ok {
		removable = exprToGoValue(v, vc.ec)
	}
	vc.line(`if %s {`, removable)
	vc.line(`	%s = %s.Render("[" + %s + " ×]")`, resultVar, style, label)
	vc.line(`} else {`)
	vc.line(`	%s = %s.Render("[" + %s + "]")`, resultVar, style, label)
	vc.line(`}`)
}

func (vc *viewContext) renderAvatar(vn *ast.VisualNode, resultVar string) {
	style := buildStyleExpr(vn.StyleAttrs, vn.StyleBlock, vc.ec, vc.scaleFactor)
	initials := `""`
	if v, ok := vn.Props["initials"]; ok {
		initials = exprToGoValue(v, vc.ec)
	} else if v, ok := vn.Props["alt"]; ok {
		initials = exprToGoValue(v, vc.ec)
	}
	vc.line(`%s = %s.Render("[" + %s + "]")`, resultVar, style, initials)
}

func (vc *viewContext) renderCard(vn *ast.VisualNode, resultVar string) {
	style := buildStyleExpr(vn.StyleAttrs, vn.StyleBlock, vc.ec, vc.scaleFactor)
	if len(vn.Children) > 0 {
		childrenVar := resultVar + "Children"
		vc.line("var %s []string", childrenVar)
		prevVertical := vc.vertical
		vc.vertical = true
		for i, child := range vn.Children {
			childVar := fmt.Sprintf("%s_%d", resultVar, i)
			vc.line("var %s string", childVar)
			vc.renderNode(child, childVar)
			vc.line("%s = append(%s, %s)", childrenVar, childrenVar, childVar)
		}
		vc.vertical = prevVertical
		vc.line(`%s = %s.Border(lipgloss.RoundedBorder()).Render(lipgloss.JoinVertical(lipgloss.Left, %s...))`, resultVar, style, childrenVar)
	} else {
		vc.line(`%s = %s.Border(lipgloss.RoundedBorder()).Render("")`, resultVar, style)
	}
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
