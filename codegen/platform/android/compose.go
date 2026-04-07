package android

import (
	"fmt"
	"maps"
	"strings"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/codegen"
)

// composeContext tracks state during Composable code generation.
type composeContext struct {
	ec             *exprContext
	buf            *strings.Builder
	indent         int
	doc            *ast.Document
	components     []*ast.Component
	hasSlot        bool                // true when rendering inside a component with slot support
	slotChildren   []*ast.VisualNode   // caller's children for slot expansion
	componentDepth int                 // recursion guard for component expansion
	callerEvents   map[string]ast.Expr // caller's event handlers (for emit propagation)
}

func (cc *composeContext) line(format string, args ...any) {
	fmt.Fprintf(cc.buf, "%s"+format+"\n", append([]any{strings.Repeat("    ", cc.indent)}, args...)...)
}

func (cc *composeContext) renderNode(vn *ast.VisualNode) {
	// Handle if
	if vn.If != nil {
		cond := exprToKtCond(*vn.If, cc.ec)
		cc.line("if (%s) {", cond)
		cc.indent++
		cc.renderNodeCore(vn)
		cc.indent--
		cc.line("}")
		return
	}

	// Handle for
	if vn.For != nil {
		iterVar := vn.For.Variable
		iterExpr := exprToKtValue(vn.For.Iterable, cc.ec)
		if vn.For.IndexVar != "" {
			cc.ec.localVars[vn.For.IndexVar] = true
			cc.line("%s.forEachIndexed { %s, %s ->", iterExpr, vn.For.IndexVar, iterVar)
		} else {
			cc.line("%s.forEach { %s ->", iterExpr, iterVar)
		}
		cc.indent++
		cc.ec.localVars[iterVar] = true
		cc.renderNodeCore(vn)
		delete(cc.ec.localVars, iterVar)
		if vn.For.IndexVar != "" {
			delete(cc.ec.localVars, vn.For.IndexVar)
		}
		cc.indent--
		cc.line("}")
		return
	}

	cc.renderNodeCore(vn)
}

func (cc *composeContext) renderNodeCore(vn *ast.VisualNode) {
	if vn.Component == "slot" {
		if cc.hasSlot && len(cc.slotChildren) > 0 {
			for _, child := range cc.slotChildren {
				cc.renderNode(child)
			}
		} else if cc.hasSlot {
			cc.line("slotContent()")
		}
		return
	}

	// Look up component definition
	comp := cc.findComponent(vn.Component)
	if comp != nil {
		body := codegen.ResolveComponentBody(comp, "android")
		if len(body) > 0 {
			cc.expandComponent(comp, vn, body)
		} else if len(comp.Body) > 0 {
			// User component with default body — emit as function call
			cc.renderUserComponent(vn)
		} else {
			// Fallback: component with no body at all
			cc.renderUserComponent(vn)
		}
		return
	}

	// Raw composable fallback (android.Column, android.Text, etc.)
	cc.renderRawComposable(vn)
}

func (cc *composeContext) findComponent(name string) *ast.Component {
	if cc.doc != nil {
		return cc.doc.FindComponent(name)
	}
	for _, c := range cc.components {
		if c.Name == name {
			return c
		}
	}
	return nil
}

// expandComponent inlines a component's body at the call site, binding
// props to component params as local Kotlin vals and providing slot children.
func (cc *composeContext) expandComponent(comp *ast.Component, vn *ast.VisualNode, body []*ast.VisualNode) {
	cc.componentDepth++
	if cc.componentDepth > 10 {
		cc.componentDepth--
		return
	}
	defer func() { cc.componentDepth-- }()

	savedLocals := maps.Clone(cc.ec.localVars)
	savedSlot := cc.hasSlot
	savedSlotChildren := cc.slotChildren

	// Bind component params: translate caller's prop expressions to Kotlin
	// strings and register them as local variable overrides so the expression
	// translator resolves them correctly.
	savedOverrides := cc.ec.propOverrides
	overrides := make(map[string]string)
	if savedOverrides != nil {
		maps.Copy(overrides, savedOverrides)
	}
	for _, p := range comp.Params {
		cc.ec.localVars[p.Name] = true
		if expr, ok := vn.Props[p.Name]; ok {
			overrides[p.Name] = exprToKtValue(expr, cc.ec)
		} else if p.Default.Literal != nil {
			overrides[p.Name] = literalToKt(p.Default)
		}
	}
	cc.ec.propOverrides = overrides

	// Propagate caller's events: when the override body uses @eventName={ emit X },
	// the codegen needs the caller's event handlers available. Store them so
	// renderRawComposable can find them.
	savedEvents := cc.callerEvents
	cc.callerEvents = vn.Events

	// Set slot children to the caller's children.
	cc.hasSlot = len(vn.Children) > 0
	cc.slotChildren = vn.Children

	for _, child := range body {
		cc.renderNode(child)
	}

	cc.ec.localVars = savedLocals
	cc.ec.propOverrides = savedOverrides
	cc.callerEvents = savedEvents
	cc.hasSlot = savedSlot
	cc.slotChildren = savedSlotChildren
}

func (cc *composeContext) renderUserComponent(vn *ast.VisualNode) {
	fnName := exportName(vn.Component)

	var comp *ast.Component
	for _, c := range cc.components {
		if c.Name == vn.Component {
			comp = c
			break
		}
	}

	var args []string
	if comp != nil {
		for _, p := range comp.Params {
			if expr, ok := vn.Props[p.Name]; ok {
				args = append(args, p.Name+" = "+exprToKtValue(expr, cc.ec))
			} else if p.Default.Literal != nil {
				args = append(args, p.Name+" = "+literalToKt(p.Default))
			}
		}
	} else {
		for name, expr := range vn.Props {
			args = append(args, name+" = "+exprToKtValue(expr, cc.ec))
		}
	}
	// If component accepts children, pass them as a trailing composable lambda
	if comp != nil && comp.ChildrenType != "" && len(vn.Children) > 0 {
		cc.line("%s(%s) {", fnName, strings.Join(args, ", "))
		cc.indent++
		for _, child := range vn.Children {
			cc.renderNode(child)
		}
		cc.indent--
		cc.line("}")
	} else {
		cc.line("%s(%s)", fnName, strings.Join(args, ", "))
	}
}

// renderRawComposable emits a generic Compose composable call for any
// component name not found as a user-defined component. This enables
// android.Column, android.Text, etc. to be used directly.
func (cc *composeContext) renderRawComposable(vn *ast.VisualNode) {
	// Extract composable name: "android.Column" → "Column", "Column" → "Column"
	name := vn.Component
	if _, local, ok := strings.Cut(name, "."); ok {
		name = local
	}

	// Build named arguments from props (excluding style which becomes modifier).
	var args []string

	// Event props: use the node's own events, falling back to caller events
	// when inside a component expansion. Consume caller events so they don't
	// propagate further to descendant composables.
	events := vn.Events
	if len(events) == 0 && cc.callerEvents != nil {
		events = cc.callerEvents
		cc.callerEvents = nil
	}

	// Composables that accept onClick as a named parameter.
	onClickComposables := map[string]bool{
		"Button": true, "IconButton": true, "TextButton": true,
		"OutlinedButton": true, "FilledTonalButton": true,
		"Card": true, "ElevatedCard": true, "OutlinedCard": true,
		"Surface": true, "AssistChip": true, "FilterChip": true,
		"InputChip": true, "SuggestionChip": true,
		"NavigationBarItem": true, "NavigationRailItem": true,
		"DropdownMenuItem": true, "FloatingActionButton": true,
	}

	// Modifier from style props — also inject click handler as Modifier.clickable
	// for composables that don't accept onClick as a parameter.
	mod := buildModifierExpr(vn.StyleFields(), cc.ec)
	if clickExpr, ok := events["click"]; ok && clickExpr.SNGL != nil && !onClickComposables[name] {
		stmts := cc.ec.translateMutation(clickExpr.SNGL)
		var b strings.Builder
		fmt.Fprintf(&b, ".clickable {\n")
		for _, s := range stmts {
			fmt.Fprintf(&b, "%s%s\n", strings.Repeat("    ", cc.indent+2), s)
		}
		fmt.Fprintf(&b, "%s}", strings.Repeat("    ", cc.indent+1))
		if mod == "Modifier" {
			mod = "Modifier\n    " + b.String()
		} else {
			mod += "\n    " + b.String()
		}
	}
	if mod != "Modifier" {
		args = append(args, "modifier = "+mod)
	}

	// Regular props → named Kotlin arguments
	for propName, expr := range vn.Props {
		if propName == "style" {
			continue // handled as modifier above
		}
		val := exprToKtValue(expr, cc.ec)
		args = append(args, propName+" = "+val)
	}

	// Map SNGL event names → Compose parameter names, only for composables
	// that accept those parameters.
	valueChangeComposables := map[string]bool{
		"OutlinedTextField": true, "TextField": true, "BasicTextField": true,
	}
	checkedChangeComposables := map[string]bool{
		"Checkbox": true, "Switch": true, "RadioButton": true,
	}
	eventMappings := map[string]string{}
	if valueChangeComposables[name] {
		eventMappings["input"] = "onValueChange"
	}
	if checkedChangeComposables[name] {
		eventMappings["change"] = "onCheckedChange"
	}
	for snglName, ktName := range eventMappings {
		expr, ok := events[snglName]
		if !ok || expr.SNGL == nil {
			continue
		}

		isValueChange := ktName == "onValueChange"
		if isValueChange {
			cc.ec.eventVar = "_inputValue_"
		}
		stmts := cc.ec.translateMutation(expr.SNGL)
		if isValueChange {
			cc.ec.eventVar = ""
		}

		var b strings.Builder
		if isValueChange {
			b.WriteString("{ _v_ ->\n")
			for _, s := range stmts {
				s = strings.ReplaceAll(s, "_inputValue_.value", "_v_")
				s = strings.ReplaceAll(s, "_inputValue_", "_v_")
				fmt.Fprintf(&b, "%s%s\n", strings.Repeat("    ", cc.indent+2), s)
			}
		} else {
			b.WriteString("{\n")
			for _, s := range stmts {
				fmt.Fprintf(&b, "%s%s\n", strings.Repeat("    ", cc.indent+2), s)
			}
		}
		fmt.Fprintf(&b, "%s}", strings.Repeat("    ", cc.indent+1))
		args = append(args, ktName+" = "+b.String())
	}

	// onClick as named param for composables that support it.
	if clickExpr, ok := events["click"]; ok && clickExpr.SNGL != nil && onClickComposables[name] {
		stmts := cc.ec.translateMutation(clickExpr.SNGL)
		var b strings.Builder
		b.WriteString("{\n")
		for _, s := range stmts {
			fmt.Fprintf(&b, "%s%s\n", strings.Repeat("    ", cc.indent+2), s)
		}
		fmt.Fprintf(&b, "%s}", strings.Repeat("    ", cc.indent+1))
		args = append(args, "onClick = "+b.String())
	}

	// Pass through non-standard events directly.
	standardEvents := map[string]bool{"click": true, "input": true, "change": true}
	for evtName, expr := range vn.Events {
		if expr.SNGL == nil || standardEvents[evtName] {
			continue
		}
		stmts := cc.ec.translateMutation(expr.SNGL)
		var b strings.Builder
		b.WriteString("{\n")
		for _, s := range stmts {
			fmt.Fprintf(&b, "%s%s\n", strings.Repeat("    ", cc.indent+2), s)
		}
		fmt.Fprintf(&b, "%s}", strings.Repeat("    ", cc.indent+1))
		args = append(args, evtName+" = "+b.String())
	}

	// Sort args for deterministic output
	sortArgs(args)

	argStr := strings.Join(args, ",\n"+strings.Repeat("    ", cc.indent+1))
	if len(args) > 0 {
		argStr = "\n" + strings.Repeat("    ", cc.indent+1) + argStr + "\n" + strings.Repeat("    ", cc.indent)
	}

	if len(vn.Children) > 0 {
		cc.line("%s(%s) {", name, argStr)
		cc.indent++
		for _, child := range vn.Children {
			cc.renderNode(child)
		}
		cc.indent--
		cc.line("}")
	} else {
		cc.line("%s(%s)", name, argStr)
	}
}

// sortArgs sorts named Kotlin arguments for deterministic output.
func sortArgs(args []string) {
	for i := range args {
		for j := i + 1; j < len(args); j++ {
			if args[i] > args[j] {
				args[i], args[j] = args[j], args[i]
			}
		}
	}
}
