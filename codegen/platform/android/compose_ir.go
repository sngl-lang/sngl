package android

import (
	"fmt"
	"strings"

	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/codegen/lang/kotlin"
	"git.duckfam.us/jonathan/sngl/ir"
)

// irComposeContext tracks state during IR-based Compose code generation.
type irComposeContext struct {
	kc      *kotlin.KtIRContext
	ctx     *codegen.CodegenCtx
	buf     *strings.Builder
	indent  int
	hasSlot bool
}

func (cc *irComposeContext) line(format string, args ...any) {
	fmt.Fprintf(cc.buf, "%s"+format+"\n", append([]any{strings.Repeat("    ", cc.indent)}, args...)...)
}

func (cc *irComposeContext) renderStmt(stmt ir.Stmt) {
	switch s := stmt.(type) {
	case *ir.NodeInst:
		cc.renderNode(s)
	case *ir.If:
		cc.renderIf(s)
	case *ir.For:
		cc.renderFor(s)
	case *ir.PlatformFilter:
		for _, bs := range s.Body {
			cc.renderStmt(bs)
		}
	case *ir.SlotInst:
		cc.line("slotContent()")
	}
}

func (cc *irComposeContext) renderIf(s *ir.If) {
	cond := cc.kc.EvalExpr(s.Cond)
	cc.line("if (%s) {", cond)
	cc.indent++
	for _, child := range s.Body {
		cc.renderStmt(child)
	}
	cc.indent--
	if len(s.Else) > 0 {
		cc.line("} else {")
		cc.indent++
		for _, child := range s.Else {
			cc.renderStmt(child)
		}
		cc.indent--
	}
	cc.line("}")
}

func (cc *irComposeContext) renderFor(s *ir.For) {
	iterExpr := cc.kc.EvalExpr(s.Iter)
	iterVar := s.Key
	if s.Value != "" {
		cc.line("%s.forEachIndexed { %s, %s ->", iterExpr, s.Key, s.Value)
		cc.indent++
		loopKC := cc.kc.WithLocal(s.Key).WithLocal(s.Value)
		savedKC := cc.kc
		cc.kc = loopKC
		for _, child := range s.Body {
			cc.renderStmt(child)
		}
		cc.kc = savedKC
	} else {
		cc.line("for (%s in %s) {", iterVar, iterExpr)
		cc.indent++
		loopKC := cc.kc.WithLocal(iterVar)
		savedKC := cc.kc
		cc.kc = loopKC
		for _, child := range s.Body {
			cc.renderStmt(child)
		}
		cc.kc = savedKC
	}
	cc.indent--
	cc.line("}")
}

func (cc *irComposeContext) renderNode(n *ir.NodeInst) {
	if n.Component != nil && cc.isUserComponent(n.Component) {
		cc.renderUserComponent(n)
		return
	}
	if n.Component != nil {
		cc.renderStdlibComposable(n)
		return
	}
	cc.line("Text(\"[unknown: %s]\")", n.Name)
}

func (cc *irComposeContext) isUserComponent(comp *ir.Component) bool {
	for _, c := range cc.ctx.Pkg.Components {
		if c == comp {
			return true
		}
	}
	return false
}

func (cc *irComposeContext) renderStdlibComposable(n *ir.NodeInst) {
	style := cc.buildModifier(n)

	switch n.Name {
	case "vbox", "stack":
		cc.line("Column(%s) {", style)
		cc.indent++
		for _, child := range n.Children {
			cc.renderStmt(child)
		}
		cc.indent--
		cc.line("}")

	case "hbox":
		cc.line("Row(%s) {", style)
		cc.indent++
		for _, child := range n.Children {
			cc.renderStmt(child)
		}
		cc.indent--
		cc.line("}")

	case "scroll":
		cc.line("Column(%s.verticalScroll(rememberScrollState())) {", style)
		cc.indent++
		for _, child := range n.Children {
			cc.renderStmt(child)
		}
		cc.indent--
		cc.line("}")

	case "text":
		content := cc.resolveContent(n)
		cc.line("Text(text = %s, %s)", content, cc.textStyle(n))

	case "button":
		text := cc.resolveTextProp(n)
		clickHandler := codegen.NodeHandler(n, "click")
		if clickHandler != nil && clickHandler.Func != nil {
			cc.line("Button(onClick = {")
			cc.indent++
			for _, stmt := range clickHandler.Func.Block {
				for _, line := range cc.kc.EvalStmt(stmt) {
					cc.line("%s", line)
				}
			}
			cc.indent--
			cc.line("}, %s) {", style)
			cc.indent++
			cc.line("Text(%s)", text)
			cc.indent--
			cc.line("}")
		} else {
			cc.line("Button(onClick = {}, %s) { Text(%s) }", style, text)
		}

	case "input":
		bindTarget := ""
		if h := codegen.NodeHandler(n, "input"); h != nil && h.Func != nil {
			if len(h.Func.Block) > 0 {
				if assign, ok := h.Func.Block[0].(*ir.Assign); ok {
					if ident, ok := assign.Target.(*ir.Ident); ok {
						bindTarget = ident.Name
					}
				}
			}
		}
		placeholder := ""
		if s, ok := codegen.IRLiteralString(codegen.NodeProp(n, "placeholder")); ok {
			placeholder = s
		}
		if bindTarget != "" {
			mod := cc.buildModifierRaw(n)
			cc.line("OutlinedTextField(")
			cc.indent++
			cc.line("value = %s,", bindTarget)
			cc.line("onValueChange = { %s = it },", bindTarget)
			if placeholder != "" {
				cc.line("label = { Text(%q) },", placeholder)
			}
			cc.line("modifier = %s", mod)
			cc.indent--
			cc.line(")")
		} else {
			cc.line("OutlinedTextField(value = \"\", onValueChange = {}, %s)", style)
		}

	case "textarea":
		cc.line("OutlinedTextField(value = \"\", onValueChange = {}, %s, minLines = 3)", style)

	case "checkbox":
		label := cc.resolveTextProp(n)
		changeHandler := codegen.NodeHandler(n, "change")
		if changeHandler != nil && changeHandler.Func != nil {
			cc.line("Row(verticalAlignment = Alignment.CenterVertically) {")
			cc.indent++
			// Find the checked state variable
			checkedVar := "false"
			if len(changeHandler.Func.Block) > 0 {
				if toggle, ok := changeHandler.Func.Block[0].(*ir.Toggle); ok {
					checkedVar = cc.kc.EvalExpr(toggle.Target)
				}
			}
			cc.line("Checkbox(checked = %s, onCheckedChange = {", checkedVar)
			cc.indent++
			for _, stmt := range changeHandler.Func.Block {
				for _, line := range cc.kc.EvalStmt(stmt) {
					cc.line("%s", line)
				}
			}
			cc.indent--
			cc.line("})")
			cc.line("Text(%s)", label)
			cc.indent--
			cc.line("}")
		} else {
			cc.line("Checkbox(checked = false, onCheckedChange = {})")
		}

	case "toggle":
		label := cc.resolveTextProp(n)
		cc.line("Row(verticalAlignment = Alignment.CenterVertically) {")
		cc.indent++
		cc.line("Text(%s)", label)
		cc.line("Switch(checked = false, onCheckedChange = {})")
		cc.indent--
		cc.line("}")

	case "select":
		cc.line("// TODO: Select composable")

	case "radio":
		cc.line("// TODO: RadioGroup composable")

	case "progress":
		if v := codegen.NodeProp(n, "value"); v != nil {
			val := cc.kc.EvalExpr(v)
			cc.line("LinearProgressIndicator(progress = { %s.toFloat() }, %s)", val, style)
		} else {
			cc.line("LinearProgressIndicator(%s)", style)
		}

	case "spinner":
		cc.line("CircularProgressIndicator(%s)", style)

	case "divider":
		cc.line("HorizontalDivider(%s)", style)

	case "spacer":
		cc.line("Spacer(%s)", style)

	case "badge":
		content := cc.resolveContent(n)
		cc.line("Badge(%s) { Text(%s) }", style, content)

	case "link":
		text := cc.resolveTextProp(n)
		cc.line("Text(%s, color = MaterialTheme.colorScheme.primary, %s)", text, cc.textStyle(n))

	case "image":
		cc.line("// TODO: Image composable")

	case "card":
		cc.line("Card(%s) {", style)
		cc.indent++
		for _, child := range n.Children {
			cc.renderStmt(child)
		}
		cc.indent--
		cc.line("}")

	case "modal":
		openExpr := codegen.NodeProp(n, "open")
		if openExpr != nil {
			cond := cc.kc.EvalExpr(openExpr)
			cc.line("if (%s) {", cond)
			cc.indent++
			cc.line("Dialog(onDismissRequest = {}) {")
			cc.indent++
			cc.line("Surface(shape = MaterialTheme.shapes.medium) {")
			cc.indent++
			cc.line("Column(modifier = Modifier.padding(16.dp)) {")
			cc.indent++
			for _, child := range n.Children {
				cc.renderStmt(child)
			}
			cc.indent--
			cc.line("}")
			cc.indent--
			cc.line("}")
			cc.indent--
			cc.line("}")
			cc.indent--
			cc.line("}")
		}

	case "tabs":
		cc.line("TabRow(selectedTabIndex = 0) {")
		cc.indent++
		for _, child := range n.Children {
			cc.renderStmt(child)
		}
		cc.indent--
		cc.line("}")

	default:
		if len(n.Children) > 0 {
			cc.line("Column(%s) {", style)
			cc.indent++
			for _, child := range n.Children {
				cc.renderStmt(child)
			}
			cc.indent--
			cc.line("}")
		} else {
			content := cc.resolveContent(n)
			cc.line("Text(%s)", content)
		}
	}
}

func (cc *irComposeContext) renderUserComponent(n *ir.NodeInst) {
	var args []string
	if n.Component != nil {
		for _, p := range n.Component.Props {
			propVal := codegen.NodeProp(n, p.Name)
			if propVal != nil {
				args = append(args, p.Name+" = "+cc.kc.EvalExpr(propVal))
			}
		}
	}
	hasSlot := n.Component != nil && n.Component.ChildrenType != nil
	if hasSlot && len(n.Children) > 0 {
		argStr := ""
		if len(args) > 0 {
			argStr = strings.Join(args, ", ") + ", "
		}
		cc.line("%s(%sslotContent = {", exportName(n.Name), argStr)
		cc.indent++
		for _, child := range n.Children {
			cc.renderStmt(child)
		}
		cc.indent--
		cc.line("})")
	} else {
		cc.line("%s(%s)", exportName(n.Name), strings.Join(args, ", "))
	}
}

// --- Helpers ---

func (cc *irComposeContext) resolveContent(n *ir.NodeInst) string {
	for _, name := range []string{"value", "text", "label", "content"} {
		if v := codegen.NodeProp(n, name); v != nil {
			return cc.kc.EvalExpr(v)
		}
	}
	return `""`
}

func (cc *irComposeContext) resolveTextProp(n *ir.NodeInst) string {
	for _, name := range []string{"text", "label", "value"} {
		if v := codegen.NodeProp(n, name); v != nil {
			return cc.kc.EvalExpr(v)
		}
	}
	return `""`
}

// buildModifierRaw returns just the modifier expression (e.g., "Modifier.padding(16.dp)")
// without the "modifier = " prefix.
func (cc *irComposeContext) buildModifierRaw(n *ir.NodeInst) string {
	styleFields := codegen.NodeStyleFields(n)
	if styleFields == nil {
		return "Modifier"
	}
	parts := []string{"Modifier"}
	for prop, expr := range styleFields {
		if mod := composeModifier(prop, cc.kc.EvalExpr(expr)); mod != "" {
			parts = append(parts, mod)
		}
	}
	return strings.Join(parts, ".")
}

func (cc *irComposeContext) buildModifier(n *ir.NodeInst) string {
	styleFields := codegen.NodeStyleFields(n)
	if styleFields == nil {
		return "modifier = Modifier"
	}
	parts := []string{"Modifier"}
	for prop, expr := range styleFields {
		if mod := composeModifier(prop, cc.kc.EvalExpr(expr)); mod != "" {
			parts = append(parts, mod)
		}
	}
	return "modifier = " + strings.Join(parts, ".")
}

func (cc *irComposeContext) textStyle(n *ir.NodeInst) string {
	styleFields := codegen.NodeStyleFields(n)
	if styleFields == nil {
		return ""
	}
	var styleParts []string
	for prop, expr := range styleFields {
		val := cc.kc.EvalExpr(expr)
		switch prop {
		case "fontSize":
			styleParts = append(styleParts, fmt.Sprintf("fontSize = %s.sp", val))
		case "fontWeight":
			if val == `"bold"` {
				styleParts = append(styleParts, "fontWeight = FontWeight.Bold")
			}
		case "textAlign":
			switch val {
			case `"center"`:
				styleParts = append(styleParts, "textAlign = TextAlign.Center")
			case `"right"`:
				styleParts = append(styleParts, "textAlign = TextAlign.End")
			}
		case "color":
			styleParts = append(styleParts, fmt.Sprintf("color = Color(android.graphics.Color.parseColor(%s))", val))
		}
	}
	if len(styleParts) == 0 {
		return ""
	}
	return "style = TextStyle(" + strings.Join(styleParts, ", ") + ")"
}

func composeModifier(prop, val string) string {
	switch prop {
	case "padding":
		return fmt.Sprintf("padding(%s.dp)", val)
	case "paddingTop":
		return fmt.Sprintf("padding(top = %s.dp)", val)
	case "paddingBottom":
		return fmt.Sprintf("padding(bottom = %s.dp)", val)
	case "paddingLeft":
		return fmt.Sprintf("padding(start = %s.dp)", val)
	case "paddingRight":
		return fmt.Sprintf("padding(end = %s.dp)", val)
	case "width":
		return fmt.Sprintf("width(%s.dp)", val)
	case "height":
		return fmt.Sprintf("height(%s.dp)", val)
	case "background":
		return fmt.Sprintf("background(Color(android.graphics.Color.parseColor(%s)))", val)
	case "opacity":
		return fmt.Sprintf("alpha(%s)", val)
	case "gap":
		// Gap handled by Arrangement in Column/Row
		return ""
	}
	return ""
}
