package html

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"html"
	"maps"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/internal/checker"
	"git.duckfam.us/jonathan/sngl/internal/htmlutil"
)

//go:embed html.sngl
var pkgSource string

func init() {
	codegen.RegisterPlatform(&Generator{})
}

// Generator implements codegen.PlatformGenerator for HTML output.
type Generator struct{}

func (g *Generator) Platform() string         { return "html" }
func (g *Generator) SupportedLangs() []string { return []string{"js"} }
func (g *Generator) PkgSource() string        { return pkgSource }

// ResolveAPI makes any identifier valid as an HTML element.
// This allows html.div, html.span, html.form, etc. to be used as components.
func (g *Generator) ResolveAPI(name string) *codegen.NativeDecls {
	return &codegen.NativeDecls{}
}

func (g *Generator) Generate(req *codegen.Request) (*codegen.Response, error) {
	if req.Lang.Lang() != "js" {
		return &codegen.Response{Error: fmt.Sprintf("html: unsupported lang %q", req.Lang.Lang())}, nil
	}

	var files []*codegen.OutputFile

	// Resolve stylesheet option: source path relative to project dir.
	stylesheetURL := ""
	if ssPath := req.Options["stylesheet"]; ssPath != "" {
		projectDir := req.Options["projectDir"]
		absPath := ssPath
		if projectDir != "" && !filepath.IsAbs(ssPath) {
			absPath = filepath.Join(projectDir, ssPath)
		}
		outName := "assets/" + filepath.Base(ssPath)
		stylesheetURL = "/" + outName
		data, err := os.ReadFile(absPath)
		if err == nil {
			files = append(files, codegen.BytesFile(outName, data))
		}
	}

	// Build WASM for imported packages that have runtime-used functions.
	var wasmLoaderHTML string
	wasmPkgs := collectWASMPackages(req.Doc)
	if len(wasmPkgs) > 0 {
		projectDir := req.Options["projectDir"]
		wasmExecAdded := false
		var loaderScripts []string

		for _, wp := range wasmPkgs {
			schemeLang := codegen.LookupLang(wp.namespace)
			if schemeLang == nil {
				for _, name := range codegen.Langs() {
					l := codegen.LookupLang(name)
					if _, ok := l.(codegen.WASMCompiler); ok {
						schemeLang = l
						break
					}
				}
			}
			wc, ok := schemeLang.(codegen.WASMCompiler)
			if !ok {
				continue
			}

			if !wasmExecAdded {
				wasmExecData, err := wc.WASMExecJS()
				if err == nil {
					files = append(files, codegen.BytesFile("assets/wasm_exec.js", wasmExecData))
					wasmExecAdded = true
				}
			}

			wasmBytes, err := wc.BuildWASM(projectDir, wp.importPath, wp.funcs)
			if err != nil {
				return &codegen.Response{Error: fmt.Sprintf("wasm build: %v", err)}, nil
			}
			wasmFile := "assets/" + wp.namespace + ".wasm"
			files = append(files, codegen.BytesFile(wasmFile, wasmBytes))
			loaderScripts = append(loaderScripts, fmt.Sprintf(
				`  const _go_%s = new Go();
  WebAssembly.instantiateStreaming(fetch("/%s"), _go_%s.importObject).then(r => { _go_%s.run(r.instance); });`,
				wp.namespace, wasmFile, wp.namespace, wp.namespace))
		}
		if len(loaderScripts) > 0 {
			wasmLoaderHTML = fmt.Sprintf(
				"  <script src=\"/assets/wasm_exec.js\"></script>\n  <script>\n  window.__sngl_externs = window.__sngl_externs || {};\n%s\n  </script>\n",
				strings.Join(loaderScripts, "\n"))
		}
	}
	_ = stylesheetURL // used by multi-window below

	// Single-page generation (no multi-window support in v2 yet).
	gen := newHTMLGen(req.Doc, req.Pkg, req.Lang, req.Options)
	gen.wasmLoader = wasmLoaderHTML
	src := codegen.Header("html", req.Source, "<!-- ", " -->") + gen.generate()
	files = append(files, codegen.BytesFile("index.html", []byte(src)))
	return &codegen.Response{Files: files}, nil
}

// staticPropString extracts a static string value from a props map.
func staticPropString(props map[string]ast.Expr, key string) string {
	if props == nil {
		return ""
	}
	e, ok := props[key]
	if !ok {
		return ""
	}
	if s, ok := exprLiteralString(e); ok {
		return s
	}
	return ""
}

// htmlGen holds all state for generating a single HTML file.
type htmlGen struct {
	doc  *ast.Document
	lang codegen.LangTranslator

	// v2: type-checked package and expression context
	pkg        *checker.Package
	translator codegen.ExprTranslator
	ctx        *codegen.ExprCtx

	// Analysis (shared)
	*codegen.CommonAnalysis
	dt *codegen.DepTracker

	// Element ID counter
	nextID int

	// Collected update functions: id → []updateFunc
	updates []updateFunc

	// Collected event handlers
	handlers []eventHandler

	// Collected timers
	timers []timerDef

	// Component param constants (name → JS expression)
	componentParams []componentParam

	// For building the scope
	scope *codegen.ExprScope

	// Page title for <title> tag in <head>
	title string

	// Stylesheet URL path to emit as <link> in <head>
	stylesheet string

	// Favicon URL path to emit as <link rel="icon"> in <head>
	favicon string

	// WASM loader HTML to inject in <head> (script tags for wasm_exec.js + instantiation)
	wasmLoader string

	// Preview mode: add data-sngl-line/col attributes, ensure all elements have IDs
	preview bool

	// Test mode: emit data-key, id, class attributes for test element lookup
	testMode bool

	// Component invocation counter for unique param names
	componentInvocations int

	// dataRenames maps original component var names to promoted unique names
	// during component inlining, so handler MutatedFields can be remapped.
	dataRenames map[string]string

	// Component nesting depth for recursion protection
	componentDepth int

	// Slot children: caller's children for abstract component body expansion
	slotChildren []ast.Stmt
}

type componentParam struct {
	name       string
	value      string
	staticOnly bool // true if value was fully consumed during static HTML render (skip JS emission)
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

type timerDef struct {
	index      int
	intervalMs int
	activeVar  string
	body       string
	mutated    map[string]bool
}

func newHTMLGen(doc *ast.Document, pkg *checker.Package, lang codegen.LangTranslator, opts map[string]string) *htmlGen {
	common := codegen.AnalyzeCommon(doc)

	g := &htmlGen{
		doc:            doc,
		pkg:            pkg,
		lang:           lang,
		CommonAnalysis: common,
		preview:        opts["preview"] == "true",
		testMode:       opts["test"] == "true",
	}

	// Build scope from common analysis, adding const names as local vars
	g.scope = common.Scope()
	for _, c := range docConsts(doc) {
		g.scope.LocalVars[c.Name] = true
	}

	g.dt = common.DepTracker()

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
	b.WriteString("  <meta name=\"viewport\" content=\"width=device-width, initial-scale=1\">\n")
	if g.title != "" {
		fmt.Fprintf(&b, "  <title>%s</title>\n", html.EscapeString(g.title))
	}
	if g.favicon != "" {
		fmt.Fprintf(&b, "  <link rel=\"icon\" href=\"%s\">\n", html.EscapeString(g.favicon))
	}
	if g.stylesheet != "" {
		fmt.Fprintf(&b, "  <link rel=\"stylesheet\" href=\"%s\">\n", html.EscapeString(g.stylesheet))
	}
	if g.stylesheet == "" {
		// Only emit default inline styles when no external stylesheet is specified.
		// Component-specific CSS is registered via CommonAnalysis.AddStyle()
		// during tree rendering and emitted here.
		b.WriteString("  <style>\n")
		b.WriteString("    * { margin: 0; padding: 0; box-sizing: border-box; }\n")
		b.WriteString("    body { font-family: system-ui, sans-serif; }\n")
		b.WriteString("  </style>\n")
	}
	if g.wasmLoader != "" {
		b.WriteString(g.wasmLoader)
	}
	b.WriteString("</head><body>\n\n")

	// Render static HTML body from the main component
	mainComp := findMainComponent(g.doc)
	if mainComp != nil {
		for _, s := range compBodyStmts(mainComp) {
			g.renderStaticStmt(&b, s, 0)
		}
	}

	// Emit component-registered CSS (populated during tree rendering above).
	if len(g.Styles) > 0 {
		b.WriteString("<style>\n")
		for _, css := range g.Styles {
			fmt.Fprintf(&b, "  %s\n", css)
		}
		b.WriteString("</style>\n")
	}

	// Only emit <script> if there's actual runtime JS to execute.
	var scriptBuf strings.Builder
	g.emitScript(&scriptBuf)
	script := scriptBuf.String()
	if strings.TrimSpace(script) != "" {
		b.WriteString("\n<script>\n")
		b.WriteString(script)
		b.WriteString("</script>\n")
	}
	b.WriteString("</body></html>\n")

	// Strip unreferenced IDs from HTML to reduce noise.
	result := b.String()
	referencedIDs := g.collectReferencedIDs()
	refSet := map[string]bool{}
	for _, id := range referencedIDs {
		refSet[id] = true
	}
	for i := 0; i < g.nextID; i++ {
		id := fmt.Sprintf("$%d", i)
		if !refSet[id] {
			result = strings.Replace(result, fmt.Sprintf(` id="%s"`, id), "", 1)
		}
	}

	return result
}

// renderStaticStmt dispatches a statement to the appropriate renderer.
// In v2, ForStmt and IfStmt are separate statement types, not fields on VisualNode.
func (g *htmlGen) renderStaticStmt(b *strings.Builder, s ast.Stmt, depth int) {
	switch n := s.(type) {
	case *ast.VisualNode:
		g.renderStaticNode(b, n, depth)
	case *ast.IfStmt:
		g.renderStaticIf(b, n, depth)
	case *ast.ForStmt:
		g.renderStaticFor(b, n, depth)
	}
}

// renderStaticIf renders a conditional block as a hidden div with an updater.
func (g *htmlGen) renderStaticIf(b *strings.Builder, stmt *ast.IfStmt, depth int) {
	indent := strings.Repeat("  ", depth)
	id := g.allocID()
	fmt.Fprintf(b, "%s<div id=\"%s\" style=\"display:none\">\n", indent, id)
	for _, s := range stmt.Body.Stmts {
		g.renderStaticStmt(b, s, depth+1)
	}
	fmt.Fprintf(b, "%s</div>\n", indent)
	g.addIfUpdater(id, stmt.Cond)

	if len(stmt.Else.Stmts) > 0 {
		elseID := g.allocID()
		fmt.Fprintf(b, "%s<div id=\"%s\">\n", indent, elseID)
		for _, s := range stmt.Else.Stmts {
			g.renderStaticStmt(b, s, depth+1)
		}
		fmt.Fprintf(b, "%s</div>\n", indent)
		g.addElseUpdater(elseID, stmt.Cond)
	}
}

// renderStaticFor renders a for-loop as an empty container with a JS updater.
func (g *htmlGen) renderStaticFor(b *strings.Builder, stmt *ast.ForStmt, depth int) {
	indent := strings.Repeat("  ", depth)
	id := g.allocID()
	fmt.Fprintf(b, "%s<div id=\"%s\"></div>\n", indent, id)
	g.addForStmtUpdater(id, stmt)

	if len(stmt.Else.Stmts) > 0 {
		elseID := g.allocID()
		fmt.Fprintf(b, "%s<div id=\"%s\">\n", indent, elseID)
		for _, s := range stmt.Else.Stmts {
			g.renderStaticStmt(b, s, depth+1)
		}
		fmt.Fprintf(b, "%s</div>\n", indent)
		g.addForElseStmtUpdater(id, elseID, stmt)
	}
}

// renderStaticNode renders a VisualNode as static HTML.
func (g *htmlGen) renderStaticNode(b *strings.Builder, vn *ast.VisualNode, depth int) {
	indent := strings.Repeat("  ", depth)
	_ = indent // used by generated template code below
	name := vnName(vn)

	switch name {
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
		style = htmlutil.AppendCSS(style, "overflow", "auto")
		if g.nodeIsReactive(vn) {
			id = g.allocID()
		}
		g.writeOpenTag(b, "div", id, style, vn, depth, vn.Pos)
		for _, s := range vnChildren(vn) {
			g.renderStaticStmt(b, s, depth+1)
		}
		fmt.Fprintf(b, "%s</div>\n", indent)
	case "stack":
		style := g.buildCSSStyle(vn)
		style = htmlutil.AppendCSS(style, "position", "relative")
		g.writeOpenTag(b, "div", "", style, vn, depth, vn.Pos)
		for _, s := range vnChildren(vn) {
			g.renderStaticStmt(b, s, depth+1)
		}
		fmt.Fprintf(b, "%s</div>\n", indent)

	// --- Tier 1: Core Input ---
	case "radio":
		g.renderStaticRadio(b, vn, depth)
	case "toggle":
		g.renderStaticToggle(b, vn, depth)
	case "select":
		g.renderStaticSelect(b, vn, depth)
	case "textarea":
		g.renderStaticTextarea(b, vn, depth)

	// --- Tier 2: Feedback & Navigation ---
	case "progress":
		style := g.buildCSSStyle(vn)
		value := g.evalStaticString(vnProps(vn), "value")
		maxVal := g.evalStaticString(vnProps(vn), "max")
		if maxVal == "" {
			maxVal = "1"
		}
		id := ""
		if g.nodeIsReactive(vn) || g.preview {
			id = g.allocID()
		}
		fmt.Fprintf(b, "%s<progress", indent)
		if id != "" {
			fmt.Fprintf(b, " id=\"%s\"", id)
		}
		if style != "" {
			fmt.Fprintf(b, " style=\"%s\"", style)
		}
		fmt.Fprintf(b, " value=\"%s\" max=\"%s\"", value, maxVal)
		g.writeUserAttrs(b, id, vn)
		b.WriteString(g.previewAttrs(vn.Pos))
		b.WriteString("></progress>\n")
		if id != "" {
			if valExpr := vnProp(vn, "value"); valExpr != nil {
				g.addAttrUpdater(id, "value", valExpr)
			}
		}
	case "spinner":
		g.CommonAnalysis.AddStyle("@keyframes sngl-spin { to { transform: rotate(360deg); } }")
		g.CommonAnalysis.AddStyle(".sngl-spinner { display: inline-block; width: 1em; height: 1em; border: 2px solid currentColor; border-right-color: transparent; border-radius: 50%; animation: sngl-spin 0.75s linear infinite; vertical-align: middle; }")
		style := g.buildCSSStyle(vn)
		label := g.evalStaticString(vnProps(vn), "label")
		id := ""
		if g.preview {
			id = g.allocID()
		}
		g.writeOpenTag(b, "span", id, style, vn, depth, vn.Pos)
		fmt.Fprintf(b, "<span class=\"sngl-spinner\"></span> %s</span>\n", html.EscapeString(label))
	case "badge":
		style := g.buildCSSStyle(vn)
		style = htmlutil.AppendCSS(style, "display", "inline-block")
		style = htmlutil.AppendCSS(style, "padding", "2px 8px")
		style = htmlutil.AppendCSS(style, "border-radius", "12px")
		style = htmlutil.AppendCSS(style, "font-size", "12px")
		value := g.evalStaticString(vnProps(vn), "value")
		id := ""
		if g.nodeIsReactive(vn) || g.preview {
			id = g.allocID()
		}
		g.writeOpenTag(b, "span", id, style, vn, depth, vn.Pos)
		fmt.Fprintf(b, "%s</span>\n", html.EscapeString(value))
		if g.propIsReactive(vnProps(vn), "value") {
			g.addTextUpdater(id, vnProp(vn, "value"))
		}
	case "tabs":
		g.renderStaticTabs(b, vn, depth)
	case "link":
		style := g.buildCSSStyle(vn)
		text := g.evalStaticString(vnProps(vn), "text")
		href := g.evalStaticString(vnProps(vn), "href")
		target := g.evalStaticString(vnProps(vn), "target")
		id := ""
		if g.nodeIsReactive(vn) || g.preview {
			id = g.allocID()
		}
		fmt.Fprintf(b, "%s<a", indent)
		if id != "" {
			fmt.Fprintf(b, " id=\"%s\"", id)
		}
		if style != "" {
			fmt.Fprintf(b, " style=\"%s\"", style)
		}
		fmt.Fprintf(b, " href=\"%s\"", html.EscapeString(href))
		if target == "blank" {
			b.WriteString(` target="_blank" rel="noopener"`)
		}
		g.writeUserAttrs(b, id, vn)
		b.WriteString(g.previewAttrs(vn.Pos))
		fmt.Fprintf(b, ">%s</a>\n", html.EscapeString(text))
		if evt := vnEvent(vn, "click"); evt != nil && len(evt.Body.Stmts) > 0 {
			if id == "" {
				id = g.allocID()
			}
			g.addClickHandler(id, evt.Body)
		}
	case "divider":
		style := g.buildCSSStyle(vn)
		label := g.evalStaticString(vnProps(vn), "label")
		id := ""
		if g.preview {
			id = g.allocID()
		}
		if label != "" {
			style = htmlutil.AppendCSS(style, "display", "flex")
			style = htmlutil.AppendCSS(style, "align-items", "center")
			style = htmlutil.AppendCSS(style, "gap", "8px")
			g.writeOpenTag(b, "div", id, style, vn, depth, vn.Pos)
			fmt.Fprintf(b, "<hr style=\"flex:1;border:none;border-top:1px solid #ccc\"/>")
			fmt.Fprintf(b, "<span>%s</span>", html.EscapeString(label))
			fmt.Fprintf(b, "<hr style=\"flex:1;border:none;border-top:1px solid #ccc\"/>")
			fmt.Fprintf(b, "</div>\n")
		} else {
			fmt.Fprintf(b, "%s<hr", indent)
			if id != "" {
				fmt.Fprintf(b, " id=\"%s\"", id)
			}
			if style != "" {
				fmt.Fprintf(b, " style=\"%s\"", style)
			}
			b.WriteString(g.previewAttrs(vn.Pos))
			b.WriteString(" />\n")
		}

	// --- Tier 3: Overlays & Layout ---
	case "modal":
		g.renderStaticModal(b, vn, depth)
	case "drawer":
		g.renderStaticConditionalContainer(b, vn, depth, "div")
	case "tooltip":
		// Wrap child with title attribute
		title := g.evalStaticString(vnProps(vn), "text")
		fmt.Fprintf(b, "%s<div title=\"%s\">\n", indent, html.EscapeString(title))
		for _, s := range vnChildren(vn) {
			g.renderStaticStmt(b, s, depth+1)
		}
		fmt.Fprintf(b, "%s</div>\n", indent)
	case "popover":
		g.renderStaticConditionalContainer(b, vn, depth, "div")
	case "accordion":
		g.renderStaticAccordion(b, vn, depth)
	case "splitview":
		style := g.buildCSSStyle(vn)
		style = htmlutil.AppendCSS(style, "display", "flex")
		direction := g.evalStaticString(vnProps(vn), "direction")
		if direction == "vertical" {
			style = htmlutil.AppendCSS(style, "flex-direction", "column")
		} else {
			style = htmlutil.AppendCSS(style, "flex-direction", "row")
		}
		id := ""
		if g.preview {
			id = g.allocID()
		}
		g.writeOpenTag(b, "div", id, style, vn, depth, vn.Pos)
		for i, s := range vnChildren(vn) {
			if i > 0 {
				fmt.Fprintf(b, "%s  <div style=\"width:4px;background:#ccc;cursor:col-resize\"></div>\n", indent)
			}
			fmt.Fprintf(b, "%s  <div style=\"flex:1;overflow:auto\">\n", indent)
			g.renderStaticStmt(b, s, depth+2)
			fmt.Fprintf(b, "%s  </div>\n", indent)
		}
		fmt.Fprintf(b, "%s</div>\n", indent)

	// --- Tier 4: Data & Desktop ---
	case "table":
		g.renderStaticTable(b, vn, depth)
	case "tree":
		g.renderStaticTree(b, vn, depth)
	case "menu":
		g.renderStaticConditionalContainer(b, vn, depth, "div")
	case "menubar":
		style := g.buildCSSStyle(vn)
		style = htmlutil.AppendCSS(style, "display", "flex")
		style = htmlutil.AppendCSS(style, "gap", "4px")
		id := ""
		if g.preview {
			id = g.allocID()
		}
		g.writeOpenTag(b, "nav", id, style, vn, depth, vn.Pos)
		fmt.Fprintf(b, "</nav>\n")
	case "toolbar":
		style := g.buildCSSStyle(vn)
		style = htmlutil.AppendCSS(style, "display", "flex")
		style = htmlutil.AppendCSS(style, "gap", "4px")
		style = htmlutil.AppendCSS(style, "align-items", "center")
		id := ""
		if g.preview {
			id = g.allocID()
		}
		g.writeOpenTag(b, "div", id, style, vn, depth, vn.Pos)
		for _, s := range vnChildren(vn) {
			g.renderStaticStmt(b, s, depth+1)
		}
		fmt.Fprintf(b, "%s</div>\n", indent)

	// --- Tier 5: Mobile & Specialized ---
	case "datepicker":
		g.renderStaticDatepicker(b, vn, depth)
	case "chip":
		style := g.buildCSSStyle(vn)
		style = htmlutil.AppendCSS(style, "display", "inline-flex")
		style = htmlutil.AppendCSS(style, "align-items", "center")
		style = htmlutil.AppendCSS(style, "padding", "4px 12px")
		style = htmlutil.AppendCSS(style, "border-radius", "16px")
		style = htmlutil.AppendCSS(style, "border", "1px solid #ccc")
		style = htmlutil.AppendCSS(style, "font-size", "14px")
		label := g.evalStaticString(vnProps(vn), "label")
		id := ""
		if g.nodeIsReactive(vn) || g.preview {
			id = g.allocID()
		}
		g.writeOpenTag(b, "span", id, style, vn, depth, vn.Pos)
		fmt.Fprintf(b, "%s", html.EscapeString(label))
		b.WriteString("</span>\n")
	case "avatar":
		style := g.buildCSSStyle(vn)
		style = htmlutil.AppendCSS(style, "display", "inline-flex")
		style = htmlutil.AppendCSS(style, "align-items", "center")
		style = htmlutil.AppendCSS(style, "justify-content", "center")
		style = htmlutil.AppendCSS(style, "border-radius", "50%")
		style = htmlutil.AppendCSS(style, "width", "40px")
		style = htmlutil.AppendCSS(style, "height", "40px")
		style = htmlutil.AppendCSS(style, "background", "#ccc")
		style = htmlutil.AppendCSS(style, "font-weight", "bold")
		initials := g.evalStaticString(vnProps(vn), "initials")
		if initials == "" {
			initials = g.evalStaticString(vnProps(vn), "alt")
		}
		id := ""
		if g.preview {
			id = g.allocID()
		}
		g.writeOpenTag(b, "div", id, style, vn, depth, vn.Pos)
		fmt.Fprintf(b, "%s</div>\n", html.EscapeString(initials))
	case "card":
		style := g.buildCSSStyle(vn)
		variant := g.evalStaticString(vnProps(vn), "variant")
		switch variant {
		case "elevated":
			style = htmlutil.AppendCSS(style, "box-shadow", "0 2px 8px rgba(0,0,0,0.15)")
		case "filled":
			style = htmlutil.AppendCSS(style, "background", "#f5f5f5")
		default: // outlined
			style = htmlutil.AppendCSS(style, "border", "1px solid #ddd")
		}
		style = htmlutil.AppendCSS(style, "border-radius", "8px")
		style = htmlutil.AppendCSS(style, "padding", "16px")
		id := ""
		if g.nodeIsReactive(vn) || g.preview {
			id = g.allocID()
		}
		g.writeOpenTag(b, "div", id, style, vn, depth, vn.Pos)
		for _, s := range vnChildren(vn) {
			g.renderStaticStmt(b, s, depth+1)
		}
		fmt.Fprintf(b, "%s</div>\n", indent)

	case "slot":
		// Project caller's children into this position
		for _, s := range g.slotChildren {
			g.renderStaticStmt(b, s, depth)
		}

	default:
		// User-defined or abstract component — inline at call site
		g.renderStaticUserComponent(b, vn, depth)
	}
}

func (g *htmlGen) renderStaticBox(b *strings.Builder, vn *ast.VisualNode, depth int, vertical bool) {
	indent := strings.Repeat("  ", depth)
	style := g.buildCSSStyle(vn)
	style = htmlutil.AppendCSS(style, "display", "flex")
	if vertical {
		style = htmlutil.AppendCSS(style, "flex-direction", "column")
	} else {
		style = htmlutil.AppendCSS(style, "flex-direction", "row")
	}

	if v := vnProp(vn, "scroll"); v != nil {
		if b, ok := exprLiteralBool(v); ok && b {
			if vertical {
				style = htmlutil.AppendCSS(style, "overflow-y", "auto")
			} else {
				style = htmlutil.AppendCSS(style, "overflow-x", "auto")
			}
		}
	}

	reactive := g.nodeIsReactive(vn)
	id := ""
	if reactive {
		id = g.allocID()
	}

	g.writeOpenTag(b, "div", id, style, vn, depth, vn.Pos)
	for _, s := range vnChildren(vn) {
		g.renderStaticStmt(b, s, depth+1)
	}
	fmt.Fprintf(b, "%s</div>\n", indent)
}

func (g *htmlGen) renderStaticText(b *strings.Builder, vn *ast.VisualNode, depth int) {
	style := g.buildCSSStyle(vn)
	val := g.evalStaticString(vnProps(vn), "value")
	reactive := g.propIsReactive(vnProps(vn), "value")
	id := ""
	if reactive {
		id = g.allocID()
	}

	// In test mode, id/class need an element ID for updaters
	if g.testMode && id == "" && g.vnHasUserAttrs(vn) {
		id = g.allocID()
	}

	g.writeOpenTag(b, "span", id, style, vn, depth, vn.Pos)
	b.WriteString(html.EscapeString(val))
	b.WriteString("</span>\n")

	if g.propIsReactive(vnProps(vn), "value") {
		g.addTextUpdater(id, vnProp(vn, "value"))
	}
	if g.testMode && id != "" {
		g.addUserAttrUpdaters(id, vn)
	}
}

func (g *htmlGen) renderStaticButton(b *strings.Builder, vn *ast.VisualNode, depth int) {
	style := g.buildCSSStyle(vn)
	text := g.evalStaticString(vnProps(vn), "text")
	reactive := g.propIsReactive(vnProps(vn), "text") || vnHasEvents(vn) || g.propIsReactive(vnProps(vn), "disabled")
	id := ""
	if reactive {
		id = g.allocID()
	}

	disabled := ""
	if v := vnProp(vn, "disabled"); v != nil {
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

	g.writeUserAttrs(b, id, vn)
	b.WriteString(g.previewAttrs(vn.Pos))
	fmt.Fprintf(b, "%s>%s</button>\n", disabled, html.EscapeString(text))

	if g.propIsReactive(vnProps(vn), "text") {
		g.addTextContentUpdater(id, vnProp(vn, "text"))
	}
	if g.propIsReactive(vnProps(vn), "disabled") {
		g.addDisabledUpdater(id, vnProp(vn, "disabled"))
	}

	// Event handlers
	if clickEvt := vnEvent(vn, "click"); clickEvt != nil {
		if len(clickEvt.Body.Stmts) > 0 {
			g.addClickHandler(id, clickEvt.Body)
		}
	}
}

func (g *htmlGen) renderStaticInput(b *strings.Builder, vn *ast.VisualNode, depth int) {
	style := g.buildCSSStyle(vn)
	id := g.allocID() // inputs are always reactive

	placeholder := ""
	if v := vnProp(vn, "placeholder"); v != nil {
		if s, ok := exprLiteralString(v); ok {
			placeholder = s
		}
	}

	inputType := "text"
	if v := vnProp(vn, "type"); v != nil {
		if s, ok := exprLiteralString(v); ok {
			inputType = s
		}
	}

	value := g.evalStaticString(vnProps(vn), "value")

	indent := strings.Repeat("  ", depth)
	fmt.Fprintf(b, "%s<input id=\"%s\"", indent, id)
	if style != "" {
		fmt.Fprintf(b, " style=\"%s\"", style)
	}
	if inputType != "text" {
		fmt.Fprintf(b, " type=\"%s\"", inputType)
	}
	if placeholder != "" {
		fmt.Fprintf(b, " placeholder=\"%s\"", html.EscapeString(placeholder))
	}
	fmt.Fprintf(b, " value=\"%s\"", html.EscapeString(value))

	g.writeUserAttrs(b, id, vn)
	b.WriteString(g.previewAttrs(vn.Pos))
	b.WriteString(" />\n")

	// Add value sync updater if the input is bound to state via set()
	if inputEvt := vnEvent(vn, "input"); inputEvt != nil {
		if len(inputEvt.Body.Stmts) > 0 {
			// Extract the set() target to determine the JS expression for the bound value
			if target, ok := extractSetTarget(inputEvt.Body.Stmts[0]); ok {
				jsExpr := g.lang.TranslateExpr(target, g.scope)
				root := codegen.FindRootIdent(target)
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
	if inputEvt := vnEvent(vn, "input"); inputEvt != nil {
		if len(inputEvt.Body.Stmts) > 0 {
			g.addInputHandler(id, inputEvt.Body)
		}
	}
}

func (g *htmlGen) renderStaticCheckbox(b *strings.Builder, vn *ast.VisualNode, depth int) {
	style := g.buildCSSStyle(vn)
	id := g.allocID()

	checked := false
	if v := vnProp(vn, "checked"); v != nil {
		checked = g.evalStaticBool(&v)
	}
	label := g.evalStaticString(vnProps(vn), "label")

	indent := strings.Repeat("  ", depth)
	fmt.Fprintf(b, "%s<label id=\"%s\"", indent, id)
	if style != "" {
		fmt.Fprintf(b, " style=\"%s\"", style)
	}

	g.writeUserAttrs(b, id, vn)
	b.WriteString(g.previewAttrs(vn.Pos))
	b.WriteString(">")
	if checked {
		b.WriteString("<input type=\"checkbox\" checked />")
	} else {
		b.WriteString("<input type=\"checkbox\" />")
	}
	fmt.Fprintf(b, " %s</label>\n", html.EscapeString(label))

	if changeEvt := vnEvent(vn, "change"); changeEvt != nil {
		if len(changeEvt.Body.Stmts) > 0 {
			g.addChangeHandler(id, changeEvt.Body)
		}
	}
}

func (g *htmlGen) renderStaticImage(b *strings.Builder, vn *ast.VisualNode, depth int) {
	style := g.buildCSSStyle(vn)
	alt := "image"
	if v := vnProp(vn, "alt"); v != nil {
		if s, ok := exprLiteralString(v); ok {
			alt = s
		}
	}
	src := ""
	if v := vnProp(vn, "src"); v != nil {
		if s, ok := exprLiteralString(v); ok {
			src = s
		}
	}
	if v := vnProp(vn, "fit"); v != nil {
		if s, ok := exprLiteralString(v); ok {
			style = htmlutil.AppendCSS(style, "object-fit", s)
		}
	}

	id := ""
	if g.preview {
		id = g.allocID()
	}

	indent := strings.Repeat("  ", depth)
	fmt.Fprintf(b, "%s<img", indent)
	if id != "" {
		fmt.Fprintf(b, " id=\"%s\"", id)
	}
	if src != "" {
		fmt.Fprintf(b, " src=\"%s\"", html.EscapeString(src))
	}
	fmt.Fprintf(b, " alt=\"%s\"", html.EscapeString(alt))
	if style != "" {
		fmt.Fprintf(b, " style=\"%s\"", style)
	}

	g.writeUserAttrs(b, id, vn)
	if g.preview {
		b.WriteString(g.previewAttrs(vn.Pos))
	}
	b.WriteString(" />\n")
}

// --- New component renderers ---

func (g *htmlGen) renderStaticRadio(b *strings.Builder, vn *ast.VisualNode, depth int) {
	style := g.buildCSSStyle(vn)
	id := g.allocID()
	indent := strings.Repeat("  ", depth)
	g.writeOpenTag(b, "fieldset", id, style, vn, depth, vn.Pos)
	// Static options rendered if literal
	if v := vnProp(vn, "options"); exprIsLiteral(v) {
		value := g.evalStaticString(vnProps(vn), "value")
		if items, ok := exprLiteralAny(v).([]any); ok {
			for _, item := range items {
				s := fmt.Sprint(item)
				checked := ""
				if s == value {
					checked = " checked"
				}
				fmt.Fprintf(b, "%s  <label><input type=\"radio\" name=\"%s\" value=\"%s\"%s /> %s</label>\n",
					indent, id, html.EscapeString(s), checked, html.EscapeString(s))
			}
		}
	}
	fmt.Fprintf(b, "%s</fieldset>\n", indent)
	if evt := vnEvent(vn, "change"); evt != nil && len(evt.Body.Stmts) > 0 {
		g.addChangeHandler(id, evt.Body)
	}
}

func (g *htmlGen) renderStaticToggle(b *strings.Builder, vn *ast.VisualNode, depth int) {
	style := g.buildCSSStyle(vn)
	id := g.allocID()
	checked := false
	if v := vnProp(vn, "checked"); v != nil {
		checked = g.evalStaticBool(&v)
	}
	label := g.evalStaticString(vnProps(vn), "label")
	indent := strings.Repeat("  ", depth)
	checkedAttr := ""
	if checked {
		checkedAttr = " checked"
	}
	fmt.Fprintf(b, "%s<label id=\"%s\"", indent, id)
	if style != "" {
		fmt.Fprintf(b, " style=\"%s\"", style)
	}
	g.writeUserAttrs(b, id, vn)
	b.WriteString(g.previewAttrs(vn.Pos))
	fmt.Fprintf(b, "><input type=\"checkbox\" role=\"switch\"%s /> %s</label>\n", checkedAttr, html.EscapeString(label))
	if evt := vnEvent(vn, "change"); evt != nil && len(evt.Body.Stmts) > 0 {
		g.addChangeHandler(id, evt.Body)
	}
}

func (g *htmlGen) renderStaticSelect(b *strings.Builder, vn *ast.VisualNode, depth int) {
	style := g.buildCSSStyle(vn)
	id := g.allocID()
	indent := strings.Repeat("  ", depth)
	value := g.evalStaticString(vnProps(vn), "value")
	placeholder := g.evalStaticString(vnProps(vn), "placeholder")
	fmt.Fprintf(b, "%s<select id=\"%s\"", indent, id)
	if style != "" {
		fmt.Fprintf(b, " style=\"%s\"", style)
	}
	g.writeUserAttrs(b, id, vn)
	b.WriteString(g.previewAttrs(vn.Pos))
	b.WriteString(">\n")
	if placeholder != "" {
		fmt.Fprintf(b, "%s  <option value=\"\" disabled selected>%s</option>\n", indent, html.EscapeString(placeholder))
	}
	if v := vnProp(vn, "options"); exprIsLiteral(v) {
		if items, ok := exprLiteralAny(v).([]any); ok {
			for _, item := range items {
				s := fmt.Sprint(item)
				sel := ""
				if s == value {
					sel = " selected"
				}
				fmt.Fprintf(b, "%s  <option value=\"%s\"%s>%s</option>\n", indent, html.EscapeString(s), sel, html.EscapeString(s))
			}
		}
	}
	fmt.Fprintf(b, "%s</select>\n", indent)
	if evt := vnEvent(vn, "change"); evt != nil && len(evt.Body.Stmts) > 0 {
		g.addChangeHandler(id, evt.Body)
	}
}

func (g *htmlGen) renderStaticTextarea(b *strings.Builder, vn *ast.VisualNode, depth int) {
	style := g.buildCSSStyle(vn)
	id := g.allocID()
	indent := strings.Repeat("  ", depth)
	value := g.evalStaticString(vnProps(vn), "value")
	placeholder := g.evalStaticString(vnProps(vn), "placeholder")
	rows := "3"
	if v := vnProp(vn, "rows"); v != nil {
		if n, ok := exprLiteralInt(v); ok {
			rows = fmt.Sprint(n)
		}
	}
	fmt.Fprintf(b, "%s<textarea id=\"%s\"", indent, id)
	if style != "" {
		fmt.Fprintf(b, " style=\"%s\"", style)
	}
	fmt.Fprintf(b, " rows=\"%s\"", rows)
	if placeholder != "" {
		fmt.Fprintf(b, " placeholder=\"%s\"", html.EscapeString(placeholder))
	}
	g.writeUserAttrs(b, id, vn)
	b.WriteString(g.previewAttrs(vn.Pos))
	fmt.Fprintf(b, ">%s</textarea>\n", html.EscapeString(value))
	if evt := vnEvent(vn, "input"); evt != nil && len(evt.Body.Stmts) > 0 {
		g.addInputHandler(id, evt.Body)
	}
}

func (g *htmlGen) renderStaticTabs(b *strings.Builder, vn *ast.VisualNode, depth int) {
	style := g.buildCSSStyle(vn)
	id := g.allocID()
	indent := strings.Repeat("  ", depth)
	g.writeOpenTag(b, "div", id, style, vn, depth, vn.Pos)
	// Tab bar
	fmt.Fprintf(b, "%s  <div role=\"tablist\" style=\"display:flex;gap:4px;border-bottom:1px solid #ccc\">\n", indent)
	if v := vnProp(vn, "items"); exprIsLiteral(v) {
		selected := 0
		if sv := vnProp(vn, "selected"); sv != nil {
			if n, ok := exprLiteralInt(sv); ok {
				selected = n
			}
		}
		if items, ok := exprLiteralAny(v).([]any); ok {
			for i, item := range items {
				s := fmt.Sprint(item)
				activeStyle := ""
				if i == selected {
					activeStyle = ";border-bottom:2px solid #333;font-weight:bold"
				}
				fmt.Fprintf(b, "%s    <button style=\"padding:8px 16px;border:none;background:none;cursor:pointer%s\">%s</button>\n",
					indent, activeStyle, html.EscapeString(s))
			}
		}
	}
	fmt.Fprintf(b, "%s  </div>\n", indent)
	// Tab content — show selected child
	for _, s := range vnChildren(vn) {
		g.renderStaticStmt(b, s, depth+1)
	}
	fmt.Fprintf(b, "%s</div>\n", indent)
}

func (g *htmlGen) renderStaticModal(b *strings.Builder, vn *ast.VisualNode, depth int) {
	style := g.buildCSSStyle(vn)
	id := g.allocID()
	indent := strings.Repeat("  ", depth)
	open := true
	if v := vnProp(vn, "open"); v != nil {
		open = g.evalStaticBool(&v)
	}
	display := ""
	if !open {
		display = "display:none;"
	}
	title := g.evalStaticString(vnProps(vn), "title")
	overlayStyle := display + "position:fixed;top:0;left:0;right:0;bottom:0;background:rgba(0,0,0,0.5);display:flex;align-items:center;justify-content:center;z-index:1000"
	if !open {
		overlayStyle = "display:none"
	}
	fmt.Fprintf(b, "%s<div id=\"%s\" style=\"%s\"", indent, id, overlayStyle)
	g.writeUserAttrs(b, id, vn)
	b.WriteString(g.previewAttrs(vn.Pos))
	b.WriteString(">\n")
	contentStyle := style
	if contentStyle == "" {
		contentStyle = "background:#fff;border-radius:8px;padding:24px;min-width:300px;max-width:80%"
	} else {
		contentStyle = htmlutil.AppendCSS(contentStyle, "background", "#fff")
		contentStyle = htmlutil.AppendCSS(contentStyle, "border-radius", "8px")
		contentStyle = htmlutil.AppendCSS(contentStyle, "padding", "24px")
	}
	fmt.Fprintf(b, "%s  <div style=\"%s\">\n", indent, contentStyle)
	if title != "" {
		fmt.Fprintf(b, "%s    <h3 style=\"margin:0 0 16px\">%s</h3>\n", indent, html.EscapeString(title))
	}
	for _, s := range vnChildren(vn) {
		g.renderStaticStmt(b, s, depth+2)
	}
	fmt.Fprintf(b, "%s  </div>\n", indent)
	fmt.Fprintf(b, "%s</div>\n", indent)
	if g.propIsReactive(vnProps(vn), "open") {
		g.addIfUpdater(id, vnProp(vn, "open"))
	}
}

func (g *htmlGen) renderStaticConditionalContainer(b *strings.Builder, vn *ast.VisualNode, depth int, tag string) {
	style := g.buildCSSStyle(vn)
	id := g.allocID()
	indent := strings.Repeat("  ", depth)
	open := true
	if v := vnProp(vn, "open"); v != nil {
		open = g.evalStaticBool(&v)
	}
	if v := vnProp(vn, "visible"); v != nil {
		open = g.evalStaticBool(&v)
	}
	if !open {
		style = htmlutil.AppendCSS(style, "display", "none")
	}
	g.writeOpenTag(b, tag, id, style, vn, depth, vn.Pos)
	for _, s := range vnChildren(vn) {
		g.renderStaticStmt(b, s, depth+1)
	}
	fmt.Fprintf(b, "%s</%s>\n", indent, tag)
	if g.propIsReactive(vnProps(vn), "open") {
		g.addIfUpdater(id, vnProp(vn, "open"))
	}
}

func (g *htmlGen) renderStaticAccordion(b *strings.Builder, vn *ast.VisualNode, depth int) {
	style := g.buildCSSStyle(vn)
	id := ""
	if g.preview {
		id = g.allocID()
	}
	indent := strings.Repeat("  ", depth)
	g.writeOpenTag(b, "div", id, style, vn, depth, vn.Pos)
	if v := vnProp(vn, "items"); exprIsLiteral(v) {
		if items, ok := exprLiteralAny(v).([]any); ok {
			for i, item := range items {
				s := fmt.Sprint(item)
				fmt.Fprintf(b, "%s  <details>\n", indent)
				fmt.Fprintf(b, "%s    <summary>%s</summary>\n", indent, html.EscapeString(s))
				children := vnChildNodes(vn)
				if i < len(children) {
					g.renderStaticNode(b, children[i], depth+2)
				}
				fmt.Fprintf(b, "%s  </details>\n", indent)
			}
		}
	}
	fmt.Fprintf(b, "%s</div>\n", indent)
}

func (g *htmlGen) renderStaticTable(b *strings.Builder, vn *ast.VisualNode, depth int) {
	style := g.buildCSSStyle(vn)
	style = htmlutil.AppendCSS(style, "border-collapse", "collapse")
	style = htmlutil.AppendCSS(style, "width", "100%")
	id := ""
	if g.nodeIsReactive(vn) || g.preview {
		id = g.allocID()
	}
	indent := strings.Repeat("  ", depth)
	g.writeOpenTag(b, "table", id, style, vn, depth, vn.Pos)
	// Header
	if v := vnProp(vn, "columns"); exprIsLiteral(v) {
		if cols, ok := exprLiteralAny(v).([]any); ok {
			fmt.Fprintf(b, "%s  <thead><tr>\n", indent)
			for _, col := range cols {
				fmt.Fprintf(b, "%s    <th style=\"text-align:left;padding:8px;border-bottom:2px solid #ddd\">%s</th>\n", indent, html.EscapeString(fmt.Sprint(col)))
			}
			fmt.Fprintf(b, "%s  </tr></thead>\n", indent)
		}
	}
	// Body
	if v := vnProp(vn, "rows"); exprIsLiteral(v) {
		if rows, ok := exprLiteralAny(v).([]any); ok {
			fmt.Fprintf(b, "%s  <tbody>\n", indent)
			for _, row := range rows {
				fmt.Fprintf(b, "%s    <tr>\n", indent)
				if cells, ok := row.([]any); ok {
					for _, cell := range cells {
						fmt.Fprintf(b, "%s      <td style=\"padding:8px;border-bottom:1px solid #eee\">%s</td>\n", indent, html.EscapeString(fmt.Sprint(cell)))
					}
				}
				fmt.Fprintf(b, "%s    </tr>\n", indent)
			}
			fmt.Fprintf(b, "%s  </tbody>\n", indent)
		}
	}
	fmt.Fprintf(b, "%s</table>\n", indent)
}

func (g *htmlGen) renderStaticTree(b *strings.Builder, vn *ast.VisualNode, depth int) {
	style := g.buildCSSStyle(vn)
	id := ""
	if g.preview {
		id = g.allocID()
	}
	indent := strings.Repeat("  ", depth)
	style = htmlutil.AppendCSS(style, "list-style", "none")
	style = htmlutil.AppendCSS(style, "padding-left", "16px")
	g.writeOpenTag(b, "ul", id, style, vn, depth, vn.Pos)
	if v := vnProp(vn, "items"); exprIsLiteral(v) {
		if items, ok := exprLiteralAny(v).([]any); ok {
			for _, item := range items {
				fmt.Fprintf(b, "%s  <li>▶ %s</li>\n", indent, html.EscapeString(fmt.Sprint(item)))
			}
		}
	}
	fmt.Fprintf(b, "%s</ul>\n", indent)
}

func (g *htmlGen) renderStaticDatepicker(b *strings.Builder, vn *ast.VisualNode, depth int) {
	style := g.buildCSSStyle(vn)
	id := g.allocID()
	indent := strings.Repeat("  ", depth)
	value := g.evalStaticString(vnProps(vn), "value")
	placeholder := g.evalStaticString(vnProps(vn), "placeholder")
	fmt.Fprintf(b, "%s<input id=\"%s\" type=\"date\"", indent, id)
	if style != "" {
		fmt.Fprintf(b, " style=\"%s\"", style)
	}
	if value != "" {
		fmt.Fprintf(b, " value=\"%s\"", html.EscapeString(value))
	}
	if placeholder != "" {
		fmt.Fprintf(b, " placeholder=\"%s\"", html.EscapeString(placeholder))
	}
	g.writeUserAttrs(b, id, vn)
	b.WriteString(g.previewAttrs(vn.Pos))
	b.WriteString(" />\n")
	if evt := vnEvent(vn, "change"); evt != nil && len(evt.Body.Stmts) > 0 {
		g.addChangeHandler(id, evt.Body)
	}
}

const maxComponentDepth = 10

// renderRawElement renders a VisualNode as a raw HTML element.
// The component name (or its local part for qualified names) is used as the tag.
func (g *htmlGen) renderRawElement(b *strings.Builder, vn *ast.VisualNode, depth int) {
	indent := strings.Repeat("  ", depth)
	// Extract tag name: "html.div" → "div", "button" → "button"
	tag := vnName(vn)
	if _, local, ok := strings.Cut(tag, "."); ok {
		tag = local
	}

	id := ""
	if g.nodeIsReactive(vn) || g.preview || g.testMode {
		id = g.allocID()
	}
	style := g.buildCSSStyle(vn)

	// Build inline attributes from static props.
	// innerText and innerHTML are rendered as element content, not attributes.
	var attrs strings.Builder
	staticInnerText := ""
	staticInnerHTML := ""
	for name, expr := range vnProps(vn) {
		if name == "style" {
			continue
		}
		// Try resolving SNGL expressions (e.g. component param references)
		// to static values for the initial HTML render.
		if exprIsReactive(expr) {
			if val := g.evalInitialString(expr); val != "" {
				switch name {
				case "innerHTML":
					staticInnerHTML = val
				case "innerText":
					staticInnerText = val
				default:
					fmt.Fprintf(&attrs, " %s=\"%s\"", html.EscapeString(name), html.EscapeString(val))
				}
				continue
			}
		}
		if !exprIsReactive(expr) {
			val := g.evalStaticString(vnProps(vn), name)
			switch name {
			case "innerText":
				staticInnerText = val
			case "innerHTML":
				staticInnerHTML = val
			default:
				if bv, ok := exprLiteralBool(expr); ok {
					if bv {
						fmt.Fprintf(&attrs, " %s", html.EscapeString(name))
					}
				} else if exprIsLiteral(expr) {
					fmt.Fprintf(&attrs, " %s=\"%s\"", html.EscapeString(name), html.EscapeString(val))
				} else if val != "" {
					fmt.Fprintf(&attrs, " %s=\"%s\"", html.EscapeString(name), html.EscapeString(val))
				}
			}
		}
	}

	// Write open tag with inline attributes
	if g.preview && id == "" {
		id = g.allocID()
	}
	fmt.Fprintf(b, "%s<%s", indent, tag)
	if id != "" {
		fmt.Fprintf(b, " id=\"%s\"", id)
	}
	// Emit static class for raw HTML elements
	if exprIsLiteral(vnProp(vn, "class")) {
		if s, ok := exprLiteralString(vnProp(vn, "class")); ok && s != "" {
			fmt.Fprintf(b, " class=%q", s)
		}
	}
	if style != "" {
		fmt.Fprintf(b, " style=\"%s\"", style)
	}
	b.WriteString(attrs.String())
	if vn != nil {

		g.writeUserAttrs(b, id, vn)
	}
	if g.preview && vn.Pos.IsValid() {
		fmt.Fprintf(b, " data-sngl-line=\"%d\" data-sngl-col=\"%d\"", vn.Pos.Line, vn.Pos.Column)
	}

	// Self-closing tags
	switch tag {
	case "input", "img", "br", "hr", "meta", "link", "area", "base", "col", "embed", "source", "track", "wbr":
		b.WriteString("/>\n")
	default:
		b.WriteString(">")
		if staticInnerHTML != "" {
			b.WriteString("\n")
			b.WriteString(staticInnerHTML)
			b.WriteString("\n")
			fmt.Fprintf(b, "%s</%s>\n", indent, tag)
		} else if staticInnerText != "" {
			b.WriteString(html.EscapeString(staticInnerText))
			fmt.Fprintf(b, "</%s>\n", tag)
		} else {
			b.WriteString("\n")
			for _, s := range vnChildren(vn) {
				g.renderStaticStmt(b, s, depth+1)
			}
			fmt.Fprintf(b, "%s</%s>\n", indent, tag)
		}
	}

	if id == "" {
		return
	}

	// Reactive props: set via JS updaters
	for name, expr := range vnProps(vn) {
		if name == "style" {
			continue
		}
		if exprIsReactive(expr) {
			jsVal := g.exprToJS(expr)
			deps := g.exprDeps(expr)
			uname := fmt.Sprintf("$u_%s_%s", id[1:], name)
			var body string
			switch name {
			case "innerHTML", "innerText", "textContent", "value", "checked", "disabled":
				// DOM properties — set directly, not via setAttribute
				body = fmt.Sprintf(`%s.%s = %s;`, id, name, jsVal)
			default:
				body = fmt.Sprintf(`%s.setAttribute(%q, %s);`, id, name, jsVal)
			}
			g.updates = append(g.updates, updateFunc{
				funcName: uname,
				body:     body,
				deps:     deps,
			})
		}
	}

	// Wire up events
	for name, expr := range vnEvents(vn) {
		switch name {
		case "click":
			g.addClickHandler(id, expr.Body)
		case "input":
			g.addInputHandler(id, expr.Body)
		case "change":
			g.addChangeHandler(id, expr.Body)
		default:
			g.addClickHandler(id, expr.Body) // fallback
		}
	}
}

func (g *htmlGen) renderStaticUserComponent(b *strings.Builder, vn *ast.VisualNode, depth int) {
	// Find the component definition
	comp := codegen.FindComponent(g.doc, vnName(vn))
	if comp == nil {
		// Not a user/abstract component — render as raw HTML element
		g.renderRawElement(b, vn, depth)
		return
	}

	g.componentDepth++
	if g.componentDepth > maxComponentDepth {
		g.componentDepth--
		return // prevent infinite component recursion
	}
	defer func() { g.componentDepth-- }()

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
	for _, p := range compParams(comp) {
		uniqueName := p.Name + suffix
		g.scope.LocalVars[p.Name] = true
		renames[p.Name] = uniqueName

		// Resolve param value from call-site props or default
		var jsVal string
		if expr := vnProp(vn, p.Name); expr != nil {
			jsVal = g.exprToJS(expr)
		} else {
			jsVal = g.literalToJS(p.Default)
		}
		g.componentParams = append(g.componentParams, componentParam{
			name:  uniqueName,
			value: jsVal,
		})
	}
	// Handle component computed functions: rename and register as local
	for _, fn := range compFuncs(comp) {
		if fn.Body != nil && len(fn.Params.Params) == 0 {
			uniqueName := fn.Name + suffix
			g.scope.LocalVars[fn.Name] = true
			renames[fn.Name] = uniqueName
		}
	}
	g.scope.Renames = renames

	// Emit computed functions with renamed references
	for _, fn := range compFuncs(comp) {
		if fn.Body != nil && len(fn.Params.Params) == 0 {
			uniqueName := fn.Name + suffix
			body := g.exprToJS(fn.Body)
			g.componentParams = append(g.componentParams, componentParam{
				name:  uniqueName,
				value: body,
			})
		}
	}

	// Promote component state vars to the parent scope with unique names.
	dataRenames := make(map[string]string)
	for _, dv := range compVars(comp) {
		uniqueName := dv.Name + suffix
		g.scope.ModelFields[uniqueName] = true
		g.dt.ModelFields[uniqueName] = true
		g.scope.LocalVars[dv.Name] = true
		renames[dv.Name] = "state." + uniqueName
		dataRenames[dv.Name] = uniqueName
	}
	g.scope.Renames = renames

	// TODO: Pre-translate component timers (requires checker.Package)

	// Set dataRenames so handler-adding methods can remap MutatedFields.
	savedDataRenames := g.dataRenames
	g.dataRenames = dataRenames

	// Set slot children for abstract component body expansion
	savedSlot := g.slotChildren
	g.slotChildren = vnChildren(vn)

	// Inline the component body at the call site.
	for _, s := range compBodyStmts(comp) {
		g.renderStaticStmt(b, s, depth)
	}

	g.slotChildren = savedSlot
	g.dataRenames = savedDataRenames

	// Restore locals
	g.scope.LocalVars = savedLocals
	g.scope.Renames = savedRenames
}

// emitScript writes the <script> block content.
func (g *htmlGen) emitScript(b *strings.Builder) {
	// TODO: Collect document-level timers (requires checker.Package)
	for i := range g.timers {
		g.timers[i].index = i
	}

	// Run IR optimizer on collected updaters, handlers, and timers.
	g.optimizeIR()

	// TODO: Extern bindings (requires checker.Package for import resolution)

	// State initialization
	b.WriteString("// State\nlet state = {")
	var stateFields []string
	for _, dv := range docVars(g.doc) {
		val := g.literalToJS(dv.Init)
		stateFields = append(stateFields, dv.Name+": "+val)
	}
	b.WriteString(strings.Join(stateFields, ", "))
	b.WriteString("};\n\n")

	// Struct constructors
	for _, sd := range docStructs(g.doc) {
		var params []string
		var body []string
		for _, f := range sd.Fields {
			params = append(params, f.Name)
			body = append(body, f.Name)
		}
		fmt.Fprintf(b, "function %s(%s) { return {%s}; }\n",
			sd.Name, strings.Join(params, ", "), strings.Join(body, ", "))
	}
	if len(docStructs(g.doc)) > 0 {
		b.WriteString("\n")
	}

	// Computed functions (zero-arg expression-form)
	hasComputed := false
	for _, fn := range docFuncs(g.doc) {
		if fn.Body != nil && len(fn.Params.Params) == 0 {
			body := g.exprToJS(fn.Body)
			fmt.Fprintf(b, "function $%s() { return %s; }\n", fn.Name, body)
			hasComputed = true
		}
	}
	if hasComputed {
		b.WriteString("\n")
	}

	// User-defined functions
	emittedFuncs := false
	for _, fn := range docFuncs(g.doc) {
		g.emitJSFunc(b, fn)
		emittedFuncs = true
	}
	if emittedFuncs {
		b.WriteString("\n")
	}

	// Setters — emit for fields with @change handlers, timer controls, or preview mode
	for _, dv := range docVars(g.doc) {
		needsSetter := g.preview
		for _, h := range dv.Handlers {
			if h.Name == "change" {
				needsSetter = true
				break
			}
		}
		if !needsSetter {
			for _, t := range g.timers {
				if t.activeVar == dv.Name {
					needsSetter = true
					break
				}
			}
		}
		if !needsSetter {
			continue
		}
		fmt.Fprintf(b, "function $set_%s(v) {\n", dv.Name)
		fmt.Fprintf(b, "  state.%s = v;\n", dv.Name)
		mutated := map[string]bool{dv.Name: true}
		for _, u := range g.findAffectedUpdaters(mutated) {
			fmt.Fprintf(b, "  %s();\n", u.funcName)
		}
		for _, h := range dv.Handlers {
			if h.Name == "change" {
				for _, s := range h.Body.Stmts {
					stmts := g.lang.TranslateMutation(s, g.scope)
					for _, js := range stmts {
						fmt.Fprintf(b, "  %s;\n", js)
					}
				}
			}
		}
		for _, t := range g.timers {
			if t.activeVar == dv.Name {
				fmt.Fprintf(b, "  $timer_%d_sync();\n", t.index)
			}
		}
		if g.preview {
			b.WriteString("  __sngl_sync_state();\n")
		}
		b.WriteString("}\n")
	}
	if len(stateFields) > 0 {
		b.WriteString("\n")
	}

	b.WriteString("\n")

	// Constants
	for _, c := range docConsts(g.doc) {
		val := g.literalToJS(c.Init)
		fmt.Fprintf(b, "const %s = %s;\n", c.Name, val)
	}
	if len(docConsts(g.doc)) > 0 {
		b.WriteString("\n")
	}

	// Helper functions — only emit if used
	if g.scope.NeededHelpers["String"] {
		b.WriteString("function String(v) { return \"\" + v; }\n\n")
	}

	// Component param constants (skip params fully consumed by static HTML render)
	for _, cp := range g.componentParams {
		if cp.staticOnly {
			continue
		}
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
		if g.preview {
			lines = append(lines, "__sngl_sync_state();")
		}
		body := strings.Join(lines, "\n  ")
		if h.event == "input" {
			fmt.Fprintf(b, "%s.addEventListener(\"%s\", function(e) {\n  %s\n});\n", h.elemID, h.event, body)
		} else {
			fmt.Fprintf(b, "%s.addEventListener(\"%s\", function() {\n  %s\n});\n", h.elemID, h.event, body)
		}
	}

	// Timers
	for _, t := range g.timers {
		updaters := g.findAffectedUpdaters(t.mutated)
		var tickLines []string
		tickLines = append(tickLines, t.body)
		for _, u := range updaters {
			tickLines = append(tickLines, u.funcName+"();")
		}
		if g.preview {
			tickLines = append(tickLines, "__sngl_sync_state();")
		}
		tickBody := strings.Join(tickLines, "\n  ")
		fmt.Fprintf(b, "\nlet $timer_%d = null;\n", t.index)
		fmt.Fprintf(b, "function $timer_%d_tick() {\n  %s\n}\n", t.index, tickBody)
		fmt.Fprintf(b, "function $timer_%d_sync() {\n", t.index)
		fmt.Fprintf(b, "  if (state.%s && !$timer_%d) {\n", t.activeVar, t.index)
		fmt.Fprintf(b, "    $timer_%d = setInterval($timer_%d_tick, %d);\n", t.index, t.index, t.intervalMs)
		fmt.Fprintf(b, "  } else if (!state.%s && $timer_%d) {\n", t.activeVar, t.index)
		fmt.Fprintf(b, "    clearInterval($timer_%d);\n", t.index)
		fmt.Fprintf(b, "    $timer_%d = null;\n", t.index)
		b.WriteString("  }\n}\n")
	}

	// Initial sync: call all update functions once to set DOM from initial state
	if len(g.updates) > 0 || len(g.timers) > 0 {
		b.WriteString("\n// Initial sync\n")
		for _, u := range g.updates {
			fmt.Fprintf(b, "%s();\n", u.funcName)
		}
		for _, t := range g.timers {
			fmt.Fprintf(b, "$timer_%d_sync();\n", t.index)
		}
	}

	// Preview mode: sync state to parent frame on every mutation
	if g.preview {
		b.WriteString("\nfunction __sngl_sync_state() { window.parent.postMessage({type: 'sngl-state', state: JSON.parse(JSON.stringify(state))}, '*'); }\n")
		b.WriteString("__sngl_sync_state();\n")
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

// optimizeIR builds a MutationModel from the collected updaters, handlers,
// and timers, runs the IR optimizer, then writes the results back.
func (g *htmlGen) optimizeIR() {
	// Convert platform types → IR types.
	updaters := make([]codegen.Updater, len(g.updates))
	for i, u := range g.updates {
		updaters[i] = codegen.Updater{
			Name: u.funcName,
			Body: u.body,
			Deps: u.deps,
		}
	}
	// Keep handler body strings keyed by elemID+event (IR uses ast.Node,
	// but HTML has already translated to JS strings).
	handlerBodyMap := make(map[string]string)
	handlers := make([]codegen.Handler, len(g.handlers))
	for i, h := range g.handlers {
		handlerBodyMap[h.elemID+":"+h.event] = h.body
		handlers[i] = codegen.Handler{
			NodeID:  h.elemID,
			Event:   h.event,
			Mutated: h.mutated,
		}
	}
	timers := make([]codegen.TimerHandler, len(g.timers))
	for i, t := range g.timers {
		timers[i] = codegen.TimerHandler{
			TimerInfo: codegen.TimerInfo{
				Index:      t.index,
				IntervalMs: t.intervalMs,
				ActiveVar:  t.activeVar,
			},
			Mutated: t.mutated,
		}
	}

	m := &codegen.MutationModel{
		Analysis:   g.CommonAnalysis,
		DepTracker: g.dt,
		Updaters:   updaters,
		Handlers:   handlers,
		Timers:     timers,
	}

	codegen.OptimizeMutation(m)

	// Write back optimized updaters.
	g.updates = make([]updateFunc, len(m.Updaters))
	for i, u := range m.Updaters {
		g.updates[i] = updateFunc{
			funcName: u.Name,
			body:     u.Body,
			deps:     u.Deps,
		}
	}

	// Write back optimized handlers, restoring body strings.
	g.handlers = make([]eventHandler, len(m.Handlers))
	for i, h := range m.Handlers {
		g.handlers[i] = eventHandler{
			elemID:  h.NodeID,
			event:   h.Event,
			body:    handlerBodyMap[h.NodeID+":"+h.Event],
			mutated: h.Mutated,
		}
	}

	// --- HTML-specific optimization passes ---

	// Deduplicate component param constants: if two params have the same
	// JS value, reuse the first name and rewrite references in updater bodies.
	g.deduplicateComponentParams()
}

// deduplicateComponentParams merges component params with identical values.
// If two params have the same JS expression, the second is removed and all
// references in updater bodies are rewritten to use the first name.
func (g *htmlGen) deduplicateComponentParams() {
	if len(g.componentParams) <= 1 {
		return
	}

	// Map value → first name that has this value.
	valueToName := make(map[string]string)
	var deduped []componentParam
	renames := make(map[string]string) // old name → canonical name

	for _, cp := range g.componentParams {
		if canonical, exists := valueToName[cp.value]; exists {
			renames[cp.name] = canonical
		} else {
			valueToName[cp.value] = cp.name
			deduped = append(deduped, cp)
		}
	}

	if len(renames) == 0 {
		return
	}

	g.componentParams = deduped

	// Rewrite updater bodies to use canonical names.
	for i, u := range g.updates {
		body := u.body
		for old, canonical := range renames {
			body = strings.ReplaceAll(body, old, canonical)
		}
		g.updates[i].body = body
	}
}

func (g *htmlGen) findAffectedUpdaters(mutatedFields map[string]bool) []updateFunc {
	if len(mutatedFields) == 0 {
		return nil
	}

	expanded := g.dt.ExpandMutated(mutatedFields)

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

func (g *htmlGen) addAttrUpdater(elemID, attr string, expr ast.Expr) {
	jsExpr := g.exprToJS(expr)
	deps := g.exprDeps(expr)
	name := fmt.Sprintf("$u_%s_%s", elemID[1:], attr)
	g.updates = append(g.updates, updateFunc{
		funcName: name,
		body:     fmt.Sprintf("%s.setAttribute(%q, %s);", elemID, attr, jsExpr),
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

// addElseUpdater adds a display updater for the else branch of an if statement.
func (g *htmlGen) addElseUpdater(elemID string, cond ast.Expr) {
	jsExpr := g.exprToJS(cond)
	deps := g.exprDeps(cond)
	name := fmt.Sprintf("$u_%s_else", elemID[1:])
	g.updates = append(g.updates, updateFunc{
		funcName: name,
		body:     fmt.Sprintf("%s.style.display = %s ? \"none\" : \"\";", elemID, jsExpr),
		deps:     deps,
	})
}

// addForStmtUpdater adds a list updater for a v2 ForStmt.
func (g *htmlGen) addForStmtUpdater(elemID string, stmt *ast.ForStmt) {
	iterVar := stmt.Key
	indexVar := stmt.Value
	if indexVar == "" {
		indexVar = "index"
	}

	iterableJS := g.exprToJS(stmt.Iter)
	deps := g.exprDeps(stmt.Iter)

	// Generate inner HTML creation code from the for body
	var innerBuf strings.Builder
	g.emitForStmtBody(&innerBuf, stmt, iterVar, indexVar, elemID)

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

// addForElseStmtUpdater adds a display updater for the else branch of a for loop.
func (g *htmlGen) addForElseStmtUpdater(forElemID, elseElemID string, stmt *ast.ForStmt) {
	iterableJS := g.exprToJS(stmt.Iter)
	deps := g.exprDeps(stmt.Iter)

	name := fmt.Sprintf("$u_%s_else", forElemID[1:])
	body := fmt.Sprintf(`%s.style.display = %s.length === 0 ? "" : "none";`,
		elseElemID, iterableJS)

	g.updates = append(g.updates, updateFunc{
		funcName: name,
		body:     body,
		deps:     deps,
	})
}

// emitForStmtBody generates the inner HTML creation code for a v2 ForStmt.
func (g *htmlGen) emitForStmtBody(b *strings.Builder, stmt *ast.ForStmt, iterVar, indexVar, containerID string) {
	savedLocals := make(map[string]bool)
	maps.Copy(savedLocals, g.scope.LocalVars)
	g.scope.LocalVars[iterVar] = true
	g.scope.LocalVars[indexVar] = true
	defer func() { g.scope.LocalVars = savedLocals }()

	listFuncName := fmt.Sprintf("$u_%s_list", containerID[1:])
	_ = listFuncName

	for _, s := range stmt.Body.Stmts {
		if vn, ok := s.(*ast.VisualNode); ok {
			// Delegate to existing emitForLoopBody per-child
			g.emitForLoopBody(b, vn, iterVar, indexVar, containerID)
		}
	}
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
	switch vnName(vn) {
	case "checkbox":
		checked := "false"
		if v := vnProp(vn, "checked"); v != nil {
			checked = g.exprToJS(v)
		}
		label := `""`
		if v := vnProp(vn, "label"); v != nil {
			label = g.exprToJS(v)
		}
		style := g.buildCSSStyle(vn)

		fmt.Fprintf(b, "    const row = document.createElement(\"label\");\n")
		g.emitForLoopDataKey(b, "row", vn)
		style = htmlutil.AppendCSS(style, "display", "block")
		fmt.Fprintf(b, "    row.style.cssText = %q;\n", style)
		fmt.Fprintf(b, "    const cb = document.createElement(\"input\");\n")
		fmt.Fprintf(b, "    cb.type = \"checkbox\";\n")
		fmt.Fprintf(b, "    cb.checked = %s;\n", checked)

		// Change handler
		if changeEvt := vnEvent(vn, "change"); changeEvt != nil {
			if len(changeEvt.Body.Stmts) > 0 {
				stmts := g.lang.TranslateMutation(changeEvt.Body.Stmts[0], g.scope)
				mutated := codegen.MutatedFields(changeEvt.Body.Stmts[0])
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
		if v := vnProp(vn, "value"); v != nil {
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
	// TODO: Key is on ForStmt in v2, not on VisualNode — needs refactoring
}

// exprDeps extracts model field dependencies, remapping through dataRenames
// when inside a component scope so deps use promoted field names.
func (g *htmlGen) exprDeps(expr ast.Expr) map[string]bool {
	// Temporarily register original names so walkDeps can find them,
	// then remap to the promoted unique names.
	var added []string
	for orig := range g.dataRenames {
		if !g.dt.ModelFields[orig] {
			g.dt.ModelFields[orig] = true
			added = append(added, orig)
		}
	}
	deps := g.dt.ExprDeps(expr)
	for _, name := range added {
		delete(g.dt.ModelFields, name)
	}
	return g.remapMutated(deps, g.dataRenames)
}

// remapMutated applies rename mappings to a set of mutated field names.
func (g *htmlGen) remapMutated(mutated map[string]bool, renames map[string]string) map[string]bool {
	if len(renames) == 0 {
		return mutated
	}
	remapped := make(map[string]bool, len(mutated))
	for name := range mutated {
		if renamed, ok := renames[name]; ok {
			remapped[renamed] = true
		} else {
			remapped[name] = true
		}
	}
	return remapped
}

func (g *htmlGen) addClickHandler(elemID string, body ast.StmtBlock) {
	if len(body.Stmts) == 0 {
		return
	}
	var stmts []string
	var mutated map[string]bool
	for _, s := range body.Stmts {
		stmts = append(stmts, g.lang.TranslateMutation(s, g.scope)...)
		for k, v := range codegen.MutatedFields(s) {
			if mutated == nil {
				mutated = make(map[string]bool)
			}
			mutated[k] = v
		}
	}
	mutated = g.remapMutated(mutated, g.dataRenames)
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

func (g *htmlGen) addInputHandler(elemID string, body ast.StmtBlock) {
	if len(body.Stmts) == 0 {
		return
	}
	savedEvent := g.scope.EventVar
	g.scope.EventVar = "e.target"
	var stmts []string
	mutated := make(map[string]bool)
	for _, s := range body.Stmts {
		stmts = append(stmts, g.lang.TranslateMutation(s, g.scope)...)
		for k, v := range codegen.MutatedFields(s) {
			mutated[k] = v
		}
	}
	g.scope.EventVar = savedEvent
	mutated = g.remapMutated(mutated, g.dataRenames)
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

func (g *htmlGen) addChangeHandler(elemID string, body ast.StmtBlock) {
	if len(body.Stmts) == 0 {
		return
	}
	var stmts []string
	mutated := make(map[string]bool)
	for _, s := range body.Stmts {
		stmts = append(stmts, g.lang.TranslateMutation(s, g.scope)...)
		for k, v := range codegen.MutatedFields(s) {
			mutated[k] = v
		}
	}
	mutated = g.remapMutated(mutated, g.dataRenames)
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
	return htmlutil.BuildCSSStyle(vn)
}

// Expression evaluation helpers

func (g *htmlGen) emitJSFunc(b *strings.Builder, fn *ast.FuncDef) {
	// Build param list
	params := make([]string, len(fn.Params.Params))
	for i, p := range fn.Params.Params {
		params[i] = p.Name
	}
	paramStr := strings.Join(params, ", ")

	// Create a scope with function params as local vars
	funcScope := &codegen.ExprScope{
		ModelFields:    g.scope.ModelFields,
		ComputedFields: g.scope.ComputedFields,
		FuncNames:      g.scope.FuncNames,
		LocalVars:      make(map[string]bool),
	}
	for k := range g.scope.LocalVars {
		funcScope.LocalVars[k] = true
	}
	for _, p := range fn.Params.Params {
		funcScope.LocalVars[p.Name] = true
	}

	// Mangle dotted names for JS: int.sqrt → int_sqrt
	jsName := strings.ReplaceAll(fn.Name, ".", "_")

	if fn.Body != nil {
		// Single-expression function
		body := g.lang.TranslateExpr(fn.Body, funcScope)
		fmt.Fprintf(b, "function %s(%s) { return %s; }\n", jsName, paramStr, body)
	} else if len(fn.Block.Stmts) > 0 {
		fmt.Fprintf(b, "function %s(%s) {\n", jsName, paramStr)
		for _, stmt := range fn.Block.Stmts {
			switch s := stmt.(type) {
			case *ast.VarStmt:
				funcScope.LocalVars[s.Name] = true
				val := g.lang.TranslateExpr(s.Init, funcScope)
				fmt.Fprintf(b, "  let %s = %s;\n", s.Name, val)
			case *ast.ReturnStmt:
				if s.Value != nil {
					ret := g.lang.TranslateExpr(s.Value, funcScope)
					fmt.Fprintf(b, "  return %s;\n", ret)
				} else {
					b.WriteString("  return;\n")
				}
			default:
				stmts := g.lang.TranslateMutation(stmt, funcScope)
				for _, line := range stmts {
					fmt.Fprintf(b, "  %s;\n", line)
				}
			}
		}
		b.WriteString("}\n")
	}
}

func (g *htmlGen) exprToJS(expr ast.Expr) string {
	if exprIsReactive(expr) {
		return g.lang.TranslateExpr(expr, g.scope)
	}
	if exprIsLiteral(expr) {
		return g.lang.TranslateLiteral(expr)
	}
	return `""`
}

func (g *htmlGen) literalToJS(expr ast.Expr) string {
	if expr == nil {
		return `""`
	}
	if lit, ok := expr.(*ast.LiteralExpr); ok {
		switch lit.Kind {
		case ast.LiteralStringQuoted, ast.LiteralStringBackticked, ast.LiteralStringTrippleQuoted:
			if s, ok := exprLiteralString(expr); ok {
				return fmt.Sprintf("%q", s)
			}
		case ast.LiteralInt:
			return lit.Raw
		case ast.LiteralFloat:
			return lit.Raw
		case ast.LiteralBool:
			return lit.Raw
		case ast.LiteralNull:
			return "null"
		}
		return lit.Raw
	}
	// Non-literal expression — translate to JS
	return g.exprToJS(expr)
}

// complexLiteralToJS converts []any or map[string]any to JSON for embedding in JS.
func complexLiteralToJS(v any) string {
	data, err := json.Marshal(v)
	if err != nil {
		return "null"
	}
	return string(data)
}

func (g *htmlGen) evalStaticString(props map[string]ast.Expr, key string) string {
	if props == nil {
		return ""
	}
	v, ok := props[key]
	if !ok {
		return ""
	}
	if exprIsLiteral(v) {
		if s, ok := exprLiteralString(v); ok {
			return s
		}
		return fmt.Sprintf("%v", exprLiteralAny(v))
	}
	// For SNGL expressions, evaluate with initial state
	if exprIsReactive(v) {
		return g.evalInitialString(v)
	}
	return ""
}

func (g *htmlGen) evalInitialString(expr ast.Expr) string {
	if !exprIsReactive(expr) {
		return ""
	}
	// Translate the expression to JS and try to resolve it to a static value.
	js := g.exprToJS(expr)
	return g.resolveJSToString(js)
}

// resolveJSToString tries to evaluate a JS expression to a static string.
// It resolves component param references (e.g. "text_1") through their
// assigned values, and handles simple string literals and concatenation.
func (g *htmlGen) resolveJSToString(js string) string {
	// Direct string literal.
	if len(js) >= 2 && js[0] == '"' && js[len(js)-1] == '"' {
		if s, err := strconv.Unquote(js); err == nil {
			return s
		}
	}
	// Component param variable — look up its assigned value.
	for i := range g.componentParams {
		if g.componentParams[i].name == js {
			if resolved := g.resolveJSToString(g.componentParams[i].value); resolved != "" {
				g.componentParams[i].staticOnly = true
				return resolved
			}
			return ""
		}
	}
	// String concatenation: ("a" + "b") or "a" + b
	if parts := splitJSConcat(js); len(parts) > 1 {
		var sb strings.Builder
		for _, p := range parts {
			s := g.resolveJSToString(strings.TrimSpace(p))
			if s == "" {
				return "" // can't resolve a part
			}
			sb.WriteString(s)
		}
		return sb.String()
	}
	return ""
}

// splitJSConcat splits a JS expression on top-level " + " operators,
// respecting string quoting and parentheses.
func splitJSConcat(s string) []string {
	var parts []string
	depth := 0
	inStr := false
	start := 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '"':
			if !inStr {
				inStr = true
			} else if i > 0 && s[i-1] != '\\' {
				inStr = false
			}
		case '(':
			if !inStr {
				depth++
			}
		case ')':
			if !inStr {
				depth--
			}
		case '+':
			if !inStr && depth == 0 {
				parts = append(parts, s[start:i])
				start = i + 1
			}
		}
	}
	if start < len(s) {
		parts = append(parts, s[start:])
	}
	if len(parts) <= 1 {
		return nil
	}
	return parts
}

func (g *htmlGen) evalStaticBool(expr *ast.Expr) bool {
	if expr == nil {
		return true
	}
	if exprIsLiteral(*expr) {
		if v, ok := exprLiteralBool(*expr); ok {
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
	return exprIsReactive(v)
}

func (g *htmlGen) nodeIsReactive(vn *ast.VisualNode) bool {
	if vnHasEvents(vn) {
		return true
	}
	for _, v := range vnProps(vn) {
		if exprIsReactive(v) {
			return true
		}
	}
	return false
}

// extractSetTarget finds the first argument of an AssignStmt, which is the target being assigned.
func extractSetTarget(s ast.Stmt) (ast.Expr, bool) {
	if s == nil {
		return nil, false
	}
	switch n := s.(type) {
	case *ast.AssignStmt:
		return n.Target, true
	}
	return nil, false
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
	if exprIsLiteral(*key) {
		if s, ok := exprLiteralString(*key); ok {
			fmt.Fprintf(b, " data-key=%q", s)
		}
	}
}

// vnHasUserAttrs returns true if the visual node has user-specified id or class.
func (g *htmlGen) vnHasUserAttrs(vn *ast.VisualNode) bool {
	return vn.ID != "" || vnProp(vn, "class") != nil
}

// addUserAttrUpdaters adds DOM updaters for user-specified id and class attributes.
func (g *htmlGen) addUserAttrUpdaters(elemID string, vn *ast.VisualNode) {
	if vnProp(vn, "class") != nil {
		jsExpr := g.exprToJS(vnProp(vn, "class"))
		deps := g.exprDeps(vnProp(vn, "class"))
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
	if exprIsLiteral(vnProp(vn, "class")) {
		if s, ok := exprLiteralString(vnProp(vn, "class")); ok {
			fmt.Fprintf(b, " class=%q", s)
		}
	}
	// Emit data-sngl-id from the #id field for test element lookup
	if vn.ID != "" {
		fmt.Fprintf(b, " data-sngl-id=%q", vn.ID)
	}
}

func (g *htmlGen) previewAttrs(pos ast.Pos) string {
	if !g.preview || !pos.IsValid() {
		return ""
	}
	return fmt.Sprintf(" data-sngl-line=\"%d\" data-sngl-col=\"%d\"", pos.Line, pos.Column)
}
