package http

import (
	"fmt"
	"html"
	"strings"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/internal/htmlutil"
)

// renderResult holds the generated Go code and any server-state actions collected.
type renderResult struct {
	code    string
	actions []codegen.HTTPAction
}

// renderWindowHTML generates Go code (fmt.Fprint statements) that writes
// the HTML body for a window. The returned result includes the Go source code
// and any collected server-state form actions.
func renderWindowHTML(doc *ast.Document, win *ast.Window, info *analysisResult, lang codegen.LangTranslator) renderResult {
	// Build a custom scope: state fields become local vars (no "m." prefix),
	// extern funcs stay as extern funcs (namespace-qualified).
	scope := &codegen.ExprScope{
		ModelFields:    make(map[string]bool),
		ComputedFields: make(map[string]bool),
		FuncNames:      make(map[string]bool),
		ExternFuncs:    make(map[string]bool),
		ExternVars:     make(map[string]bool),
		LocalVars:      make(map[string]bool),
		NeededHelpers:  make(map[string]bool),
	}
	// All data fields are local vars in the handler, not model fields.
	for name := range info.ModelFields {
		scope.LocalVars[name] = true
	}
	for name := range info.ComputedFields {
		scope.LocalVars[name] = true
	}
	for name := range info.FuncNames {
		scope.FuncNames[name] = true
	}
	// Put extern func names from go:// imports in scope.
	for ns, decls := range doc.NativeImports {
		if decls == nil {
			continue
		}
		for _, d := range decls.Data {
			scope.ExternFuncs[ns+"."+d.Name] = true
		}
	}

	r := &renderer{
		doc:      doc,
		info:     info,
		lang:     lang,
		scope:    scope,
		clientJS: newClientJSState(info, doc),
	}

	var b strings.Builder
	for _, child := range win.Children {
		r.renderNode(&b, child, 1)
	}

	// Emit client-side JS for client-only state (if any).
	if r.clientJS != nil {
		if script := r.clientJS.emitScript(); script != "" {
			fmt.Fprintf(&b, "\tfmt.Fprint(w, `%s`)\n", script)
		}
	}

	code := b.String()

	// Inject hidden params (e.g., *http.Request) into go:// function calls.
	code = injectHiddenParams(code, doc)

	return renderResult{
		code:    code,
		actions: r.actions,
	}
}

type renderer struct {
	doc      *ast.Document
	info     *analysisResult
	lang     codegen.LangTranslator
	scope    *codegen.ExprScope
	clientJS *clientJSState
	nextID   int
	actions  []codegen.HTTPAction // collected server-state form actions
}

func (r *renderer) allocID() string {
	id := fmt.Sprintf("s%d", r.nextID)
	r.nextID++
	return id
}

// renderNode generates Go fmt.Fprint statements for a visual node.
func (r *renderer) renderNode(b *strings.Builder, vn *ast.VisualNode, depth int) {
	indent := strings.Repeat("\t", depth)

	// Handle for-loops.
	if vn.For != nil {
		r.renderForLoop(b, vn, depth)
		return
	}

	// Handle if-conditionals.
	if vn.If != nil {
		r.renderIfBlock(b, vn, depth)
		return
	}

	// Dispatch to component-specific rendering.
	switch vn.Component {
	case "vbox":
		r.renderBox(b, vn, depth, "column")
	case "hbox":
		r.renderBox(b, vn, depth, "row")
	case "text":
		r.renderText(b, vn, depth)
	case "button":
		r.renderButton(b, vn, depth)
	case "input":
		r.renderInput(b, vn, depth)
	case "checkbox":
		r.renderCheckbox(b, vn, depth)
	case "image":
		r.renderImage(b, vn, depth)
	case "link":
		r.renderLink(b, vn, depth)
	case "spacer":
		fmt.Fprintf(b, "%sfmt.Fprint(w, `<div style=\"flex:1\"></div>`)\n", indent)
	case "divider":
		fmt.Fprintf(b, "%sfmt.Fprint(w, `<hr>`)\n", indent)
	case "slot":
		// Slot expansion handled at component inline time.
	default:
		// Try user-defined component, fall back to raw HTML element.
		if comp := r.doc.FindComponent(vn.Component); comp != nil {
			r.renderUserComponent(b, vn, comp, depth)
		} else {
			r.renderRawElement(b, vn, depth)
		}
	}
}

func (r *renderer) renderBox(b *strings.Builder, vn *ast.VisualNode, depth int, direction string) {
	indent := strings.Repeat("\t", depth)
	style := r.buildStyle(vn)
	style = htmlutil.AppendCSS(style, "display", "flex")
	style = htmlutil.AppendCSS(style, "flex-direction", direction)
	r.writeOpen(b, indent, "div", style)
	for _, child := range vn.Children {
		r.renderNode(b, child, depth)
	}
	fmt.Fprintf(b, "%sfmt.Fprint(w, `</div>`)\n", indent)
}

func (r *renderer) renderText(b *strings.Builder, vn *ast.VisualNode, depth int) {
	indent := strings.Repeat("\t", depth)
	style := r.buildStyle(vn)
	r.writeOpen(b, indent, "span", style)
	if v, ok := vn.Props["value"]; ok {
		r.writeExprEscaped(b, indent, v)
	}
	fmt.Fprintf(b, "%sfmt.Fprint(w, `</span>`)\n", indent)
}

func (r *renderer) renderButton(b *strings.Builder, vn *ast.VisualNode, depth int) {
	indent := strings.Repeat("\t", depth)
	style := r.buildStyle(vn)
	disabled := r.staticBool(vn.Props, "disabled")

	clickExpr, hasClick := vn.Events["click"]
	isClientClick := hasClick && r.clientJS != nil && r.clientJS.isClientMutation(clickExpr)
	isServerClick := hasClick && !isClientClick && clickExpr.SNGL != nil

	// Server-state click: wrap button in a form.
	if isServerClick {
		actionName := fmt.Sprintf("action%d", len(r.actions))
		r.actions = append(r.actions, codegen.HTTPAction{
			Name: actionName,
			Expr: clickExpr,
		})
		fmt.Fprintf(b, "%sfmt.Fprint(w, `<form method=\"POST\"><input type=\"hidden\" name=\"action\" value=\"%s\">`)\n", indent, actionName)
	}

	// Client-state click: assign ID for JS handler.
	var idAttr string
	if isClientClick {
		id := r.allocID()
		idAttr = fmt.Sprintf(` id="%s"`, id)
		r.clientJS.addClickHandler(id, clickExpr)
	}

	extra := idAttr
	if disabled {
		extra += ` disabled`
	}
	// Server forms use type=submit; client buttons use type=button.
	if isServerClick {
		extra += ` type="submit"`
	}
	if style != "" {
		fmt.Fprintf(b, "%sfmt.Fprint(w, `<button style=%q%s>`)\n", indent, style, extra)
	} else {
		fmt.Fprintf(b, "%sfmt.Fprint(w, `<button%s>`)\n", indent, extra)
	}
	if v, ok := vn.Props["text"]; ok {
		fmt.Fprintf(b, "%sfmt.Fprint(w, `<span>`)\n", indent)
		r.writeExprEscaped(b, indent, v)
		fmt.Fprintf(b, "%sfmt.Fprint(w, `</span>`)\n", indent)
	}
	for _, child := range vn.Children {
		r.renderNode(b, child, depth)
	}
	fmt.Fprintf(b, "%sfmt.Fprint(w, `</button>`)\n", indent)

	if isServerClick {
		fmt.Fprintf(b, "%sfmt.Fprint(w, `</form>`)\n", indent)
	}
}

func (r *renderer) renderInput(b *strings.Builder, vn *ast.VisualNode, depth int) {
	indent := strings.Repeat("\t", depth)
	attrs := r.collectAttrs(vn, "type", "placeholder", "value", "disabled", "readonly")
	fmt.Fprintf(b, "%sfmt.Fprint(w, `<input%s>`)\n", indent, attrs)
}

func (r *renderer) renderCheckbox(b *strings.Builder, vn *ast.VisualNode, depth int) {
	indent := strings.Repeat("\t", depth)
	checked := r.staticBool(vn.Props, "checked")
	disabled := r.staticBool(vn.Props, "disabled")
	label := r.staticString(vn.Props, "label")
	checkedAttr := ""
	if checked {
		checkedAttr = " checked"
	}
	disabledAttr := ""
	if disabled {
		disabledAttr = " disabled"
	}
	fmt.Fprintf(b, "%sfmt.Fprint(w, `<label style=\"display:inline-flex;align-items:center;gap:4px\">`)\n", indent)
	fmt.Fprintf(b, "%sfmt.Fprint(w, `<input type=\"checkbox\"%s%s>`)\n", indent, checkedAttr, disabledAttr)
	if label != "" {
		fmt.Fprintf(b, "%sfmt.Fprint(w, `<span>%s</span>`)\n", indent, html.EscapeString(label))
	}
	fmt.Fprintf(b, "%sfmt.Fprint(w, `</label>`)\n", indent)
}

func (r *renderer) renderImage(b *strings.Builder, vn *ast.VisualNode, depth int) {
	indent := strings.Repeat("\t", depth)
	attrs := r.collectAttrs(vn, "src", "alt")
	style := r.buildStyle(vn)
	if style != "" {
		attrs += fmt.Sprintf(` style=%q`, style)
	}
	fmt.Fprintf(b, "%sfmt.Fprint(w, `<img%s>`)\n", indent, attrs)
}

func (r *renderer) renderLink(b *strings.Builder, vn *ast.VisualNode, depth int) {
	indent := strings.Repeat("\t", depth)
	href := r.staticString(vn.Props, "href")
	text := r.staticString(vn.Props, "text")
	fmt.Fprintf(b, "%sfmt.Fprint(w, `<a href=%q>`)\n", indent, href)
	if text != "" {
		fmt.Fprintf(b, "%sfmt.Fprint(w, `<span>%s</span>`)\n", indent, html.EscapeString(text))
	}
	for _, child := range vn.Children {
		r.renderNode(b, child, depth)
	}
	fmt.Fprintf(b, "%sfmt.Fprint(w, `</a>`)\n", indent)
}

func (r *renderer) renderForLoop(b *strings.Builder, vn *ast.VisualNode, depth int) {
	indent := strings.Repeat("\t", depth)
	iterVar := vn.For.Variable
	indexVar := vn.For.IndexVar
	if indexVar == "" {
		indexVar = "index"
	}
	iterableExpr := r.exprToGo(vn.For.Iterable)
	fmt.Fprintf(b, "%sfor %s, %s := range %s {\n", indent, indexVar, iterVar, iterableExpr)
	// Temporarily register loop vars as local.
	r.scope.LocalVars[iterVar] = true
	r.scope.LocalVars[indexVar] = true
	for _, child := range vn.Children {
		r.renderNode(b, child, depth+1)
	}
	delete(r.scope.LocalVars, iterVar)
	delete(r.scope.LocalVars, indexVar)
	fmt.Fprintf(b, "%s}\n", indent)
}

func (r *renderer) renderIfBlock(b *strings.Builder, vn *ast.VisualNode, depth int) {
	indent := strings.Repeat("\t", depth)

	// Client-only conditional: render always with display:none, toggle via JS.
	if r.clientJS != nil && r.clientJS.isClientOnly(*vn.If) {
		id := r.allocID()
		r.clientJS.addIfUpdater(id, *vn.If)
		// Render the node without the if, but with the ID and hidden initially.
		stripped := *vn
		stripped.If = nil
		// Emit with id and initial display:none.
		fmt.Fprintf(b, "%s// client-state conditional: %s\n", indent, id)
		r.renderNodeWithID(b, &stripped, depth, id, true)
		return
	}

	// Server-state conditional: use Go if statement.
	condExpr := r.exprToGo(*vn.If)
	fmt.Fprintf(b, "%sif %s {\n", indent, condExpr)
	stripped := *vn
	stripped.If = nil
	r.renderNode(b, &stripped, depth+1)
	fmt.Fprintf(b, "%s}\n", indent)
}

// renderNodeWithID wraps a node in a div with an ID, optionally hidden.
func (r *renderer) renderNodeWithID(b *strings.Builder, vn *ast.VisualNode, depth int, id string, hidden bool) {
	indent := strings.Repeat("\t", depth)
	display := ""
	if hidden {
		display = ` style="display:none"`
	}
	fmt.Fprintf(b, "%sfmt.Fprint(w, `<div id=\"%s\"%s>`)\n", indent, id, display)
	r.renderNode(b, vn, depth)
	fmt.Fprintf(b, "%sfmt.Fprint(w, `</div>`)\n", indent)
}

func (r *renderer) renderUserComponent(b *strings.Builder, vn *ast.VisualNode, comp *ast.Component, depth int) {
	// Save and restore scope.
	savedLocal := make(map[string]bool)
	for k, v := range r.scope.LocalVars {
		savedLocal[k] = v
	}

	// Register params as locals.
	for _, p := range comp.Params {
		r.scope.LocalVars[p.Name] = true
	}

	// Inline the component body.
	body := comp.Body
	if platformBody, ok := comp.PlatformBodies["http"]; ok {
		body = platformBody
	} else if platformBody, ok := comp.PlatformBodies["html"]; ok {
		body = platformBody
	}
	for _, child := range body {
		r.renderNode(b, child, depth)
	}

	// Restore scope.
	r.scope.LocalVars = savedLocal
}

func (r *renderer) renderRawElement(b *strings.Builder, vn *ast.VisualNode, depth int) {
	indent := strings.Repeat("\t", depth)
	tag := vn.Component
	// Strip namespace prefix (e.g., "html.div" → "div").
	if i := strings.LastIndex(tag, "."); i >= 0 {
		tag = tag[i+1:]
	}

	style := r.buildStyle(vn)
	attrs := ""
	for name := range vn.Props {
		if name == "style" || name == "textContent" || name == "innerHTML" {
			continue
		}
		val := r.staticString(vn.Props, name)
		if val != "" {
			attrs += fmt.Sprintf(` %s=%q`, name, val)
		}
	}
	if style != "" {
		attrs += fmt.Sprintf(` style=%q`, style)
	}

	selfClosing := htmlutil.IsSelfClosing(tag)
	if selfClosing {
		fmt.Fprintf(b, "%sfmt.Fprint(w, `<%s%s>`)\n", indent, tag, attrs)
		return
	}

	fmt.Fprintf(b, "%sfmt.Fprint(w, `<%s%s>`)\n", indent, tag, attrs)

	// Handle textContent prop.
	if v, ok := vn.Props["textContent"]; ok {
		r.writeExprEscaped(b, indent, v)
	}

	for _, child := range vn.Children {
		r.renderNode(b, child, depth)
	}
	fmt.Fprintf(b, "%sfmt.Fprint(w, `</%s>`)\n", indent, tag)
}

// writeExprEscaped writes a Go expression that outputs an HTML-escaped value.
func (r *renderer) writeExprEscaped(b *strings.Builder, indent string, expr ast.Expr) {
	if expr.Literal != nil {
		// Static literal — escape at compile time.
		s := fmt.Sprint(expr.Literal)
		fmt.Fprintf(b, "%sfmt.Fprint(w, `%s`)\n", indent, html.EscapeString(s))
		return
	}
	if expr.SNGL != nil {
		goExpr := r.lang.TranslateExpr(expr.SNGL, r.scope)
		fmt.Fprintf(b, "%sfmt.Fprint(w, html.EscapeString(fmt.Sprint(%s)))\n", indent, goExpr)
		return
	}
}

func (r *renderer) exprToGo(expr ast.Expr) string {
	if expr.SNGL != nil {
		return r.lang.TranslateExpr(expr.SNGL, r.scope)
	}
	if expr.Literal != nil {
		return r.lang.TranslateLiteral(expr)
	}
	return `""`
}

// injectHiddenParams scans generated Go code for calls to go:// imported
// functions that have a hidden first parameter (*http.Request or context.Context)
// and injects "r" or "r.Context()" as the first argument.
func injectHiddenParams(code string, doc *ast.Document) string {
	for ns, decls := range doc.NativeImports {
		if decls == nil {
			continue
		}
		for _, d := range decls.Data {
			if d.HiddenParam == "" {
				continue
			}
			injectedArg := hiddenParamArgs(d.HiddenParam)
			call := ns + "." + d.Name + "("
			for {
				idx := strings.Index(code, call)
				if idx < 0 {
					break
				}
				afterCall := idx + len(call)
				if afterCall < len(code) && code[afterCall] == ')' {
					code = code[:afterCall] + injectedArg + code[afterCall:]
				} else {
					code = code[:afterCall] + injectedArg + ", " + code[afterCall:]
				}
			}
		}
	}
	return code
}

// hiddenParamArgs returns the Go argument expression for injecting hidden params.
func hiddenParamArgs(hidden string) string {
	parts := strings.Split(hidden, ",")
	var args []string
	for _, p := range parts {
		switch p {
		case "*http.Request":
			args = append(args, "r")
		case "http.ResponseWriter":
			args = append(args, "w")
		case "context.Context":
			args = append(args, "r.Context()")
		}
	}
	return strings.Join(args, ", ")
}

func (r *renderer) writeOpen(b *strings.Builder, indent, tag, style string) {
	if style != "" {
		fmt.Fprintf(b, "%sfmt.Fprint(w, `<%s style=%q>`)\n", indent, tag, style)
	} else {
		fmt.Fprintf(b, "%sfmt.Fprint(w, `<%s>`)\n", indent, tag)
	}
}

func (r *renderer) buildStyle(vn *ast.VisualNode) string {
	return htmlutil.BuildCSSStyle(vn)
}

func (r *renderer) staticString(props map[string]ast.Expr, key string) string {
	return htmlutil.StaticString(props, key)
}

func (r *renderer) staticBool(props map[string]ast.Expr, key string) bool {
	return htmlutil.StaticBool(props, key)
}

func (r *renderer) collectAttrs(vn *ast.VisualNode, keys ...string) string {
	var attrs string
	for _, key := range keys {
		val := r.staticString(vn.Props, key)
		if val != "" {
			attrs += fmt.Sprintf(` %s=%q`, key, val)
		}
	}
	style := r.buildStyle(vn)
	if style != "" {
		attrs += fmt.Sprintf(` style=%q`, style)
	}
	return attrs
}

