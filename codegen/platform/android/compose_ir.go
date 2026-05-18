package android

import (
	"fmt"
	"slices"
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
	case *ir.ErrorBoundary:
		for _, child := range s.Children {
			cc.renderStmt(child)
		}
	case *ir.Window:
		panic(fmt.Sprintf("android: unexpected nested Window in compose tree: %#v", s))
	case *ir.Assign, *ir.CallStmt, *ir.LocalVar, *ir.Return, *ir.Emit, *ir.Toggle:
		// Imperative stmts have no Compose rendering.
	case *ir.ContextProvider:
		panic(fmt.Sprintf("android: ContextProvider should be lowered before compose emission: %#v", s))
	default:
		panic(fmt.Sprintf("android.renderStmt: unhandled ir.Stmt %T", s))
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

// emitInputHandlerCall lowers one @input/@change handler attached to
// an input/entry composable. The handler body runs inside Compose's
// onValueChange lambda with the new text bound to `newValueVar`; the
// handler's event param (if any) is materialized as a tiny data
// holder so `e.value` reads return the same Kotlin string. Handlers
// with no event param (e.g. `@input { count += 1 }`) drop the alias.
func emitInputHandlerCall(cc *irComposeContext, n *ir.NodeInst, eventName, newValueVar string) {
	h := codegen.NodeHandler(n, eventName)
	if h == nil || h.Func == nil {
		return
	}
	// Each handler body runs in its own block so two handlers with
	// the same event-param name (e.g. both `@input(e)` and
	// `@change(e)`) don't collide on a single Kotlin scope.
	cc.line("run {")
	cc.indent++
	if len(h.Func.Params) > 0 {
		param := h.Func.Params[0]
		cc.line("val %s = SnglInputEvent(%s)", param.Name, newValueVar)
	}
	for _, stmt := range h.Func.Block {
		for _, line := range cc.kc.EvalStmt(stmt) {
			cc.line("%s", line)
		}
	}
	cc.indent--
	cc.line("}")
}

func (cc *irComposeContext) isUserComponent(comp *ir.Component) bool {
	return slices.Contains(cc.ctx.Pkg.Components, comp)
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
		// Text takes a `modifier` param even though our style helper
		// folds visual styling into TextStyle; the modifier carries
		// the testTag, which is the only thing finders rely on.
		mod := cc.buildModifier(n)
		ts := cc.textStyle(n)
		args := []string{"text = " + content, mod}
		if ts != "" {
			args = append(args, ts)
		}
		cc.line("Text(%s)", strings.Join(args, ", "))

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
		// Resolve `value=...` for the controlled-input expression.
		valueExpr := "\"\""
		if v := codegen.NodeProp(n, "value"); v != nil {
			valueExpr = cc.kc.EvalExpr(v)
		}
		placeholder := ""
		if s, ok := codegen.IRLiteralString(codegen.NodeProp(n, "placeholder")); ok {
			placeholder = s
		}
		// Collect @input and @change handlers — Compose has no
		// commit event distinct from per-keystroke change, so both
		// fire on onValueChange. The synthetic event is built per
		// handler using its first param's name, aliased to a
		// data-class holder so `e.value` resolves naturally.
		mod := cc.buildModifierRaw(n)
		cc.line("OutlinedTextField(")
		cc.indent++
		cc.line("value = %s,", valueExpr)
		cc.line("onValueChange = { newValue ->")
		cc.indent++
		emitInputHandlerCall(cc, n, "input", "newValue")
		emitInputHandlerCall(cc, n, "change", "newValue")
		emitInputHandlerCall(cc, n, "changed", "newValue")
		cc.indent--
		cc.line("},")
		if placeholder != "" {
			cc.line("label = { Text(%q) },", placeholder)
		}
		cc.line("modifier = %s", mod)
		cc.indent--
		cc.line(")")

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
// without the "modifier = " prefix. A user-authored #id (not the
// synthetic __nN ids from passReactivity) attaches a `.testTag("<id>")`
// so Compose UI tests can locate the node via `onNodeWithTag`.
func (cc *irComposeContext) buildModifierRaw(n *ir.NodeInst) string {
	parts := []string{"Modifier"}
	if id := userTestTag(n); id != "" {
		parts = append(parts, fmt.Sprintf("testTag(%q)", id))
	}
	for prop, expr := range codegen.NodeStyleFields(n) {
		if mod := composeModifier(prop, cc.kc.EvalExpr(expr)); mod != "" {
			parts = append(parts, mod)
		}
	}
	return strings.Join(parts, ".")
}

func (cc *irComposeContext) buildModifier(n *ir.NodeInst) string {
	return "modifier = " + cc.buildModifierRaw(n)
}

// userTestTag returns n.ID when it's a user-authored #id and worth
// surfacing as a Compose testTag. Synthetic __nN ids assigned by
// passReactivity are skipped — they aren't addressable from test
// source anyway.
func userTestTag(n *ir.NodeInst) string {
	if n == nil || n.ID == "" {
		return ""
	}
	if strings.HasPrefix(n.ID, "__n") {
		return ""
	}
	return n.ID
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
			styleParts = append(styleParts, fmt.Sprintf("color = Color(android.graphics.Color.parseColor(%s))", normalizeHexColor(val)))
		}
	}
	if len(styleParts) == 0 {
		return ""
	}
	return "style = TextStyle(" + strings.Join(styleParts, ", ") + ")"
}

// normalizeHexColor expands a 3-digit hex color literal ("#abc") to its
// 6-digit equivalent ("#aabbcc") since android.graphics.Color.parseColor
// rejects 3-digit shorthand. val is a Kotlin string-literal expression
// (e.g. `"#555"`), surrounding quotes preserved; non-literal expressions
// and already-normalized colors pass through unchanged.
func normalizeHexColor(val string) string {
	if len(val) < 2 || val[0] != '"' || val[len(val)-1] != '"' {
		return val
	}
	inner := val[1 : len(val)-1]
	if len(inner) != 4 || inner[0] != '#' {
		return val
	}
	for i := 1; i < 4; i++ {
		c := inner[i]
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F') {
			return val
		}
	}
	return fmt.Sprintf(`"#%c%c%c%c%c%c"`, inner[1], inner[1], inner[2], inner[2], inner[3], inner[3])
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
		return fmt.Sprintf("background(Color(android.graphics.Color.parseColor(%s)))", normalizeHexColor(val))
	case "opacity":
		return fmt.Sprintf("alpha(%s)", val)
	case "gap":
		// Gap handled by Arrangement in Column/Row
		return ""
	}
	return ""
}
