package html

import (
	"fmt"
	"maps"
	"net/http"
	"sort"
	"strings"
	"time"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/internal/compiler"
	"git.duckfam.us/jonathan/sngl/internal/testrunner"
	"git.duckfam.us/jonathan/sngl/internal/testutil/webtest"
)

func init() {
	codegen.RegisterPlatform(&Generator{})
}

// Generator implements codegen.PlatformGenerator for HTML output.
type Generator struct{}

func (g *Generator) Platform() string         { return "html" }
func (g *Generator) SupportedLangs() []string { return []string{"js"} }

func (g *Generator) Generate(req *codegen.Request) (*codegen.Response, error) {
	if req.Lang.Lang() != "js" {
		return &codegen.Response{Error: fmt.Sprintf("html: unsupported lang %q", req.Lang.Lang())}, nil
	}

	gen := newHTMLGen(req.Doc, req.Lang, req.Options)
	src := gen.generate()

	return &codegen.Response{
		Files: []*codegen.OutputFile{
			{Name: "index.html", Content: []byte(src)},
		},
	}, nil
}

func (g *Generator) RunTests(doc *ast.Document, lang codegen.LangTranslator, tests []*ast.TestDef) ([]*testrunner.Result, error) {
	// Group tests by component
	compTests := make(map[string][]*ast.TestDef)
	for _, td := range tests {
		compTests[td.Component] = append(compTests[td.Component], td)
	}

	var results []*testrunner.Result
	for compName, tests := range compTests {
		compDoc := compiler.PromoteComponent(doc, compName)
		if compDoc == nil {
			continue
		}

		resp, err := g.Generate(&codegen.Request{
			Doc:     compDoc,
			Lang:    lang,
			Options: map[string]string{"test": "true"},
		})
		if err != nil {
			return nil, fmt.Errorf("generate %q: %w", compName, err)
		}
		if resp.Error != "" {
			return nil, fmt.Errorf("generate %q: %s", compName, resp.Error)
		}
		html := string(resp.Files[0].Content)

		mux := http.NewServeMux()
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/html")
			w.Write([]byte(html))
		})
		engine := webtest.New(mux)

		for _, td := range tests {
			result := g.runSingleTest(engine, compDoc, lang, compName, td)
			results = append(results, result)
		}

		engine.Close()
	}
	return results, nil
}

func (g *Generator) runSingleTest(engine *webtest.Engine, doc *ast.Document, lang codegen.LangTranslator, compName string, td *ast.TestDef) *testrunner.Result {
	start := time.Now()
	desc := td.Desc
	if desc == "" {
		desc = "unnamed"
	}

	result := &testrunner.Result{
		Component: compName,
		Desc:      desc,
		Passed:    true,
	}

	browser, err := engine.StartHeadless(1280, 720)
	if err != nil {
		result.Passed = false
		result.Error = fmt.Sprintf("browser start: %v", err)
		result.Duration = time.Since(start)
		return result
	}
	defer browser.Close()

	if err := browser.NavigateRaw(engine.BaseURL() + "/"); err != nil {
		result.Passed = false
		result.Error = fmt.Sprintf("navigate: %v", err)
		result.Duration = time.Since(start)
		return result
	}

	runner := compiler.NewCDPRunner(browser.Page(), doc, lang)
	if err := runner.InjectHelpers(); err != nil {
		result.Passed = false
		result.Error = fmt.Sprintf("inject helpers: %v", err)
		result.Duration = time.Since(start)
		return result
	}

	if err := runner.ExecTest(td.Body); err != nil {
		result.Passed = false
		result.Error = err.Error()
		result.Duration = time.Since(start)
		return result
	}

	for _, sub := range td.Subtests {
		child := g.runSubtest(runner, compName, sub)
		result.Children = append(result.Children, child)
		if !child.Passed {
			result.Passed = false
		}
	}

	result.Duration = time.Since(start)
	return result
}

func (g *Generator) runSubtest(runner *compiler.CDPRunner, compName string, td *ast.TestDef) *testrunner.Result {
	start := time.Now()
	desc := td.Desc
	if desc == "" {
		desc = "unnamed"
	}

	result := &testrunner.Result{
		Component: compName,
		Desc:      desc,
		Passed:    true,
	}

	if err := runner.SaveState(); err != nil {
		result.Passed = false
		result.Error = fmt.Sprintf("save state: %v", err)
		result.Duration = time.Since(start)
		return result
	}
	defer runner.RestoreState()

	if err := runner.ExecTest(td.Body); err != nil {
		result.Passed = false
		result.Error = err.Error()
		result.Duration = time.Since(start)
		return result
	}

	for _, sub := range td.Subtests {
		child := g.runSubtest(runner, compName, sub)
		result.Children = append(result.Children, child)
		if !child.Passed {
			result.Passed = false
		}
	}

	result.Duration = time.Since(start)
	return result
}

// htmlGen holds all state for generating a single HTML file.
type htmlGen struct {
	doc  *ast.Document
	lang codegen.LangTranslator

	// Analysis results
	modelFields    map[string]bool
	computedFields map[string]bool
	structFields   map[string][]string // struct name → ordered field names

	// Computed dependency tracking
	computedDeps map[string]map[string]bool // computed name → set of root state fields it reads

	// Element ID counter
	nextID int

	// Collected update functions: id → []updateFunc
	updates []updateFunc

	// Collected event handlers
	handlers []eventHandler

	// Component param constants (name → JS expression)
	componentParams []componentParam

	// For building the scope
	scope *codegen.ExprScope

	// Preview mode: add data-sngl-line/col attributes, ensure all elements have IDs
	preview bool

	// Test mode: emit data-key, id, class attributes for test element lookup
	testMode bool

	// Component invocation counter for unique param names
	componentInvocations int
}

type componentParam struct {
	name  string
	value string
}

type updateFunc struct {
	funcName string // e.g., "$u_0_text"
	body     string // JS function body
	deps     map[string]bool
}

type eventHandler struct {
	elemID  string
	event   string // "click", "input", "change"
	body    string // JS statements
	mutated map[string]bool
}

func newHTMLGen(doc *ast.Document, lang codegen.LangTranslator, opts map[string]string) *htmlGen {
	g := &htmlGen{
		doc:            doc,
		lang:           lang,
		preview:        opts["preview"] == "true",
		testMode:       opts["test"] == "true",
		modelFields:    make(map[string]bool),
		computedFields: make(map[string]bool),
		structFields:   make(map[string][]string),
		computedDeps:   make(map[string]map[string]bool),
	}

	for _, d := range doc.Data {
		g.modelFields[d.Name] = true
	}
	for _, c := range doc.Computeds {
		g.modelFields[c.Name] = true
		g.computedFields[c.Name] = true
	}
	for _, sd := range doc.Structs {
		var fields []string
		for _, f := range sd.Fields {
			fields = append(fields, f.Name)
		}
		g.structFields[sd.Name] = fields
	}

	localVars := make(map[string]bool)
	for _, c := range doc.Consts {
		localVars[c.Name] = true
	}
	g.scope = &codegen.ExprScope{
		ModelFields:    g.modelFields,
		ComputedFields: g.computedFields,
		LocalVars:      localVars,
	}

	// Compute dependency info for computeds
	for _, c := range doc.Computeds {
		if c.Expr.SNGL != nil {
			g.computedDeps[c.Name] = extractDeps(c.Expr.SNGL, g.modelFields)
		}
	}

	return g
}

func (g *htmlGen) allocID() string {
	id := fmt.Sprintf("$%d", g.nextID)
	g.nextID++
	return id
}

func (g *htmlGen) generate() string {
	var b strings.Builder

	b.WriteString("<!DOCTYPE html>\n<html><head>\n")
	b.WriteString("  <meta charset=\"utf-8\">\n")
	b.WriteString("  <style>\n")
	b.WriteString("    * { margin: 0; padding: 0; box-sizing: border-box; }\n")
	b.WriteString("    body { font-family: system-ui, sans-serif; }\n")
	b.WriteString("  </style>\n")
	b.WriteString("</head><body>\n\n")

	// Render static HTML body
	if g.doc.App != nil {
		for _, child := range g.doc.App.Children {
			g.renderStaticNode(&b, child, 0)
		}
	}

	b.WriteString("\n<script>\n")
	g.emitScript(&b)
	b.WriteString("</script>\n")
	b.WriteString("</body></html>\n")

	return b.String()
}

// renderStaticNode renders a VisualNode as static HTML.
func (g *htmlGen) renderStaticNode(b *strings.Builder, vn *ast.VisualNode, depth int) {
	indent := strings.Repeat("  ", depth)

	// Handle for-loops: render an empty container div, generate JS updater
	if vn.For != nil {
		id := g.allocID()
		style := g.buildCSSStyle(vn)
		// For containers in boxes get flex layout from parent, but the container
		// itself might have styles from the node
		if vn.Component == "vbox" {
			style = appendCSS(style, "display", "flex")
			style = appendCSS(style, "flex-direction", "column")
		} else if vn.Component == "hbox" {
			style = appendCSS(style, "display", "flex")
			style = appendCSS(style, "flex-direction", "row")
		}
		g.writeOpenTag(b, "div", id, style, vn, depth, vn.Pos)
		fmt.Fprintf(b, "%s</div>\n", indent)
		g.addForUpdater(id, vn)
		return
	}

	switch vn.Component {
	case "vbox":
		g.renderStaticBox(b, vn, depth, true)
	case "hbox":
		g.renderStaticBox(b, vn, depth, false)
	case "text":
		g.renderStaticText(b, vn, depth)
	case "button":
		g.renderStaticButton(b, vn, depth)
	case "input":
		g.renderStaticInput(b, vn, depth)
	case "checkbox":
		g.renderStaticCheckbox(b, vn, depth)
	case "image":
		g.renderStaticImage(b, vn, depth)
	case "spacer":
		if g.preview {
			id := g.allocID()
			fmt.Fprintf(b, "%s<div id=\"%s\" style=\"flex:1\"%s></div>\n", indent, id, g.previewAttrs(vn.Pos))
		} else {
			fmt.Fprintf(b, "%s<div style=\"flex:1\"></div>\n", indent)
		}
	case "scroll":
		id := ""
		style := g.buildCSSStyle(vn)
		style = appendCSS(style, "overflow", "auto")
		if g.nodeIsReactive(vn) {
			id = g.allocID()
		}
		g.writeOpenTag(b, "div", id, style, vn, depth, vn.Pos)
		for _, child := range vn.Children {
			g.renderStaticNode(b, child, depth+1)
		}
		fmt.Fprintf(b, "%s</div>\n", indent)
	case "stack":
		style := g.buildCSSStyle(vn)
		style = appendCSS(style, "position", "relative")
		g.writeOpenTag(b, "div", "", style, vn, depth, vn.Pos)
		for _, child := range vn.Children {
			g.renderStaticNode(b, child, depth+1)
		}
		fmt.Fprintf(b, "%s</div>\n", indent)
	default:
		// User-defined component — inline at call site
		g.renderStaticUserComponent(b, vn, depth)
	}
}

func (g *htmlGen) renderStaticBox(b *strings.Builder, vn *ast.VisualNode, depth int, vertical bool) {
	indent := strings.Repeat("  ", depth)
	style := g.buildCSSStyle(vn)
	style = appendCSS(style, "display", "flex")
	if vertical {
		style = appendCSS(style, "flex-direction", "column")
	} else {
		style = appendCSS(style, "flex-direction", "row")
	}

	reactive := g.nodeIsReactive(vn)
	id := ""
	if reactive {
		id = g.allocID()
	}

	// Handle if
	ifDisplay := ""
	if vn.If != nil {
		if !g.evalStaticBool(vn.If) {
			ifDisplay = "display:none"
		}
		if id == "" {
			id = g.allocID()
		}
		g.addIfUpdater(id, *vn.If)
	}

	if ifDisplay != "" {
		style = appendCSS(style, "display", "none")
	}

	g.writeOpenTag(b, "div", id, style, vn, depth, vn.Pos)
	for _, child := range vn.Children {
		g.renderStaticNode(b, child, depth+1)
	}
	fmt.Fprintf(b, "%s</div>\n", indent)
}

func (g *htmlGen) renderStaticText(b *strings.Builder, vn *ast.VisualNode, depth int) {
	style := g.buildCSSStyle(vn)
	val := g.evalStaticString(vn.Props, "value")
	reactive := g.propIsReactive(vn.Props, "value") || vn.If != nil
	id := ""
	if reactive {
		id = g.allocID()
	}

	// In test mode, id/class need an element ID for updaters
	if g.testMode && id == "" && g.vnHasUserAttrs(vn) {
		id = g.allocID()
	}

	ifDisplay := ""
	if vn.If != nil {
		if !g.evalStaticBool(vn.If) {
			ifDisplay = "none"
		}
		if id == "" {
			id = g.allocID()
		}
		g.addIfUpdater(id, *vn.If)
	}
	if ifDisplay != "" {
		style = appendCSS(style, "display", "none")
	}

	g.writeOpenTag(b, "span", id, style, vn, depth, vn.Pos)
	b.WriteString(escapeHTML(val))
	b.WriteString("</span>\n")

	if g.propIsReactive(vn.Props, "value") {
		g.addTextUpdater(id, vn.Props["value"])
	}
	if g.testMode && id != "" {
		g.addUserAttrUpdaters(id, vn)
	}
}

func (g *htmlGen) renderStaticButton(b *strings.Builder, vn *ast.VisualNode, depth int) {
	style := g.buildCSSStyle(vn)
	text := g.evalStaticString(vn.Props, "text")
	reactive := g.propIsReactive(vn.Props, "text") || vn.If != nil || len(vn.Events) > 0 || g.propIsReactive(vn.Props, "disabled")
	id := ""
	if reactive {
		id = g.allocID()
	}

	ifDisplay := ""
	if vn.If != nil {
		if !g.evalStaticBool(vn.If) {
			ifDisplay = "none"
		}
		if id == "" {
			id = g.allocID()
		}
		g.addIfUpdater(id, *vn.If)
	}
	if ifDisplay != "" {
		style = appendCSS(style, "display", "none")
	}

	disabled := ""
	if v, ok := vn.Props["disabled"]; ok {
		if g.evalStaticBool(&v) {
			disabled = " disabled"
		}
	}

	if g.preview && id == "" {
		id = g.allocID()
	}
	indent := strings.Repeat("  ", depth)
	if id != "" {
		fmt.Fprintf(b, "%s<button id=\"%s\"", indent, id)
	} else {
		fmt.Fprintf(b, "%s<button", indent)
	}
	if style != "" {
		fmt.Fprintf(b, " style=\"%s\"", style)
	}
	g.writeDataKey(b, vn.Key)
	g.writeUserAttrs(b, id, vn)
	b.WriteString(g.previewAttrs(vn.Pos))
	fmt.Fprintf(b, "%s>%s</button>\n", disabled, escapeHTML(text))

	if g.propIsReactive(vn.Props, "text") {
		g.addTextContentUpdater(id, vn.Props["text"])
	}
	if g.propIsReactive(vn.Props, "disabled") {
		g.addDisabledUpdater(id, vn.Props["disabled"])
	}

	// Event handlers
	if clickEvt, ok := vn.Events["click"]; ok {
		if clickEvt.SNGL != nil {
			g.addClickHandler(id, clickEvt)
		}
	}
}

func (g *htmlGen) renderStaticInput(b *strings.Builder, vn *ast.VisualNode, depth int) {
	style := g.buildCSSStyle(vn)
	id := g.allocID() // inputs are always reactive

	placeholder := ""
	if v, ok := vn.Props["placeholder"]; ok {
		if s, ok := v.Literal.(string); ok {
			placeholder = s
		}
	}

	inputType := "text"
	if v, ok := vn.Props["type"]; ok {
		if s, ok := v.Literal.(string); ok {
			inputType = s
		}
	}

	value := g.evalStaticString(vn.Props, "value")

	indent := strings.Repeat("  ", depth)
	fmt.Fprintf(b, "%s<input id=\"%s\"", indent, id)
	if style != "" {
		fmt.Fprintf(b, " style=\"%s\"", style)
	}
	if inputType != "text" {
		fmt.Fprintf(b, " type=\"%s\"", inputType)
	}
	if placeholder != "" {
		fmt.Fprintf(b, " placeholder=\"%s\"", escapeHTML(placeholder))
	}
	fmt.Fprintf(b, " value=\"%s\"", escapeHTML(value))
	g.writeDataKey(b, vn.Key)
	g.writeUserAttrs(b, id, vn)
	b.WriteString(g.previewAttrs(vn.Pos))
	b.WriteString(" />\n")

	// Add value sync updater if the input is bound to state via set()
	if inputEvt, ok := vn.Events["input"]; ok {
		if inputEvt.SNGL != nil {
			// Extract the set() target to determine the JS expression for the bound value
			if target, ok := extractSetTarget(inputEvt.SNGL); ok {
				jsExpr := g.lang.TranslateExpr(target, g.scope)
				root := findMutationRoot(target)
				name := fmt.Sprintf("$u_%s_val", id[1:])
				g.updates = append(g.updates, updateFunc{
					funcName: name,
					body:     fmt.Sprintf("%s.value = %s;", id, jsExpr),
					deps:     map[string]bool{root: true},
				})
			}
		}
	}

	// Input event handler
	if inputEvt, ok := vn.Events["input"]; ok {
		if inputEvt.SNGL != nil {
			g.addInputHandler(id, inputEvt)
		}
	}
}

func (g *htmlGen) renderStaticCheckbox(b *strings.Builder, vn *ast.VisualNode, depth int) {
	style := g.buildCSSStyle(vn)
	id := g.allocID()

	checked := false
	if v, ok := vn.Props["checked"]; ok {
		checked = g.evalStaticBool(&v)
	}
	label := g.evalStaticString(vn.Props, "label")

	indent := strings.Repeat("  ", depth)
	fmt.Fprintf(b, "%s<label id=\"%s\"", indent, id)
	if style != "" {
		fmt.Fprintf(b, " style=\"%s\"", style)
	}
	g.writeDataKey(b, vn.Key)
	g.writeUserAttrs(b, id, vn)
	b.WriteString(g.previewAttrs(vn.Pos))
	b.WriteString(">")
	if checked {
		b.WriteString("<input type=\"checkbox\" checked />")
	} else {
		b.WriteString("<input type=\"checkbox\" />")
	}
	fmt.Fprintf(b, " %s</label>\n", escapeHTML(label))

	if changeEvt, ok := vn.Events["change"]; ok {
		if changeEvt.SNGL != nil {
			g.addChangeHandler(id, changeEvt)
		}
	}
}

func (g *htmlGen) renderStaticImage(b *strings.Builder, vn *ast.VisualNode, depth int) {
	style := g.buildCSSStyle(vn)
	alt := "image"
	if v, ok := vn.Props["alt"]; ok {
		if s, ok := v.Literal.(string); ok {
			alt = s
		}
	}
	src := ""
	if v, ok := vn.Props["src"]; ok {
		if s, ok := v.Literal.(string); ok {
			src = s
		}
	}
	if v, ok := vn.Props["fit"]; ok {
		if s, ok := v.Literal.(string); ok {
			style = appendCSS(style, "object-fit", s)
		}
	}

	if g.preview {
		id := g.allocID()
		indent := strings.Repeat("  ", depth)
		fmt.Fprintf(b, "%s<img id=\"%s\"", indent, id)
		if src != "" {
			fmt.Fprintf(b, " src=\"%s\"", escapeHTML(src))
		}
		fmt.Fprintf(b, " alt=\"%s\"", escapeHTML(alt))
		if style != "" {
			fmt.Fprintf(b, " style=\"%s\"", style)
		}
		g.writeDataKey(b, vn.Key)
		g.writeUserAttrs(b, id, vn)
		b.WriteString(g.previewAttrs(vn.Pos))
		b.WriteString(" />\n")
		return
	}
	indent := strings.Repeat("  ", depth)
	fmt.Fprintf(b, "%s<img", indent)
	if src != "" {
		fmt.Fprintf(b, " src=\"%s\"", escapeHTML(src))
	}
	fmt.Fprintf(b, " alt=\"%s\"", escapeHTML(alt))
	if style != "" {
		fmt.Fprintf(b, " style=\"%s\"", style)
	}
	g.writeDataKey(b, vn.Key)
	g.writeUserAttrs(b, "", vn)
	b.WriteString(" />\n")
}

func (g *htmlGen) renderStaticUserComponent(b *strings.Builder, vn *ast.VisualNode, depth int) {
	// Find the component definition
	var comp *ast.Component
	for _, c := range g.doc.Components {
		if c.Name == vn.Component {
			comp = c
			break
		}
	}
	if comp == nil {
		return
	}

	g.componentInvocations++
	suffix := fmt.Sprintf("_%d", g.componentInvocations)

	// Save and set local vars for parameters
	savedLocals := make(map[string]bool)
	maps.Copy(savedLocals, g.scope.LocalVars)
	savedRenames := g.scope.Renames

	renames := make(map[string]string)
	if savedRenames != nil {
		maps.Copy(renames, savedRenames)
	}

	// Record component param values for JS constants with unique names
	for _, p := range comp.Params {
		uniqueName := p.Name + suffix
		g.scope.LocalVars[p.Name] = true
		renames[p.Name] = uniqueName

		// Resolve param value from call-site props or default
		var jsVal string
		if expr, ok := vn.Props[p.Name]; ok {
			jsVal = g.exprToJS(expr)
		} else {
			jsVal = g.literalToJS(p.Default)
		}
		g.componentParams = append(g.componentParams, componentParam{
			name:  uniqueName,
			value: jsVal,
		})
	}
	// Handle component computeds: rename and register as local
	for _, c := range comp.Computeds {
		uniqueName := c.Name + suffix
		g.scope.LocalVars[c.Name] = true
		renames[c.Name] = uniqueName
	}
	g.scope.Renames = renames

	// Emit computed functions with renamed references
	for _, c := range comp.Computeds {
		uniqueName := c.Name + suffix
		body := g.exprToJS(c.Expr)
		g.componentParams = append(g.componentParams, componentParam{
			name:  uniqueName,
			value: body,
		})
	}

	// Inline the component body at the call site
	for _, child := range comp.Body {
		g.renderStaticNode(b, child, depth)
	}

	// Restore locals
	g.scope.LocalVars = savedLocals
	g.scope.Renames = savedRenames
}

// emitScript writes the <script> block content.
func (g *htmlGen) emitScript(b *strings.Builder) {
	// State initialization
	b.WriteString("// State\nlet state = {")
	var stateFields []string
	for _, d := range g.doc.Data {
		if d.Extern || d.IsFunc {
			continue
		}
		val := g.literalToJS(d.Init)
		stateFields = append(stateFields, d.Name+": "+val)
	}
	b.WriteString(strings.Join(stateFields, ", "))
	b.WriteString("};\n\n")

	// Struct constructors
	for _, sd := range g.doc.Structs {
		var params []string
		var body []string
		for _, f := range sd.Fields {
			params = append(params, f.Name)
			body = append(body, f.Name)
		}
		fmt.Fprintf(b, "function %s(%s) { return {%s}; }\n",
			sd.Name, strings.Join(params, ", "), strings.Join(body, ", "))
	}
	if len(g.doc.Structs) > 0 {
		b.WriteString("\n")
	}

	// Computed functions
	for _, c := range g.doc.Computeds {
		body := g.exprToJS(c.Expr)
		fmt.Fprintf(b, "function $%s() { return %s; }\n", c.Name, body)
	}
	if len(g.doc.Computeds) > 0 {
		b.WriteString("\n")
	}

	// Getters
	for _, d := range g.doc.Data {
		if d.Extern || d.IsFunc {
			continue
		}
		fmt.Fprintf(b, "function $get_%s() { return state.%s; }\n", d.Name, d.Name)
	}
	if len(stateFields) > 0 {
		b.WriteString("\n")
	}

	// Setters
	for _, d := range g.doc.Data {
		if d.Extern || d.IsFunc {
			continue
		}
		fmt.Fprintf(b, "function $set_%s(v) {\n", d.Name)
		fmt.Fprintf(b, "  state.%s = v;\n", d.Name)
		// Call affected updaters
		mutated := map[string]bool{d.Name: true}
		for _, u := range g.findAffectedUpdaters(mutated) {
			fmt.Fprintf(b, "  %s();\n", u.funcName)
		}
		// Fire trigger callback
		if d.Trigger != "" {
			cbField := "$on_" + d.Name + "_changed"
			fmt.Fprintf(b, "  if (state.%s) state.%s(v);\n", cbField, cbField)
		}
		b.WriteString("}\n")
	}
	if len(stateFields) > 0 {
		b.WriteString("\n")
	}

	// Trigger registration
	for _, d := range g.doc.Data {
		if d.Extern || d.IsFunc || d.Trigger == "" {
			continue
		}
		cbField := "$on_" + d.Name + "_changed"
		fmt.Fprintf(b, "function %s(fn) { state.%s = fn; }\n", d.Trigger, cbField)
	}
	b.WriteString("\n")

	// Constants
	for _, c := range g.doc.Consts {
		val := g.literalToJS(c.Init)
		fmt.Fprintf(b, "const %s = %s;\n", c.Name, val)
	}
	if len(g.doc.Consts) > 0 {
		b.WriteString("\n")
	}

	// Helper functions
	b.WriteString("function String(v) { return \"\" + v; }\n\n")

	// Component param constants
	for _, cp := range g.componentParams {
		fmt.Fprintf(b, "const %s = %s;\n", cp.name, cp.value)
	}
	if len(g.componentParams) > 0 {
		b.WriteString("\n")
	}

	// Element references
	refs := g.collectReferencedIDs()
	for _, id := range refs {
		fmt.Fprintf(b, "const %s = document.getElementById(\"%s\");\n", id, id)
	}
	if len(refs) > 0 {
		b.WriteString("\n")
	}

	// Update functions
	for _, u := range g.updates {
		fmt.Fprintf(b, "function %s() { %s }\n", u.funcName, u.body)
	}
	if len(g.updates) > 0 {
		b.WriteString("\n")
	}

	// Event handlers with static dispatch
	for _, h := range g.handlers {
		// Build the list of update functions to call based on mutated fields
		updaters := g.findAffectedUpdaters(h.mutated)
		var lines []string
		lines = append(lines, h.body)
		for _, u := range updaters {
			lines = append(lines, u.funcName+"();")
		}
		body := strings.Join(lines, "\n  ")
		if h.event == "input" {
			fmt.Fprintf(b, "%s.addEventListener(\"%s\", function(e) {\n  %s\n});\n", h.elemID, h.event, body)
		} else {
			fmt.Fprintf(b, "%s.addEventListener(\"%s\", function() {\n  %s\n});\n", h.elemID, h.event, body)
		}
	}

	// Initial sync: call all update functions once to set DOM from initial state
	if len(g.updates) > 0 {
		b.WriteString("\n// Initial sync\n")
		for _, u := range g.updates {
			fmt.Fprintf(b, "%s();\n", u.funcName)
		}
	}

	// Preview mode: export update function names for state-preserving hot reload
	if g.preview && len(g.updates) > 0 {
		b.WriteString("\nconst __sngl_updates = [")
		for i, u := range g.updates {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(u.funcName)
		}
		b.WriteString("];\n")
	}
}

func (g *htmlGen) collectReferencedIDs() []string {
	seen := make(map[string]bool)
	var ids []string

	for _, u := range g.updates {
		// Extract the element ID from the func name: $u_N_xxx → $N
		parts := strings.SplitN(u.funcName, "_", 3)
		if len(parts) >= 2 {
			elemID := "$" + parts[1]
			if !seen[elemID] {
				seen[elemID] = true
				ids = append(ids, elemID)
			}
		}
	}
	for _, h := range g.handlers {
		if !seen[h.elemID] {
			seen[h.elemID] = true
			ids = append(ids, h.elemID)
		}
	}

	sort.Strings(ids)
	return ids
}

func (g *htmlGen) findAffectedUpdaters(mutatedFields map[string]bool) []updateFunc {
	if len(mutatedFields) == 0 {
		return nil
	}

	// Expand mutated fields through computed deps
	expanded := make(map[string]bool)
	for f := range mutatedFields {
		expanded[f] = true
	}
	// If a mutated field is read by a computed, anything that depends on
	// that computed is also affected
	for compName, compDeps := range g.computedDeps {
		for dep := range compDeps {
			if mutatedFields[dep] {
				expanded[compName] = true
			}
		}
	}

	var result []updateFunc
	for _, u := range g.updates {
		for dep := range u.deps {
			if expanded[dep] {
				result = append(result, u)
				break
			}
		}
	}
	return result
}

// addTextUpdater adds an updater that sets el.textContent from an expression.
func (g *htmlGen) addTextUpdater(elemID string, expr ast.Expr) {
	jsExpr := g.exprToJS(expr)
	deps := g.exprDeps(expr)
	name := fmt.Sprintf("$u_%s_text", elemID[1:])
	g.updates = append(g.updates, updateFunc{
		funcName: name,
		body:     fmt.Sprintf("%s.textContent = %s;", elemID, jsExpr),
		deps:     deps,
	})
}

func (g *htmlGen) addTextContentUpdater(elemID string, expr ast.Expr) {
	jsExpr := g.exprToJS(expr)
	deps := g.exprDeps(expr)
	name := fmt.Sprintf("$u_%s_text", elemID[1:])
	g.updates = append(g.updates, updateFunc{
		funcName: name,
		body:     fmt.Sprintf("%s.textContent = %s;", elemID, jsExpr),
		deps:     deps,
	})
}

func (g *htmlGen) addDisabledUpdater(elemID string, expr ast.Expr) {
	jsExpr := g.exprToJS(expr)
	deps := g.exprDeps(expr)
	name := fmt.Sprintf("$u_%s_disabled", elemID[1:])
	g.updates = append(g.updates, updateFunc{
		funcName: name,
		body:     fmt.Sprintf("%s.disabled = %s;", elemID, jsExpr),
		deps:     deps,
	})
}

func (g *htmlGen) addIfUpdater(elemID string, expr ast.Expr) {
	jsExpr := g.exprToJS(expr)
	deps := g.exprDeps(expr)
	name := fmt.Sprintf("$u_%s_if", elemID[1:])
	g.updates = append(g.updates, updateFunc{
		funcName: name,
		body:     fmt.Sprintf("%s.style.display = %s ? \"\" : \"none\";", elemID, jsExpr),
		deps:     deps,
	})
}

func (g *htmlGen) addForUpdater(elemID string, vn *ast.VisualNode) {
	if vn.For == nil {
		return
	}
	iterVar := vn.For.Variable
	indexVar := vn.For.IndexVar
	if indexVar == "" {
		indexVar = "index"
	}

	iterableJS := g.exprToJS(vn.For.Iterable)
	deps := g.exprDeps(vn.For.Iterable)

	// Generate the inner HTML creation code
	var innerBuf strings.Builder
	g.emitForLoopBody(&innerBuf, vn, iterVar, indexVar, elemID)

	name := fmt.Sprintf("$u_%s_list", elemID[1:])
	body := fmt.Sprintf(`%s.innerHTML = "";
  for (let %s = 0; %s < %s.length; %s++) {
    const %s = %s[%s];
%s  }`,
		elemID,
		indexVar, indexVar, iterableJS, indexVar,
		iterVar, iterableJS, indexVar,
		innerBuf.String())

	g.updates = append(g.updates, updateFunc{
		funcName: name,
		body:     body,
		deps:     deps,
	})
}

func (g *htmlGen) emitForLoopBody(b *strings.Builder, vn *ast.VisualNode, iterVar, indexVar, containerID string) {
	// Save local vars
	savedLocals := make(map[string]bool)
	maps.Copy(savedLocals, g.scope.LocalVars)
	g.scope.LocalVars[iterVar] = true
	g.scope.LocalVars[indexVar] = true
	defer func() { g.scope.LocalVars = savedLocals }()

	listFuncName := fmt.Sprintf("$u_%s_list", containerID[1:])

	// Generate based on component type
	switch vn.Component {
	case "checkbox":
		checked := "false"
		if v, ok := vn.Props["checked"]; ok {
			checked = g.exprToJS(v)
		}
		label := `""`
		if v, ok := vn.Props["label"]; ok {
			label = g.exprToJS(v)
		}
		style := g.buildCSSStyle(vn)

		fmt.Fprintf(b, "    const row = document.createElement(\"label\");\n")
		g.emitForLoopDataKey(b, "row", vn)
		style = appendCSS(style, "display", "block")
		fmt.Fprintf(b, "    row.style.cssText = %q;\n", style)
		fmt.Fprintf(b, "    const cb = document.createElement(\"input\");\n")
		fmt.Fprintf(b, "    cb.type = \"checkbox\";\n")
		fmt.Fprintf(b, "    cb.checked = %s;\n", checked)

		// Change handler
		if changeEvt, ok := vn.Events["change"]; ok {
			if changeEvt.SNGL != nil {
				stmts := g.lang.TranslateMutation(changeEvt.SNGL, g.scope)
				mutated := extractMutatedFields(changeEvt.SNGL)
				var handlerLines []string
				for _, s := range stmts {
					handlerLines = append(handlerLines, s+";")
				}
				handlerLines = append(handlerLines, listFuncName+"();")
				// Also call updaters for other fields affected
				for _, u := range g.findAffectedUpdaters(mutated) {
					if u.funcName != listFuncName {
						handlerLines = append(handlerLines, u.funcName+"();")
					}
				}
				fmt.Fprintf(b, "    cb.addEventListener(\"change\", function() {\n")
				for _, line := range handlerLines {
					fmt.Fprintf(b, "      %s\n", line)
				}
				fmt.Fprintf(b, "    });\n")
			}
		}

		fmt.Fprintf(b, "    row.appendChild(cb);\n")
		fmt.Fprintf(b, "    row.appendChild(document.createTextNode(\" \" + %s));\n", label)
		fmt.Fprintf(b, "    %s.appendChild(row);\n", containerID)

	case "text":
		tag := "span"
		fmt.Fprintf(b, "    const el = document.createElement(%q);\n", tag)
		g.emitForLoopDataKey(b, "el", vn)
		val := `""`
		if v, ok := vn.Props["value"]; ok {
			val = g.exprToJS(v)
		}
		fmt.Fprintf(b, "    el.textContent = %s;\n", val)
		fmt.Fprintf(b, "    %s.appendChild(el);\n", containerID)

	default:
		// Generic: create a div for each item
		fmt.Fprintf(b, "    const el = document.createElement(\"div\");\n")
		g.emitForLoopDataKey(b, "el", vn)
		fmt.Fprintf(b, "    el.textContent = String(%s);\n", iterVar)
		fmt.Fprintf(b, "    %s.appendChild(el);\n", containerID)
	}
}

// emitForLoopDataKey emits a data-key attribute for elements inside for loops.
func (g *htmlGen) emitForLoopDataKey(b *strings.Builder, elVar string, vn *ast.VisualNode) {
	if !g.testMode && !g.preview {
		return
	}
	if vn.Key == nil {
		return
	}
	keyJS := g.exprToJS(*vn.Key)
	fmt.Fprintf(b, "    %s.setAttribute('data-key', %s);\n", elVar, keyJS)
}

func (g *htmlGen) addClickHandler(elemID string, expr ast.Expr) {
	if expr.SNGL == nil {
		return
	}
	stmts := g.lang.TranslateMutation(expr.SNGL, g.scope)
	mutated := extractMutatedFields(expr.SNGL)
	var lines []string
	for _, s := range stmts {
		lines = append(lines, s+";")
	}
	g.handlers = append(g.handlers, eventHandler{
		elemID:  elemID,
		event:   "click",
		body:    strings.Join(lines, "\n  "),
		mutated: mutated,
	})
}

func (g *htmlGen) addInputHandler(elemID string, expr ast.Expr) {
	if expr.SNGL == nil {
		return
	}
	// Set event var for input handlers
	savedEvent := g.scope.EventVar
	g.scope.EventVar = "e.target"
	stmts := g.lang.TranslateMutation(expr.SNGL, g.scope)
	g.scope.EventVar = savedEvent
	mutated := extractMutatedFields(expr.SNGL)
	var lines []string
	for _, s := range stmts {
		lines = append(lines, s+";")
	}
	g.handlers = append(g.handlers, eventHandler{
		elemID:  elemID,
		event:   "input",
		body:    strings.Join(lines, "\n  "),
		mutated: mutated,
	})
}

func (g *htmlGen) addChangeHandler(elemID string, expr ast.Expr) {
	if expr.SNGL == nil {
		return
	}
	stmts := g.lang.TranslateMutation(expr.SNGL, g.scope)
	mutated := extractMutatedFields(expr.SNGL)
	var lines []string
	for _, s := range stmts {
		lines = append(lines, s+";")
	}
	g.handlers = append(g.handlers, eventHandler{
		elemID:  elemID,
		event:   "change",
		body:    strings.Join(lines, "\n  "),
		mutated: mutated,
	})
}

// CSS building

func (g *htmlGen) buildCSSStyle(vn *ast.VisualNode) string {
	merged := make(map[string]ast.Expr)
	maps.Copy(merged, vn.StyleBlock)
	maps.Copy(merged, vn.StyleAttrs)

	var parts []string
	for prop, expr := range merged {
		if css := stylePropToCSS(prop, expr); css != "" {
			parts = append(parts, css)
		}
	}

	sort.Strings(parts)
	return strings.Join(parts, ";")
}

func stylePropToCSS(prop string, expr ast.Expr) string {
	val := exprToStaticValue(expr)

	switch prop {
	case "padding":
		return fmt.Sprintf("padding:%spx", val)
	case "paddingTop":
		return fmt.Sprintf("padding-top:%spx", val)
	case "paddingRight":
		return fmt.Sprintf("padding-right:%spx", val)
	case "paddingBottom":
		return fmt.Sprintf("padding-bottom:%spx", val)
	case "paddingLeft":
		return fmt.Sprintf("padding-left:%spx", val)
	case "paddingX":
		return fmt.Sprintf("padding-left:%spx;padding-right:%spx", val, val)
	case "paddingY":
		return fmt.Sprintf("padding-top:%spx;padding-bottom:%spx", val, val)
	case "margin":
		return fmt.Sprintf("margin:%spx", val)
	case "marginTop":
		return fmt.Sprintf("margin-top:%spx", val)
	case "marginRight":
		return fmt.Sprintf("margin-right:%spx", val)
	case "marginBottom":
		return fmt.Sprintf("margin-bottom:%spx", val)
	case "marginLeft":
		return fmt.Sprintf("margin-left:%spx", val)
	case "marginX":
		return fmt.Sprintf("margin-left:%spx;margin-right:%spx", val, val)
	case "marginY":
		return fmt.Sprintf("margin-top:%spx;margin-bottom:%spx", val, val)
	case "width":
		return fmt.Sprintf("width:%spx", val)
	case "height":
		return fmt.Sprintf("height:%spx", val)
	case "maxWidth":
		return fmt.Sprintf("max-width:%spx", val)
	case "maxHeight":
		return fmt.Sprintf("max-height:%spx", val)
	case "gap":
		return fmt.Sprintf("gap:%spx", val)
	case "color":
		return fmt.Sprintf("color:%s", val)
	case "background":
		return fmt.Sprintf("background-color:%s", val)
	case "fontSize":
		return fmt.Sprintf("font-size:%spx", val)
	case "fontWeight":
		return fmt.Sprintf("font-weight:%s", val)
	case "fontStyle":
		return fmt.Sprintf("font-style:%s", val)
	case "textAlign":
		return fmt.Sprintf("text-align:%s", val)
	case "borderWidth":
		return fmt.Sprintf("border-width:%spx;border-style:solid", val)
	case "borderRadius":
		return fmt.Sprintf("border-radius:%spx", val)
	case "borderColor":
		return fmt.Sprintf("border-color:%s", val)
	case "opacity":
		return fmt.Sprintf("opacity:%s", val)
	case "maxLines":
		return fmt.Sprintf("-webkit-line-clamp:%s;overflow:hidden;display:-webkit-box;-webkit-box-orient:vertical", val)
	}
	return ""
}

func exprToStaticValue(expr ast.Expr) string {
	if expr.Literal != nil {
		switch v := expr.Literal.(type) {
		case string:
			return v
		case int:
			return fmt.Sprintf("%d", v)
		case float64:
			return fmt.Sprintf("%v", v)
		case bool:
			if v {
				return "true"
			}
			return "false"
		}
	}
	return ""
}

func appendCSS(existing, prop, value string) string {
	entry := prop + ":" + value
	if existing == "" {
		return entry
	}
	return existing + ";" + entry
}

// Expression evaluation helpers

func (g *htmlGen) exprToJS(expr ast.Expr) string {
	if expr.SNGL != nil {
		return g.lang.TranslateExpr(expr.SNGL, g.scope)
	}
	if expr.Literal != nil {
		return g.lang.TranslateLiteral(expr)
	}
	return `""`
}

func (g *htmlGen) literalToJS(expr ast.Expr) string {
	if expr.Literal != nil {
		switch v := expr.Literal.(type) {
		case string:
			return fmt.Sprintf("%q", v)
		case int:
			return fmt.Sprintf("%d", v)
		case float64:
			return fmt.Sprintf("%v", v)
		case bool:
			if v {
				return "true"
			}
			return "false"
		}
	}
	// SNGL init expression (e.g., struct literal)
	if expr.SNGL != nil {
		return g.exprToJS(expr)
	}
	if strings.HasPrefix(expr.TypeHint, "list:") || strings.HasPrefix(expr.TypeHint, "[]") {
		return "[]"
	}
	return `""`
}

func (g *htmlGen) evalStaticString(props map[string]ast.Expr, key string) string {
	if props == nil {
		return ""
	}
	v, ok := props[key]
	if !ok {
		return ""
	}
	if v.Literal != nil {
		if s, ok := v.Literal.(string); ok {
			return s
		}
		return fmt.Sprintf("%v", v.Literal)
	}
	// For SNGL expressions, evaluate with initial state
	if v.SNGL != nil {
		return g.evalInitialString(v)
	}
	return ""
}

func (g *htmlGen) evalInitialString(_ ast.Expr) string {
	// For the initial render, expressions can't be statically evaluated.
	// Return empty and let JS update on load.
	return ""
}

func (g *htmlGen) evalStaticBool(expr *ast.Expr) bool {
	if expr == nil {
		return true
	}
	if expr.Literal != nil {
		if v, ok := expr.Literal.(bool); ok {
			return v
		}
	}
	// Expression: assume true for initial render
	return true
}

func (g *htmlGen) propIsReactive(props map[string]ast.Expr, key string) bool {
	if props == nil {
		return false
	}
	v, ok := props[key]
	if !ok {
		return false
	}
	return v.SNGL != nil
}

func (g *htmlGen) nodeIsReactive(vn *ast.VisualNode) bool {
	if vn.If != nil {
		return true
	}
	if vn.For != nil {
		return true
	}
	if len(vn.Events) > 0 {
		return true
	}
	for _, v := range vn.Props {
		if v.SNGL != nil {
			return true
		}
	}
	return false
}

// Dependency extraction

func (g *htmlGen) exprDeps(expr ast.Expr) map[string]bool {
	if expr.SNGL != nil {
		deps := extractDeps(expr.SNGL, g.modelFields)
		// Expand through computeds
		return g.expandDeps(deps)
	}
	return nil
}

func (g *htmlGen) expandDeps(deps map[string]bool) map[string]bool {
	result := make(map[string]bool)
	for d := range deps {
		result[d] = true
		// If d is a computed, add its transitive deps
		if g.computedFields[d] {
			if compDeps, ok := g.computedDeps[d]; ok {
				for cd := range compDeps {
					result[cd] = true
				}
			}
		}
	}
	return result
}

func extractDeps(e ast.Node, modelFields map[string]bool) map[string]bool {
	deps := make(map[string]bool)
	walkDeps(e, modelFields, deps)
	return deps
}

func walkDeps(e ast.Node, modelFields map[string]bool, deps map[string]bool) {
	if e == nil {
		return
	}
	switch n := e.(type) {
	case *ast.IdentExpr:
		if modelFields[n.Name] {
			deps[n.Name] = true
		}
	case *ast.SelectExpr:
		root := findRootIdent(n.Operand)
		if root != "" && modelFields[root] {
			deps[root] = true
		}
	case *ast.CallExpr:
		for _, arg := range n.Args {
			walkDeps(arg, modelFields, deps)
		}
	case *ast.MethodExpr:
		walkDeps(n.Receiver, modelFields, deps)
		for _, arg := range n.Args {
			walkDeps(arg, modelFields, deps)
		}
	case *ast.BinaryExpr:
		walkDeps(n.Left, modelFields, deps)
		walkDeps(n.Right, modelFields, deps)
	case *ast.UnaryExpr:
		walkDeps(n.Operand, modelFields, deps)
	case *ast.TernaryExpr:
		walkDeps(n.Cond, modelFields, deps)
		walkDeps(n.Then, modelFields, deps)
		walkDeps(n.Else, modelFields, deps)
	case *ast.IndexExpr:
		walkDeps(n.Operand, modelFields, deps)
		walkDeps(n.Index, modelFields, deps)
	case *ast.ListExpr:
		for _, el := range n.Elements {
			walkDeps(el, modelFields, deps)
		}
	case *ast.StructExpr:
		for _, field := range n.Fields {
			walkDeps(field.Value, modelFields, deps)
		}
	case *ast.InterpolationExpr:
		for _, part := range n.Parts {
			walkDeps(part, modelFields, deps)
		}
	case *ast.AssignStmt:
		walkDeps(n.Target, modelFields, deps)
		walkDeps(n.Value, modelFields, deps)
	case *ast.ToggleStmt:
		walkDeps(n.Target, modelFields, deps)
	case *ast.EmitStmt:
		for _, arg := range n.Args {
			walkDeps(arg, modelFields, deps)
		}
	case *ast.StmtBlock:
		for _, stmt := range n.Stmts {
			walkDeps(stmt, modelFields, deps)
		}
	}
}

func findRootIdent(e ast.Node) string {
	if e == nil {
		return ""
	}
	switch n := e.(type) {
	case *ast.IdentExpr:
		return n.Name
	case *ast.SelectExpr:
		return findRootIdent(n.Operand)
	case *ast.IndexExpr:
		return findRootIdent(n.Operand)
	case *ast.MethodExpr:
		return findRootIdent(n.Receiver)
	}
	return ""
}

// extractSetTarget finds the first argument of an AssignStmt, which is the target being assigned.
func extractSetTarget(e ast.Node) (ast.Node, bool) {
	if e == nil {
		return nil, false
	}
	switch n := e.(type) {
	case *ast.AssignStmt:
		return n.Target, true
	case *ast.StmtBlock:
		if len(n.Stmts) > 0 {
			return extractSetTarget(n.Stmts[0])
		}
	}
	return nil, false
}

func extractMutatedFields(e ast.Node) map[string]bool {
	fields := make(map[string]bool)
	if e == nil {
		return fields
	}
	switch n := e.(type) {
	case *ast.StmtBlock:
		for _, stmt := range n.Stmts {
			maps.Copy(fields, extractMutatedFields(stmt))
		}
	case *ast.AssignStmt:
		root := findMutationRoot(n.Target)
		if root != "" {
			fields[root] = true
		}
	case *ast.ToggleStmt:
		root := findMutationRoot(n.Target)
		if root != "" {
			fields[root] = true
		}
	case *ast.CallExpr:
		// Function calls like append() etc — check first arg
		if len(n.Args) >= 1 {
			root := findMutationRoot(n.Args[0])
			if root != "" {
				fields[root] = true
			}
		}
	case *ast.MethodExpr:
		root := findMutationRoot(n.Receiver)
		if root != "" {
			fields[root] = true
		}
	}
	return fields
}

func findMutationRoot(e ast.Node) string {
	if e == nil {
		return ""
	}
	switch n := e.(type) {
	case *ast.IdentExpr:
		return n.Name
	case *ast.SelectExpr:
		return findMutationRoot(n.Operand)
	case *ast.IndexExpr:
		return findMutationRoot(n.Operand)
	}
	return ""
}

func (g *htmlGen) writeOpenTag(b *strings.Builder, tag, id, style string, vn *ast.VisualNode, depth int, pos ...ast.Pos) {
	if g.preview && id == "" {
		id = g.allocID()
	}
	indent := strings.Repeat("  ", depth)
	fmt.Fprintf(b, "%s<%s", indent, tag)
	if id != "" {
		fmt.Fprintf(b, " id=\"%s\"", id)
	}
	if style != "" {
		fmt.Fprintf(b, " style=\"%s\"", style)
	}
	if vn != nil {
		g.writeDataKey(b, vn.Key)
		g.writeUserAttrs(b, id, vn)
	}
	if g.preview && len(pos) > 0 && pos[0].IsValid() {
		fmt.Fprintf(b, " data-sngl-line=\"%d\" data-sngl-col=\"%d\"", pos[0].Line, pos[0].Column)
	}
	b.WriteString(">\n")
}

// writeDataKey emits a data-key="..." attribute for literal key values.
// Only emitted in test or preview mode.
func (g *htmlGen) writeDataKey(b *strings.Builder, key *ast.Expr) {
	if !g.testMode && !g.preview {
		return
	}
	if key == nil {
		return
	}
	if key.Literal != nil {
		if s, ok := key.Literal.(string); ok {
			fmt.Fprintf(b, " data-key=%q", s)
		}
	}
}

// vnHasUserAttrs returns true if the visual node has user-specified id or class.
func (g *htmlGen) vnHasUserAttrs(vn *ast.VisualNode) bool {
	return vn.ID != nil || vn.Class != nil
}

// addUserAttrUpdaters adds DOM updaters for user-specified id and class attributes.
func (g *htmlGen) addUserAttrUpdaters(elemID string, vn *ast.VisualNode) {
	if vn.ID != nil {
		jsExpr := g.exprToJS(*vn.ID)
		deps := g.exprDeps(*vn.ID)
		name := fmt.Sprintf("$u_%s_uid", elemID[1:])
		g.updates = append(g.updates, updateFunc{
			funcName: name,
			body:     fmt.Sprintf("%s.id = %s;", elemID, jsExpr),
			deps:     deps,
		})
	}
	if vn.Class != nil {
		jsExpr := g.exprToJS(*vn.Class)
		deps := g.exprDeps(*vn.Class)
		name := fmt.Sprintf("$u_%s_cls", elemID[1:])
		g.updates = append(g.updates, updateFunc{
			funcName: name,
			body:     fmt.Sprintf("%s.className = %s;", elemID, jsExpr),
			deps:     deps,
		})
	}
}

// writeUserAttrs emits user-specified id and class attributes in test mode.
// When there's an internal id, user id/class are set via updaters instead.
func (g *htmlGen) writeUserAttrs(b *strings.Builder, internalID string, vn *ast.VisualNode) {
	if !g.testMode {
		return
	}
	// Static class can always be emitted directly (no conflict with internal attrs)
	if vn.Class != nil && vn.Class.Literal != nil {
		if s, ok := vn.Class.Literal.(string); ok {
			fmt.Fprintf(b, " class=%q", s)
		}
	}
	// Static id: only emit directly when there's no internal id
	// (when there IS an internal id, the updater sets it after element refs are captured)
	if vn.ID != nil && vn.ID.Literal != nil && internalID == "" {
		if s, ok := vn.ID.Literal.(string); ok {
			fmt.Fprintf(b, " id=%q", s)
		}
	}
}

func (g *htmlGen) previewAttrs(pos ast.Pos) string {
	if !g.preview || !pos.IsValid() {
		return ""
	}
	return fmt.Sprintf(" data-sngl-line=\"%d\" data-sngl-col=\"%d\"", pos.Line, pos.Column)
}

func escapeHTML(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	s = strings.ReplaceAll(s, "\"", "&quot;")
	return s
}
