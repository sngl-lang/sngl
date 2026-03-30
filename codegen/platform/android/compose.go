package android

import (
	"fmt"
	"strings"

	"git.duckfam.us/jonathan/sngl/ast"
)

// composeContext tracks state during Composable code generation.
type composeContext struct {
	ec         *exprContext
	buf        *strings.Builder
	indent     int
	components []*ast.Component
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
	switch vn.Component {
	case "vbox":
		cc.renderColumn(vn)
	case "hbox":
		cc.renderRow(vn)
	case "stack":
		cc.renderBox(vn)
	case "scroll":
		cc.renderScroll(vn)
	case "spacer":
		cc.renderSpacer(vn)
	case "text":
		cc.renderText(vn)
	case "image":
		cc.renderImage(vn)
	case "badge":
		cc.renderBadge(vn)
	case "progress":
		cc.renderProgress(vn)
	case "spinner":
		cc.renderSpinner(vn)
	case "divider":
		cc.renderDivider(vn)
	case "card":
		cc.renderCard(vn)
	case "button":
		cc.renderButton(vn)
	case "input":
		cc.renderInput(vn)
	case "checkbox":
		cc.renderCheckbox(vn)
	case "radio":
		cc.renderRadio(vn)
	case "toggle":
		cc.renderToggle(vn)
	case "select":
		cc.renderSelect(vn)
	case "textarea":
		cc.renderTextarea(vn)
	case "datepicker":
		cc.renderDatepicker(vn)
	case "chip":
		cc.renderChip(vn)
	case "link":
		cc.renderLink(vn)
	case "tabs":
		cc.renderTabs(vn)
	case "modal":
		cc.renderModal(vn)
	case "drawer":
		cc.renderDrawer(vn)
	case "tooltip":
		cc.renderTooltip(vn)
	case "popover":
		cc.renderPopover(vn)
	case "menu":
		cc.renderMenu(vn)
	case "menubar":
		cc.renderMenubar(vn)
	case "toolbar":
		cc.renderToolbar(vn)
	case "accordion":
		cc.renderAccordion(vn)
	case "splitview":
		cc.renderSplitview(vn)
	case "table":
		cc.renderTable(vn)
	case "tree":
		cc.renderTree(vn)
	case "avatar":
		cc.renderAvatar(vn)
	case "pullrefresh":
		cc.renderPullrefresh(vn)
	case "toast":
		cc.renderToast(vn)
	default:
		cc.renderUserComponent(vn)
	}
}

func (cc *composeContext) renderColumn(vn *ast.VisualNode) {
	mod := buildModifierExpr(vn.StyleAttrs, vn.StyleBlock, cc.ec)
	gap := cc.getGap(vn)
	arr := ""
	if gap != "" {
		arr = fmt.Sprintf(",\n%sverticalArrangement = Arrangement.spacedBy(%s)", strings.Repeat("    ", cc.indent+1), gap)
	}
	cc.line("Column(\n%smodifier = %s%s\n%s) {", strings.Repeat("    ", cc.indent+1), mod, arr, strings.Repeat("    ", cc.indent))
	cc.indent++
	for _, child := range vn.Children {
		cc.renderNode(child)
	}
	cc.indent--
	cc.line("}")
}

func (cc *composeContext) renderRow(vn *ast.VisualNode) {
	mod := buildModifierExpr(vn.StyleAttrs, vn.StyleBlock, cc.ec)
	gap := cc.getGap(vn)
	arr := ""
	if gap != "" {
		arr = fmt.Sprintf(",\n%shorizontalArrangement = Arrangement.spacedBy(%s)", strings.Repeat("    ", cc.indent+1), gap)
	}
	cc.line("Row(\n%smodifier = %s%s\n%s) {", strings.Repeat("    ", cc.indent+1), mod, arr, strings.Repeat("    ", cc.indent))
	cc.indent++
	for _, child := range vn.Children {
		cc.renderNode(child)
	}
	cc.indent--
	cc.line("}")
}

func (cc *composeContext) renderBox(vn *ast.VisualNode) {
	mod := buildModifierExpr(vn.StyleAttrs, vn.StyleBlock, cc.ec)
	cc.line("Box(modifier = %s) {", mod)
	cc.indent++
	for _, child := range vn.Children {
		cc.renderNode(child)
	}
	cc.indent--
	cc.line("}")
}

func (cc *composeContext) renderScroll(vn *ast.VisualNode) {
	mod := buildModifierExpr(vn.StyleAttrs, vn.StyleBlock, cc.ec)
	cc.line("Column(modifier = %s.verticalScroll(rememberScrollState())) {", mod)
	cc.indent++
	for _, child := range vn.Children {
		cc.renderNode(child)
	}
	cc.indent--
	cc.line("}")
}

func (cc *composeContext) renderSpacer(vn *ast.VisualNode) {
	mod := buildModifierExpr(vn.StyleAttrs, vn.StyleBlock, cc.ec)
	cc.line("Spacer(modifier = %s)", mod)
}

func (cc *composeContext) renderText(vn *ast.VisualNode) {
	val := `""`
	if v, ok := vn.Props["value"]; ok {
		val = exprToKtValue(v, cc.ec)
	}
	textStyle := buildTextStyleExpr(vn.StyleAttrs, vn.StyleBlock, cc.ec)
	mod := buildModifierExpr(vn.StyleAttrs, vn.StyleBlock, cc.ec)
	extras := ""
	if textStyle != "" {
		extras += ",\n" + strings.Repeat("    ", cc.indent+1) + "style = " + textStyle
	}
	if mod != "Modifier" {
		extras += ",\n" + strings.Repeat("    ", cc.indent+1) + "modifier = " + mod
	}
	cc.line("Text(\n%stext = %s%s\n%s)", strings.Repeat("    ", cc.indent+1), val, extras, strings.Repeat("    ", cc.indent))
}

func (cc *composeContext) renderImage(vn *ast.VisualNode) {
	mod := buildModifierExpr(vn.StyleAttrs, vn.StyleBlock, cc.ec)
	src := `""`
	if v, ok := vn.Props["src"]; ok {
		src = exprToKtValue(v, cc.ec)
	}
	alt := `""`
	if v, ok := vn.Props["alt"]; ok {
		alt = exprToKtValue(v, cc.ec)
	}
	cc.line("// AsyncImage requires Coil dependency")
	cc.line("AsyncImage(\n%smodel = %s,\n%scontentDescription = %s,\n%smodifier = %s\n%s)",
		strings.Repeat("    ", cc.indent+1), src,
		strings.Repeat("    ", cc.indent+1), alt,
		strings.Repeat("    ", cc.indent+1), mod,
		strings.Repeat("    ", cc.indent))
}

func (cc *composeContext) renderBadge(vn *ast.VisualNode) {
	val := `""`
	if v, ok := vn.Props["value"]; ok {
		val = exprToKtValue(v, cc.ec)
	}
	cc.line("Badge { Text(%s) }", val)
}

func (cc *composeContext) renderProgress(vn *ast.VisualNode) {
	mod := buildModifierExpr(vn.StyleAttrs, vn.StyleBlock, cc.ec)
	value := "0"
	if v, ok := vn.Props["value"]; ok {
		value = exprToKtValue(v, cc.ec)
	}
	maxVal := "100"
	if v, ok := vn.Props["max"]; ok {
		maxVal = exprToKtValue(v, cc.ec)
	}
	extras := ""
	if mod != "Modifier" {
		extras = ",\n" + strings.Repeat("    ", cc.indent+1) + "modifier = " + mod
	}
	cc.line("LinearProgressIndicator(\n%sprogress = { (%s).toFloat() / (%s).toFloat() }%s\n%s)",
		strings.Repeat("    ", cc.indent+1), value, maxVal, extras, strings.Repeat("    ", cc.indent))
}

func (cc *composeContext) renderSpinner(vn *ast.VisualNode) {
	mod := buildModifierExpr(vn.StyleAttrs, vn.StyleBlock, cc.ec)
	if mod != "Modifier" {
		cc.line("CircularProgressIndicator(modifier = %s)", mod)
	} else {
		cc.line("CircularProgressIndicator()")
	}
}

func (cc *composeContext) renderDivider(vn *ast.VisualNode) {
	mod := buildModifierExpr(vn.StyleAttrs, vn.StyleBlock, cc.ec)
	if mod != "Modifier" {
		cc.line("HorizontalDivider(modifier = %s)", mod)
	} else {
		cc.line("HorizontalDivider()")
	}
}

func (cc *composeContext) renderCard(vn *ast.VisualNode) {
	mod := buildModifierExpr(vn.StyleAttrs, vn.StyleBlock, cc.ec)
	cc.line("Card(modifier = %s) {", mod)
	cc.indent++
	for _, child := range vn.Children {
		cc.renderNode(child)
	}
	cc.indent--
	cc.line("}")
}

func (cc *composeContext) renderButton(vn *ast.VisualNode) {
	text := `""`
	if v, ok := vn.Props["text"]; ok {
		text = exprToKtValue(v, cc.ec)
	}
	onClick := "{ }"
	if evt, ok := vn.Events["click"]; ok && evt.SNGL != nil {
		stmts := cc.ec.translateMutation(evt.SNGL)
		onClick = "{\n"
		for _, s := range stmts {
			onClick += strings.Repeat("    ", cc.indent+2) + s + "\n"
		}
		onClick += strings.Repeat("    ", cc.indent+1) + "}"
	}
	disabled := ""
	if v, ok := vn.Props["disabled"]; ok {
		disabled = fmt.Sprintf(",\n%senabled = !(%s)", strings.Repeat("    ", cc.indent+1), exprToKtValue(v, cc.ec))
	}
	cc.line("Button(\n%sonClick = %s%s\n%s) {",
		strings.Repeat("    ", cc.indent+1), onClick, disabled, strings.Repeat("    ", cc.indent))
	cc.indent++
	cc.line("Text(%s)", text)
	cc.indent--
	cc.line("}")
}

func (cc *composeContext) renderInput(vn *ast.VisualNode) {
	value := `""`
	if v, ok := vn.Props["value"]; ok {
		value = exprToKtValue(v, cc.ec)
	}
	onValueChange := "{ }"
	if evt, ok := vn.Events["input"]; ok && evt.SNGL != nil {
		cc.ec.eventVar = "it"
		stmts := cc.ec.translateMutation(evt.SNGL)
		cc.ec.eventVar = ""
		onValueChange = "{ it ->\n"
		for _, s := range stmts {
			onValueChange += strings.Repeat("    ", cc.indent+2) + s + "\n"
		}
		onValueChange += strings.Repeat("    ", cc.indent+1) + "}"
	}
	mod := buildModifierExpr(vn.StyleAttrs, vn.StyleBlock, cc.ec)
	extras := ""
	if mod != "Modifier" {
		extras += ",\n" + strings.Repeat("    ", cc.indent+1) + "modifier = " + mod
	}
	if v, ok := vn.Props["placeholder"]; ok {
		ph := exprToKtValue(v, cc.ec)
		extras += fmt.Sprintf(",\n%splaceholder = { Text(%s) }", strings.Repeat("    ", cc.indent+1), ph)
	}
	cc.line("OutlinedTextField(\n%svalue = %s,\n%sonValueChange = %s%s\n%s)",
		strings.Repeat("    ", cc.indent+1), value,
		strings.Repeat("    ", cc.indent+1), onValueChange,
		extras, strings.Repeat("    ", cc.indent))
}

func (cc *composeContext) renderCheckbox(vn *ast.VisualNode) {
	checked := "false"
	if v, ok := vn.Props["checked"]; ok {
		checked = exprToKtValue(v, cc.ec)
	}
	onCheckedChange := "{ }"
	if evt, ok := vn.Events["change"]; ok && evt.SNGL != nil {
		stmts := cc.ec.translateMutation(evt.SNGL)
		onCheckedChange = "{ _checked ->\n"
		for _, s := range stmts {
			onCheckedChange += strings.Repeat("    ", cc.indent+2) + s + "\n"
		}
		onCheckedChange += strings.Repeat("    ", cc.indent+1) + "}"
	}
	label := ""
	if v, ok := vn.Props["label"]; ok {
		label = exprToKtValue(v, cc.ec)
	}
	cc.line("Row(verticalAlignment = Alignment.CenterVertically) {")
	cc.indent++
	cc.line("Checkbox(\n%schecked = %s,\n%sonCheckedChange = %s\n%s)",
		strings.Repeat("    ", cc.indent+1), checked,
		strings.Repeat("    ", cc.indent+1), onCheckedChange,
		strings.Repeat("    ", cc.indent))
	if label != "" {
		cc.line("Text(%s)", label)
	}
	cc.indent--
	cc.line("}")
}

func (cc *composeContext) renderRadio(vn *ast.VisualNode) {
	value := `""`
	if v, ok := vn.Props["value"]; ok {
		value = exprToKtValue(v, cc.ec)
	}
	options := `listOf<Any>()`
	if v, ok := vn.Props["options"]; ok {
		options = exprToKtValue(v, cc.ec)
	}
	onChange := ""
	if evt, ok := vn.Events["change"]; ok && evt.SNGL != nil {
		stmts := cc.ec.translateMutation(evt.SNGL)
		onChange = strings.Join(stmts, "; ")
	}
	cc.line("Column {")
	cc.indent++
	cc.line("%s.forEach { option ->", options)
	cc.indent++
	cc.line("Row(verticalAlignment = Alignment.CenterVertically) {")
	cc.indent++
	if onChange != "" {
		cc.line("RadioButton(\n%sselected = option.toString() == (%s).toString(),\n%sonClick = { %s }\n%s)",
			strings.Repeat("    ", cc.indent+1), value,
			strings.Repeat("    ", cc.indent+1), onChange,
			strings.Repeat("    ", cc.indent))
	} else {
		cc.line("RadioButton(\n%sselected = option.toString() == (%s).toString(),\n%sonClick = { }\n%s)",
			strings.Repeat("    ", cc.indent+1), value,
			strings.Repeat("    ", cc.indent+1),
			strings.Repeat("    ", cc.indent))
	}
	cc.line("Text(option.toString())")
	cc.indent--
	cc.line("}")
	cc.indent--
	cc.line("}")
	cc.indent--
	cc.line("}")
}

func (cc *composeContext) renderToggle(vn *ast.VisualNode) {
	checked := "false"
	if v, ok := vn.Props["checked"]; ok {
		checked = exprToKtValue(v, cc.ec)
	}
	onCheckedChange := "{ }"
	if evt, ok := vn.Events["change"]; ok && evt.SNGL != nil {
		stmts := cc.ec.translateMutation(evt.SNGL)
		onCheckedChange = "{\n"
		for _, s := range stmts {
			onCheckedChange += strings.Repeat("    ", cc.indent+2) + s + "\n"
		}
		onCheckedChange += strings.Repeat("    ", cc.indent+1) + "}"
	}
	label := ""
	if v, ok := vn.Props["label"]; ok {
		label = exprToKtValue(v, cc.ec)
	}
	cc.line("Row(verticalAlignment = Alignment.CenterVertically) {")
	cc.indent++
	cc.line("Switch(\n%schecked = %s,\n%sonCheckedChange = %s\n%s)",
		strings.Repeat("    ", cc.indent+1), checked,
		strings.Repeat("    ", cc.indent+1), onCheckedChange,
		strings.Repeat("    ", cc.indent))
	if label != "" {
		cc.line("Text(%s)", label)
	}
	cc.indent--
	cc.line("}")
}

func (cc *composeContext) renderSelect(vn *ast.VisualNode) {
	value := `""`
	if v, ok := vn.Props["value"]; ok {
		value = exprToKtValue(v, cc.ec)
	}
	options := `listOf<Any>()`
	if v, ok := vn.Props["options"]; ok {
		options = exprToKtValue(v, cc.ec)
	}
	onChange := ""
	if evt, ok := vn.Events["change"]; ok && evt.SNGL != nil {
		stmts := cc.ec.translateMutation(evt.SNGL)
		onChange = strings.Join(stmts, "; ")
	}
	cc.line("var expanded by remember { mutableStateOf(false) }")
	cc.line("ExposedDropdownMenuBox(\n%sexpanded = expanded,\n%sonExpandedChange = { expanded = it }\n%s) {",
		strings.Repeat("    ", cc.indent+1),
		strings.Repeat("    ", cc.indent+1),
		strings.Repeat("    ", cc.indent))
	cc.indent++
	cc.line("OutlinedTextField(\n%svalue = (%s).toString(),\n%sonValueChange = { },\n%sreadOnly = true,\n%smodifier = Modifier.menuAnchor()\n%s)",
		strings.Repeat("    ", cc.indent+1), value,
		strings.Repeat("    ", cc.indent+1),
		strings.Repeat("    ", cc.indent+1),
		strings.Repeat("    ", cc.indent+1),
		strings.Repeat("    ", cc.indent))
	cc.line("ExposedDropdownMenu(\n%sexpanded = expanded,\n%sonDismissRequest = { expanded = false }\n%s) {",
		strings.Repeat("    ", cc.indent+1),
		strings.Repeat("    ", cc.indent+1),
		strings.Repeat("    ", cc.indent))
	cc.indent++
	cc.line("%s.forEach { option ->", options)
	cc.indent++
	if onChange != "" {
		cc.line("DropdownMenuItem(\n%stext = { Text(option.toString()) },\n%sonClick = { expanded = false; %s }\n%s)",
			strings.Repeat("    ", cc.indent+1),
			strings.Repeat("    ", cc.indent+1), onChange,
			strings.Repeat("    ", cc.indent))
	} else {
		cc.line("DropdownMenuItem(\n%stext = { Text(option.toString()) },\n%sonClick = { expanded = false }\n%s)",
			strings.Repeat("    ", cc.indent+1),
			strings.Repeat("    ", cc.indent+1),
			strings.Repeat("    ", cc.indent))
	}
	cc.indent--
	cc.line("}")
	cc.indent--
	cc.line("}")
	cc.indent--
	cc.line("}")
}

func (cc *composeContext) renderTextarea(vn *ast.VisualNode) {
	value := `""`
	if v, ok := vn.Props["value"]; ok {
		value = exprToKtValue(v, cc.ec)
	}
	onValueChange := "{ }"
	if evt, ok := vn.Events["input"]; ok && evt.SNGL != nil {
		cc.ec.eventVar = "it"
		stmts := cc.ec.translateMutation(evt.SNGL)
		cc.ec.eventVar = ""
		onValueChange = "{ it ->\n"
		for _, s := range stmts {
			onValueChange += strings.Repeat("    ", cc.indent+2) + s + "\n"
		}
		onValueChange += strings.Repeat("    ", cc.indent+1) + "}"
	}
	mod := buildModifierExpr(vn.StyleAttrs, vn.StyleBlock, cc.ec)
	extras := ""
	if mod != "Modifier" {
		extras += ",\n" + strings.Repeat("    ", cc.indent+1) + "modifier = " + mod
	}
	extras += fmt.Sprintf(",\n%sminLines = 3", strings.Repeat("    ", cc.indent+1))
	if v, ok := vn.Props["placeholder"]; ok {
		ph := exprToKtValue(v, cc.ec)
		extras += fmt.Sprintf(",\n%splaceholder = { Text(%s) }", strings.Repeat("    ", cc.indent+1), ph)
	}
	cc.line("OutlinedTextField(\n%svalue = %s,\n%sonValueChange = %s%s\n%s)",
		strings.Repeat("    ", cc.indent+1), value,
		strings.Repeat("    ", cc.indent+1), onValueChange,
		extras, strings.Repeat("    ", cc.indent))
}

func (cc *composeContext) renderDatepicker(vn *ast.VisualNode) {
	value := `""`
	if v, ok := vn.Props["value"]; ok {
		value = exprToKtValue(v, cc.ec)
	}
	cc.line("// DatePicker: simplified as text field")
	cc.line("OutlinedTextField(\n%svalue = (%s).toString(),\n%sonValueChange = { },\n%sreadOnly = true,\n%splaceholder = { Text(\"YYYY-MM-DD\") }\n%s)",
		strings.Repeat("    ", cc.indent+1), value,
		strings.Repeat("    ", cc.indent+1),
		strings.Repeat("    ", cc.indent+1),
		strings.Repeat("    ", cc.indent+1),
		strings.Repeat("    ", cc.indent))
}

func (cc *composeContext) renderChip(vn *ast.VisualNode) {
	label := `""`
	if v, ok := vn.Props["label"]; ok {
		label = exprToKtValue(v, cc.ec)
	}
	onClick := "{ }"
	if evt, ok := vn.Events["click"]; ok && evt.SNGL != nil {
		stmts := cc.ec.translateMutation(evt.SNGL)
		onClick = "{\n"
		for _, s := range stmts {
			onClick += strings.Repeat("    ", cc.indent+2) + s + "\n"
		}
		onClick += strings.Repeat("    ", cc.indent+1) + "}"
	}
	cc.line("AssistChip(\n%sonClick = %s,\n%slabel = { Text(%s) }\n%s)",
		strings.Repeat("    ", cc.indent+1), onClick,
		strings.Repeat("    ", cc.indent+1), label,
		strings.Repeat("    ", cc.indent))
}

func (cc *composeContext) renderLink(vn *ast.VisualNode) {
	text := `""`
	if v, ok := vn.Props["text"]; ok {
		text = exprToKtValue(v, cc.ec)
	}
	href := `""`
	if v, ok := vn.Props["href"]; ok {
		href = exprToKtValue(v, cc.ec)
	}
	cc.line("// Link: %s", href)
	cc.line("TextButton(onClick = { /* open URI */ }) {")
	cc.indent++
	cc.line("Text(%s)", text)
	cc.indent--
	cc.line("}")
}

func (cc *composeContext) renderTabs(vn *ast.VisualNode) {
	items := `listOf<Any>()`
	if v, ok := vn.Props["items"]; ok {
		items = exprToKtValue(v, cc.ec)
	}
	selected := "0"
	if v, ok := vn.Props["selected"]; ok {
		selected = exprToKtValue(v, cc.ec)
	}
	onChange := ""
	if evt, ok := vn.Events["change"]; ok && evt.SNGL != nil {
		stmts := cc.ec.translateMutation(evt.SNGL)
		onChange = strings.Join(stmts, "; ")
	}
	cc.line("TabRow(selectedTabIndex = %s) {", selected)
	cc.indent++
	cc.line("%s.forEachIndexed { index, item ->", items)
	cc.indent++
	if onChange != "" {
		cc.line("Tab(\n%sselected = index == %s,\n%sonClick = { %s },\n%stext = { Text(item.toString()) }\n%s)",
			strings.Repeat("    ", cc.indent+1), selected,
			strings.Repeat("    ", cc.indent+1), onChange,
			strings.Repeat("    ", cc.indent+1),
			strings.Repeat("    ", cc.indent))
	} else {
		cc.line("Tab(\n%sselected = index == %s,\n%sonClick = { },\n%stext = { Text(item.toString()) }\n%s)",
			strings.Repeat("    ", cc.indent+1), selected,
			strings.Repeat("    ", cc.indent+1),
			strings.Repeat("    ", cc.indent+1),
			strings.Repeat("    ", cc.indent))
	}
	cc.indent--
	cc.line("}")
	cc.indent--
	cc.line("}")
}

func (cc *composeContext) renderModal(vn *ast.VisualNode) {
	open := "false"
	if v, ok := vn.Props["open"]; ok {
		open = exprToKtValue(v, cc.ec)
	}
	title := `""`
	if v, ok := vn.Props["title"]; ok {
		title = exprToKtValue(v, cc.ec)
	}
	cc.line("if (%s) {", open)
	cc.indent++
	cc.line("AlertDialog(\n%sonDismissRequest = { },\n%stitle = { Text(%s) },\n%stext = {",
		strings.Repeat("    ", cc.indent+1),
		strings.Repeat("    ", cc.indent+1), title,
		strings.Repeat("    ", cc.indent+1))
	cc.indent++
	cc.line("Column {")
	cc.indent++
	for _, child := range vn.Children {
		cc.renderNode(child)
	}
	cc.indent--
	cc.line("}")
	cc.indent--
	cc.line("},\n%sconfirmButton = { }\n%s)", strings.Repeat("    ", cc.indent+1), strings.Repeat("    ", cc.indent))
	cc.indent--
	cc.line("}")
}

func (cc *composeContext) renderDrawer(vn *ast.VisualNode) {
	cc.line("ModalNavigationDrawer(")
	cc.indent++
	cc.line("drawerContent = {")
	cc.indent++
	cc.line("ModalDrawerSheet {")
	cc.indent++
	// Render first child as drawer content if available
	if len(vn.Children) > 0 {
		cc.renderNode(vn.Children[0])
	}
	cc.indent--
	cc.line("}")
	cc.indent--
	cc.line("}")
	cc.indent--
	cc.line(") {")
	cc.indent++
	// Render remaining children as main content
	for _, child := range vn.Children[1:] {
		cc.renderNode(child)
	}
	cc.indent--
	cc.line("}")
}

func (cc *composeContext) renderTooltip(vn *ast.VisualNode) {
	text := `""`
	if v, ok := vn.Props["text"]; ok {
		text = exprToKtValue(v, cc.ec)
	}
	cc.line("TooltipBox(\n%spositionProvider = TooltipDefaults.rememberPlainTooltipPositionProvider(),\n%stooltip = { PlainTooltip { Text(%s) } },\n%sstate = rememberTooltipState()\n%s) {",
		strings.Repeat("    ", cc.indent+1),
		strings.Repeat("    ", cc.indent+1), text,
		strings.Repeat("    ", cc.indent+1),
		strings.Repeat("    ", cc.indent))
	cc.indent++
	for _, child := range vn.Children {
		cc.renderNode(child)
	}
	cc.indent--
	cc.line("}")
}

func (cc *composeContext) renderPopover(vn *ast.VisualNode) {
	open := "false"
	if v, ok := vn.Props["open"]; ok {
		open = exprToKtValue(v, cc.ec)
	}
	cc.line("DropdownMenu(\n%sexpanded = %s,\n%sonDismissRequest = { }\n%s) {",
		strings.Repeat("    ", cc.indent+1), open,
		strings.Repeat("    ", cc.indent+1),
		strings.Repeat("    ", cc.indent))
	cc.indent++
	for _, child := range vn.Children {
		cc.renderNode(child)
	}
	cc.indent--
	cc.line("}")
}

func (cc *composeContext) renderMenu(vn *ast.VisualNode) {
	open := "false"
	if v, ok := vn.Props["open"]; ok {
		open = exprToKtValue(v, cc.ec)
	}
	items := `listOf<Any>()`
	if v, ok := vn.Props["items"]; ok {
		items = exprToKtValue(v, cc.ec)
	}
	cc.line("DropdownMenu(\n%sexpanded = %s,\n%sonDismissRequest = { }\n%s) {",
		strings.Repeat("    ", cc.indent+1), open,
		strings.Repeat("    ", cc.indent+1),
		strings.Repeat("    ", cc.indent))
	cc.indent++
	cc.line("%s.forEach { item ->", items)
	cc.indent++
	cc.line("DropdownMenuItem(\n%stext = { Text(item.toString()) },\n%sonClick = { }\n%s)",
		strings.Repeat("    ", cc.indent+1),
		strings.Repeat("    ", cc.indent+1),
		strings.Repeat("    ", cc.indent))
	cc.indent--
	cc.line("}")
	cc.indent--
	cc.line("}")
}

func (cc *composeContext) renderMenubar(vn *ast.VisualNode) {
	items := `listOf<Any>()`
	if v, ok := vn.Props["items"]; ok {
		items = exprToKtValue(v, cc.ec)
	}
	cc.line("Row {")
	cc.indent++
	cc.line("%s.forEach { item ->", items)
	cc.indent++
	cc.line("TextButton(onClick = { }) { Text(item.toString()) }")
	cc.indent--
	cc.line("}")
	cc.indent--
	cc.line("}")
}

func (cc *composeContext) renderToolbar(vn *ast.VisualNode) {
	cc.line("Row(\n%smodifier = Modifier.fillMaxWidth(),\n%shorizontalArrangement = Arrangement.SpaceBetween\n%s) {",
		strings.Repeat("    ", cc.indent+1),
		strings.Repeat("    ", cc.indent+1),
		strings.Repeat("    ", cc.indent))
	cc.indent++
	for _, child := range vn.Children {
		cc.renderNode(child)
	}
	cc.indent--
	cc.line("}")
}

func (cc *composeContext) renderAccordion(vn *ast.VisualNode) {
	items := `listOf<Any>()`
	if v, ok := vn.Props["items"]; ok {
		items = exprToKtValue(v, cc.ec)
	}
	cc.line("Column {")
	cc.indent++
	cc.line("%s.forEachIndexed { index, item ->", items)
	cc.indent++
	cc.line("var sectionExpanded by remember { mutableStateOf(false) }")
	cc.line("TextButton(onClick = { sectionExpanded = !sectionExpanded }) {")
	cc.indent++
	cc.line("Text((if (sectionExpanded) \"▼ \" else \"▶ \") + item.toString())")
	cc.indent--
	cc.line("}")
	cc.line("androidx.compose.animation.AnimatedVisibility(visible = sectionExpanded) {")
	cc.indent++
	if len(vn.Children) > 0 {
		for _, child := range vn.Children {
			cc.renderNode(child)
		}
	} else {
		cc.line("Text(\"Content\")")
	}
	cc.indent--
	cc.line("}")
	cc.indent--
	cc.line("}")
	cc.indent--
	cc.line("}")
}

func (cc *composeContext) renderSplitview(vn *ast.VisualNode) {
	cc.line("Row(modifier = Modifier.fillMaxWidth()) {")
	cc.indent++
	if len(vn.Children) >= 2 {
		cc.line("Box(modifier = Modifier.weight(1f)) {")
		cc.indent++
		cc.renderNode(vn.Children[0])
		cc.indent--
		cc.line("}")
		cc.line("VerticalDivider()")
		cc.line("Box(modifier = Modifier.weight(1f)) {")
		cc.indent++
		cc.renderNode(vn.Children[1])
		cc.indent--
		cc.line("}")
	} else if len(vn.Children) == 1 {
		cc.renderNode(vn.Children[0])
	}
	cc.indent--
	cc.line("}")
}

func (cc *composeContext) renderTable(vn *ast.VisualNode) {
	columns := `listOf<Any>()`
	if v, ok := vn.Props["columns"]; ok {
		columns = exprToKtValue(v, cc.ec)
	}
	rows := `listOf<Any>()`
	if v, ok := vn.Props["rows"]; ok {
		rows = exprToKtValue(v, cc.ec)
	}
	cc.line("Column {")
	cc.indent++
	cc.line("// Header")
	cc.line("Row {")
	cc.indent++
	cc.line("%s.forEach { col -> Text(col.toString(), modifier = Modifier.weight(1f)) }", columns)
	cc.indent--
	cc.line("}")
	cc.line("HorizontalDivider()")
	cc.line("// Rows")
	cc.line("%s.forEach { row ->", rows)
	cc.indent++
	cc.line("Row {")
	cc.indent++
	cc.line("if (row is List<*>) { row.forEach { cell -> Text(cell.toString(), modifier = Modifier.weight(1f)) } }")
	cc.indent--
	cc.line("}")
	cc.indent--
	cc.line("}")
	cc.indent--
	cc.line("}")
}

func (cc *composeContext) renderTree(vn *ast.VisualNode) {
	items := `listOf<Any>()`
	if v, ok := vn.Props["items"]; ok {
		items = exprToKtValue(v, cc.ec)
	}
	cc.line("Column {")
	cc.indent++
	cc.line("%s.forEach { item -> Text(\"▶ \" + item.toString()) }", items)
	cc.indent--
	cc.line("}")
}

func (cc *composeContext) renderAvatar(vn *ast.VisualNode) {
	initials := `""`
	if v, ok := vn.Props["initials"]; ok {
		initials = exprToKtValue(v, cc.ec)
	} else if v, ok := vn.Props["alt"]; ok {
		initials = exprToKtValue(v, cc.ec)
	}
	cc.line("Box(\n%smodifier = Modifier.size(40.dp).clip(androidx.compose.foundation.shape.CircleShape).background(MaterialTheme.colorScheme.primaryContainer),\n%scontentAlignment = Alignment.Center\n%s) {",
		strings.Repeat("    ", cc.indent+1),
		strings.Repeat("    ", cc.indent+1),
		strings.Repeat("    ", cc.indent))
	cc.indent++
	cc.line("Text(%s)", initials)
	cc.indent--
	cc.line("}")
}

func (cc *composeContext) renderPullrefresh(vn *ast.VisualNode) {
	cc.line("// PullToRefresh: rendering children directly")
	for _, child := range vn.Children {
		cc.renderNode(child)
	}
}

func (cc *composeContext) renderToast(vn *ast.VisualNode) {
	message := `""`
	if v, ok := vn.Props["message"]; ok {
		message = exprToKtValue(v, cc.ec)
	}
	visible := "false"
	if v, ok := vn.Props["visible"]; ok {
		visible = exprToKtValue(v, cc.ec)
	}
	cc.line("if (%s) {", visible)
	cc.indent++
	cc.line("Snackbar { Text(%s) }", message)
	cc.indent--
	cc.line("}")
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
	cc.line("%s(%s)", fnName, strings.Join(args, ", "))
}

func (cc *composeContext) getGap(vn *ast.VisualNode) string {
	for _, m := range []map[string]ast.Expr{vn.StyleBlock, vn.StyleAttrs} {
		if m == nil {
			continue
		}
		if gapExpr, ok := m["gap"]; ok {
			if gapExpr.Literal != nil {
				switch v := gapExpr.Literal.(type) {
				case int:
					return fmt.Sprintf("%d.dp", v)
				case float64:
					return fmt.Sprintf("%d.dp", int(v))
				}
			}
			if gapExpr.SNGL != nil {
				return cc.ec.translateExpr(gapExpr.SNGL) + ".dp"
			}
		}
	}
	return ""
}
