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
	"strings"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/codegen"
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
func (g *Generator) ResolveAPI(name string) *ast.NativeDecls {
	return &ast.NativeDecls{}
}

func (g *Generator) Generate(req *codegen.Request) (*codegen.Response, error) {
	if req.Lang.Lang() != "js" {
		return &codegen.Response{Error: fmt.Sprintf("html: unsupported lang %q", req.Lang.Lang())}, nil
	}

	// Collect file assets (populated by optimizer from file:// path() calls).
	var files []*codegen.OutputFile
	for _, fa := range req.Doc.FileAssets {
		data, err := os.ReadFile(fa.SrcPath)
		if err == nil {
			files = append(files, codegen.BytesFile(fa.OutPath, data))
		}
	}

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
	// Each import scheme's language may provide a WASMCompiler.
	var wasmLoaderHTML string
	wasmPkgs := collectWASMPackages(req.Doc)
	if len(wasmPkgs) > 0 {
		projectDir := req.Options["projectDir"]
		wasmExecAdded := false
		var loaderScripts []string

		for _, wp := range wasmPkgs {
			// Look up the lang for this import's scheme (e.g., go:// → "go" lang)
			schemeLang := codegen.LookupLang(wp.namespace)
			// Try all registered langs for WASMCompiler support
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

			// Add runtime support JS (once)
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

	// Merge non-pure go:// extern functions into doc.Data for WASM bindings.
	// Pure functions were fully evaluated at compile time and don't need runtime bindings.
	for _, ni := range req.Doc.NativeImports {
		for _, d := range ni.Data {
			if d.IsFunc && d.Extern && d.Purity != ast.PurityPure && (d.Resolved == nil || d.Resolved.NativePkg != "file") {
				req.Doc.Data = append(req.Doc.Data, d)
			}
		}
	}

	// Generate HTML pages from windows (or single-window fallback).
	windows := req.Doc.App.EffectiveWindows()
	if len(windows) <= 1 {
		gen := newHTMLGen(req.Doc, req.Lang, req.Options)
		gen.wasmLoader = wasmLoaderHTML
		src := gen.generate()
		files = append(files, codegen.BytesFile("index.html", []byte(src)))
		return &codegen.Response{Files: files}, nil
	}
	for _, win := range windows {
		name := win.Name
		// Try href prop for the filename if no static name
		if name == "" {
			name = staticPropString(win.Props, "href")
		}
		if name == "" {
			name = staticPropString(win.Props, "slug")
		}
		if name == "" {
			name = staticPropString(win.Props, "title")
		}
		// If name looks like a URL path (starts with /), use it directly
		if strings.HasPrefix(name, "/") {
			name = strings.TrimPrefix(name, "/")
		} else if strings.HasSuffix(name, ".html") {
			// Already has extension, use as-is
		} else if name == "main" || name == "" || name == "index" {
			name = "index.html"
		} else {
			name = name + ".html"
		}
		// Create a synthetic document with this window's children as the app body.
		// Merge window-level state into the document so emitScript() can see it.
		winDoc := req.Doc.Clone()
		winDoc.App = &ast.App{Children: win.Children}
		winDoc.Data = append(winDoc.Data, win.Data...)
		winDoc.Consts = append(winDoc.Consts, win.Consts...)
		winDoc.Functions = append(winDoc.Functions, win.Functions...)
		winDoc.Timers = append(winDoc.Timers, win.Timers...)
		gen := newHTMLGen(winDoc, req.Lang, req.Options)
		gen.title = staticPropString(win.Props, "title")
		gen.stylesheet = stylesheetURL
		gen.favicon = staticPropString(win.Props, "favicon")
		gen.wasmLoader = wasmLoaderHTML
		src := gen.generate()
		files = append(files, codegen.BytesFile(name, []byte(src)))
	}
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
	if s, ok := e.Literal.(string); ok {
		return s
	}
	return ""
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
	dt           *codegen.DepTracker

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

	// Component nesting depth for recursion protection
	componentDepth int

	// Slot children: caller's children for abstract component body expansion
	slotChildren []*ast.VisualNode
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

type timerDef struct {
	index      int
	intervalMs int
	activeVar  string
	body       string
	mutated    map[string]bool
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
	for _, fn := range doc.Functions {
		if fn.Body.SNGL != nil && len(fn.Params) == 0 && !fn.IsStdlib {
			g.modelFields[fn.Name] = true
			g.computedFields[fn.Name] = true
		}
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
	funcNames := make(map[string]bool)
	for _, fn := range doc.Functions {
		funcNames[fn.Name] = true
	}
	g.scope = &codegen.ExprScope{
		ModelFields:    g.modelFields,
		ComputedFields: g.computedFields,
		FuncNames:      funcNames,
		LocalVars:      localVars,
		NeededHelpers:  make(map[string]bool),
	}

	// Compute dependency info for computed functions
	for _, fn := range doc.Functions {
		if fn.Body.SNGL != nil && len(fn.Params) == 0 && !fn.IsStdlib {
			g.computedDeps[fn.Name] = codegen.ExtractDeps(fn.Body.SNGL, g.modelFields)
		}
	}

	g.dt = codegen.NewDepTracker(g.modelFields, g.computedFields, g.computedDeps)

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
		b.WriteString("  <style>\n")
		b.WriteString("    * { margin: 0; padding: 0; box-sizing: border-box; }\n")
		b.WriteString("    body { font-family: system-ui, sans-serif; }\n")
		b.WriteString("    @keyframes sngl-spin { to { transform: rotate(360deg); } }\n")
		b.WriteString("    .sngl-spinner { display: inline-block; width: 1em; height: 1em; border: 2px solid currentColor; border-right-color: transparent; border-radius: 50%; animation: sngl-spin 0.75s linear infinite; vertical-align: middle; }\n")
		b.WriteString("  </style>\n")
	}
	if g.wasmLoader != "" {
		b.WriteString(g.wasmLoader)
	}
	b.WriteString("</head><body>\n\n")

	// Render static HTML body
	if g.doc.App != nil {
		for _, child := range g.doc.App.Children {
			g.renderStaticNode(&b, child, 0)
		}
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
		// Render else container (shown when list is empty)
		var elseID string
		if len(vn.For.Else) > 0 {
			elseID = g.allocID()
			fmt.Fprintf(b, "%s<div id=\"%s\">\n", indent, elseID)
			for _, elseNode := range vn.For.Else {
				g.renderStaticNode(b, elseNode, depth+1)
			}
			fmt.Fprintf(b, "%s</div>\n", indent)
		}
		g.addForUpdater(id, vn)
		if elseID != "" {
			g.addForElseUpdater(id, elseID, vn)
		}
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
		value := g.evalStaticString(vn.Props, "value")
		maxVal := g.evalStaticString(vn.Props, "max")
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
			if valExpr, ok := vn.Props["value"]; ok {
				g.addAttrUpdater(id, "value", valExpr)
			}
		}
	case "spinner":
		style := g.buildCSSStyle(vn)
		label := g.evalStaticString(vn.Props, "label")
		id := ""
		if g.preview {
			id = g.allocID()
		}
		g.writeOpenTag(b, "span", id, style, vn, depth, vn.Pos)
		fmt.Fprintf(b, "<span class=\"sngl-spinner\"></span> %s</span>\n", html.EscapeString(label))
	case "badge":
		style := g.buildCSSStyle(vn)
		style = appendCSS(style, "display", "inline-block")
		style = appendCSS(style, "padding", "2px 8px")
		style = appendCSS(style, "border-radius", "12px")
		style = appendCSS(style, "font-size", "12px")
		value := g.evalStaticString(vn.Props, "value")
		id := ""
		if g.nodeIsReactive(vn) || g.preview {
			id = g.allocID()
		}
		g.writeOpenTag(b, "span", id, style, vn, depth, vn.Pos)
		fmt.Fprintf(b, "%s</span>\n", html.EscapeString(value))
		if g.propIsReactive(vn.Props, "value") {
			g.addTextUpdater(id, vn.Props["value"])
		}
	case "tabs":
		g.renderStaticTabs(b, vn, depth)
	case "link":
		style := g.buildCSSStyle(vn)
		text := g.evalStaticString(vn.Props, "text")
		href := g.evalStaticString(vn.Props, "href")
		target := g.evalStaticString(vn.Props, "target")
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
		if evt, ok := vn.Events["click"]; ok && evt.SNGL != nil {
			if id == "" {
				id = g.allocID()
			}
			g.addClickHandler(id, evt)
		}
	case "divider":
		style := g.buildCSSStyle(vn)
		label := g.evalStaticString(vn.Props, "label")
		id := ""
		if g.preview {
			id = g.allocID()
		}
		if label != "" {
			style = appendCSS(style, "display", "flex")
			style = appendCSS(style, "align-items", "center")
			style = appendCSS(style, "gap", "8px")
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
		title := g.evalStaticString(vn.Props, "text")
		fmt.Fprintf(b, "%s<div title=\"%s\">\n", indent, html.EscapeString(title))
		for _, child := range vn.Children {
			g.renderStaticNode(b, child, depth+1)
		}
		fmt.Fprintf(b, "%s</div>\n", indent)
	case "popover":
		g.renderStaticConditionalContainer(b, vn, depth, "div")
	case "accordion":
		g.renderStaticAccordion(b, vn, depth)
	case "splitview":
		style := g.buildCSSStyle(vn)
		style = appendCSS(style, "display", "flex")
		direction := g.evalStaticString(vn.Props, "direction")
		if direction == "vertical" {
			style = appendCSS(style, "flex-direction", "column")
		} else {
			style = appendCSS(style, "flex-direction", "row")
		}
		id := ""
		if g.preview {
			id = g.allocID()
		}
		g.writeOpenTag(b, "div", id, style, vn, depth, vn.Pos)
		for i, child := range vn.Children {
			if i > 0 {
				fmt.Fprintf(b, "%s  <div style=\"width:4px;background:#ccc;cursor:col-resize\"></div>\n", indent)
			}
			fmt.Fprintf(b, "%s  <div style=\"flex:1;overflow:auto\">\n", indent)
			g.renderStaticNode(b, child, depth+2)
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
		style = appendCSS(style, "display", "flex")
		style = appendCSS(style, "gap", "4px")
		id := ""
		if g.preview {
			id = g.allocID()
		}
		g.writeOpenTag(b, "nav", id, style, vn, depth, vn.Pos)
		fmt.Fprintf(b, "</nav>\n")
	case "toolbar":
		style := g.buildCSSStyle(vn)
		style = appendCSS(style, "display", "flex")
		style = appendCSS(style, "gap", "4px")
		style = appendCSS(style, "align-items", "center")
		id := ""
		if g.preview {
			id = g.allocID()
		}
		g.writeOpenTag(b, "div", id, style, vn, depth, vn.Pos)
		for _, child := range vn.Children {
			g.renderStaticNode(b, child, depth+1)
		}
		fmt.Fprintf(b, "%s</div>\n", indent)

	// --- Tier 5: Mobile & Specialized ---
	case "datepicker":
		g.renderStaticDatepicker(b, vn, depth)
	case "chip":
		style := g.buildCSSStyle(vn)
		style = appendCSS(style, "display", "inline-flex")
		style = appendCSS(style, "align-items", "center")
		style = appendCSS(style, "padding", "4px 12px")
		style = appendCSS(style, "border-radius", "16px")
		style = appendCSS(style, "border", "1px solid #ccc")
		style = appendCSS(style, "font-size", "14px")
		label := g.evalStaticString(vn.Props, "label")
		id := ""
		if g.nodeIsReactive(vn) || g.preview {
			id = g.allocID()
		}
		g.writeOpenTag(b, "span", id, style, vn, depth, vn.Pos)
		fmt.Fprintf(b, "%s", html.EscapeString(label))
		b.WriteString("</span>\n")
	case "avatar":
		style := g.buildCSSStyle(vn)
		style = appendCSS(style, "display", "inline-flex")
		style = appendCSS(style, "align-items", "center")
		style = appendCSS(style, "justify-content", "center")
		style = appendCSS(style, "border-radius", "50%")
		style = appendCSS(style, "width", "40px")
		style = appendCSS(style, "height", "40px")
		style = appendCSS(style, "background", "#ccc")
		style = appendCSS(style, "font-weight", "bold")
		initials := g.evalStaticString(vn.Props, "initials")
		if initials == "" {
			initials = g.evalStaticString(vn.Props, "alt")
		}
		id := ""
		if g.preview {
			id = g.allocID()
		}
		g.writeOpenTag(b, "div", id, style, vn, depth, vn.Pos)
		fmt.Fprintf(b, "%s</div>\n", html.EscapeString(initials))
	case "card":
		style := g.buildCSSStyle(vn)
		variant := g.evalStaticString(vn.Props, "variant")
		switch variant {
		case "elevated":
			style = appendCSS(style, "box-shadow", "0 2px 8px rgba(0,0,0,0.15)")
		case "filled":
			style = appendCSS(style, "background", "#f5f5f5")
		default: // outlined
			style = appendCSS(style, "border", "1px solid #ddd")
		}
		style = appendCSS(style, "border-radius", "8px")
		style = appendCSS(style, "padding", "16px")
		id := ""
		if g.nodeIsReactive(vn) || g.preview {
			id = g.allocID()
		}
		g.writeOpenTag(b, "div", id, style, vn, depth, vn.Pos)
		for _, child := range vn.Children {
			g.renderStaticNode(b, child, depth+1)
		}
		fmt.Fprintf(b, "%s</div>\n", indent)

	case "slot":
		// Project caller's children into this position
		for _, child := range g.slotChildren {
			g.renderStaticNode(b, child, depth)
		}

	default:
		// User-defined or abstract component — inline at call site
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

	if v, ok := vn.Props["scroll"]; ok {
		if b, ok := v.Literal.(bool); ok && b {
			if vertical {
				style = appendCSS(style, "overflow-y", "auto")
			} else {
				style = appendCSS(style, "overflow-x", "auto")
			}
		}
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
	b.WriteString(html.EscapeString(val))
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
	fmt.Fprintf(b, "%s>%s</button>\n", disabled, html.EscapeString(text))

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
		fmt.Fprintf(b, " placeholder=\"%s\"", html.EscapeString(placeholder))
	}
	fmt.Fprintf(b, " value=\"%s\"", html.EscapeString(value))
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
	fmt.Fprintf(b, " %s</label>\n", html.EscapeString(label))

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
			fmt.Fprintf(b, " src=\"%s\"", html.EscapeString(src))
		}
		fmt.Fprintf(b, " alt=\"%s\"", html.EscapeString(alt))
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
		fmt.Fprintf(b, " src=\"%s\"", html.EscapeString(src))
	}
	fmt.Fprintf(b, " alt=\"%s\"", html.EscapeString(alt))
	if style != "" {
		fmt.Fprintf(b, " style=\"%s\"", style)
	}
	g.writeDataKey(b, vn.Key)
	g.writeUserAttrs(b, "", vn)
	b.WriteString(" />\n")
}

// --- New component renderers ---

func (g *htmlGen) renderStaticRadio(b *strings.Builder, vn *ast.VisualNode, depth int) {
	style := g.buildCSSStyle(vn)
	id := g.allocID()
	indent := strings.Repeat("  ", depth)
	g.writeOpenTag(b, "fieldset", id, style, vn, depth, vn.Pos)
	// Static options rendered if literal
	if v, ok := vn.Props["options"]; ok && v.Literal != nil {
		value := g.evalStaticString(vn.Props, "value")
		if items, ok := v.Literal.([]any); ok {
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
	if evt, ok := vn.Events["change"]; ok && evt.SNGL != nil {
		g.addChangeHandler(id, evt)
	}
}

func (g *htmlGen) renderStaticToggle(b *strings.Builder, vn *ast.VisualNode, depth int) {
	style := g.buildCSSStyle(vn)
	id := g.allocID()
	checked := false
	if v, ok := vn.Props["checked"]; ok {
		checked = g.evalStaticBool(&v)
	}
	label := g.evalStaticString(vn.Props, "label")
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
	if evt, ok := vn.Events["change"]; ok && evt.SNGL != nil {
		g.addChangeHandler(id, evt)
	}
}

func (g *htmlGen) renderStaticSelect(b *strings.Builder, vn *ast.VisualNode, depth int) {
	style := g.buildCSSStyle(vn)
	id := g.allocID()
	indent := strings.Repeat("  ", depth)
	value := g.evalStaticString(vn.Props, "value")
	placeholder := g.evalStaticString(vn.Props, "placeholder")
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
	if v, ok := vn.Props["options"]; ok && v.Literal != nil {
		if items, ok := v.Literal.([]any); ok {
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
	if evt, ok := vn.Events["change"]; ok && evt.SNGL != nil {
		g.addChangeHandler(id, evt)
	}
}

func (g *htmlGen) renderStaticTextarea(b *strings.Builder, vn *ast.VisualNode, depth int) {
	style := g.buildCSSStyle(vn)
	id := g.allocID()
	indent := strings.Repeat("  ", depth)
	value := g.evalStaticString(vn.Props, "value")
	placeholder := g.evalStaticString(vn.Props, "placeholder")
	rows := "3"
	if v, ok := vn.Props["rows"]; ok {
		if n, ok := v.Literal.(int); ok {
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
	if evt, ok := vn.Events["input"]; ok && evt.SNGL != nil {
		g.addInputHandler(id, evt)
	}
}

func (g *htmlGen) renderStaticTabs(b *strings.Builder, vn *ast.VisualNode, depth int) {
	style := g.buildCSSStyle(vn)
	id := g.allocID()
	indent := strings.Repeat("  ", depth)
	g.writeOpenTag(b, "div", id, style, vn, depth, vn.Pos)
	// Tab bar
	fmt.Fprintf(b, "%s  <div role=\"tablist\" style=\"display:flex;gap:4px;border-bottom:1px solid #ccc\">\n", indent)
	if v, ok := vn.Props["items"]; ok && v.Literal != nil {
		selected := 0
		if sv, ok := vn.Props["selected"]; ok {
			if n, ok := sv.Literal.(int); ok {
				selected = n
			}
		}
		if items, ok := v.Literal.([]any); ok {
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
	for _, child := range vn.Children {
		g.renderStaticNode(b, child, depth+1)
	}
	fmt.Fprintf(b, "%s</div>\n", indent)
}

func (g *htmlGen) renderStaticModal(b *strings.Builder, vn *ast.VisualNode, depth int) {
	style := g.buildCSSStyle(vn)
	id := g.allocID()
	indent := strings.Repeat("  ", depth)
	open := true
	if v, ok := vn.Props["open"]; ok {
		open = g.evalStaticBool(&v)
	}
	display := ""
	if !open {
		display = "display:none;"
	}
	title := g.evalStaticString(vn.Props, "title")
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
		contentStyle = appendCSS(contentStyle, "background", "#fff")
		contentStyle = appendCSS(contentStyle, "border-radius", "8px")
		contentStyle = appendCSS(contentStyle, "padding", "24px")
	}
	fmt.Fprintf(b, "%s  <div style=\"%s\">\n", indent, contentStyle)
	if title != "" {
		fmt.Fprintf(b, "%s    <h3 style=\"margin:0 0 16px\">%s</h3>\n", indent, html.EscapeString(title))
	}
	for _, child := range vn.Children {
		g.renderStaticNode(b, child, depth+2)
	}
	fmt.Fprintf(b, "%s  </div>\n", indent)
	fmt.Fprintf(b, "%s</div>\n", indent)
	if g.propIsReactive(vn.Props, "open") {
		g.addIfUpdater(id, vn.Props["open"])
	}
}

func (g *htmlGen) renderStaticConditionalContainer(b *strings.Builder, vn *ast.VisualNode, depth int, tag string) {
	style := g.buildCSSStyle(vn)
	id := g.allocID()
	indent := strings.Repeat("  ", depth)
	open := true
	if v, ok := vn.Props["open"]; ok {
		open = g.evalStaticBool(&v)
	}
	if v, ok := vn.Props["visible"]; ok {
		open = g.evalStaticBool(&v)
	}
	if !open {
		style = appendCSS(style, "display", "none")
	}
	g.writeOpenTag(b, tag, id, style, vn, depth, vn.Pos)
	for _, child := range vn.Children {
		g.renderStaticNode(b, child, depth+1)
	}
	fmt.Fprintf(b, "%s</%s>\n", indent, tag)
	if g.propIsReactive(vn.Props, "open") {
		g.addIfUpdater(id, vn.Props["open"])
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
	if v, ok := vn.Props["items"]; ok && v.Literal != nil {
		if items, ok := v.Literal.([]any); ok {
			for i, item := range items {
				s := fmt.Sprint(item)
				fmt.Fprintf(b, "%s  <details>\n", indent)
				fmt.Fprintf(b, "%s    <summary>%s</summary>\n", indent, html.EscapeString(s))
				if i < len(vn.Children) {
					g.renderStaticNode(b, vn.Children[i], depth+2)
				}
				fmt.Fprintf(b, "%s  </details>\n", indent)
			}
		}
	}
	fmt.Fprintf(b, "%s</div>\n", indent)
}

func (g *htmlGen) renderStaticTable(b *strings.Builder, vn *ast.VisualNode, depth int) {
	style := g.buildCSSStyle(vn)
	style = appendCSS(style, "border-collapse", "collapse")
	style = appendCSS(style, "width", "100%")
	id := ""
	if g.nodeIsReactive(vn) || g.preview {
		id = g.allocID()
	}
	indent := strings.Repeat("  ", depth)
	g.writeOpenTag(b, "table", id, style, vn, depth, vn.Pos)
	// Header
	if v, ok := vn.Props["columns"]; ok && v.Literal != nil {
		if cols, ok := v.Literal.([]any); ok {
			fmt.Fprintf(b, "%s  <thead><tr>\n", indent)
			for _, col := range cols {
				fmt.Fprintf(b, "%s    <th style=\"text-align:left;padding:8px;border-bottom:2px solid #ddd\">%s</th>\n", indent, html.EscapeString(fmt.Sprint(col)))
			}
			fmt.Fprintf(b, "%s  </tr></thead>\n", indent)
		}
	}
	// Body
	if v, ok := vn.Props["rows"]; ok && v.Literal != nil {
		if rows, ok := v.Literal.([]any); ok {
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
	style = appendCSS(style, "list-style", "none")
	style = appendCSS(style, "padding-left", "16px")
	g.writeOpenTag(b, "ul", id, style, vn, depth, vn.Pos)
	if v, ok := vn.Props["items"]; ok && v.Literal != nil {
		if items, ok := v.Literal.([]any); ok {
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
	value := g.evalStaticString(vn.Props, "value")
	placeholder := g.evalStaticString(vn.Props, "placeholder")
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
	if evt, ok := vn.Events["change"]; ok && evt.SNGL != nil {
		g.addChangeHandler(id, evt)
	}
}

const maxComponentDepth = 10

// renderRawElement renders a VisualNode as a raw HTML element.
// The component name (or its local part for qualified names) is used as the tag.
func (g *htmlGen) renderRawElement(b *strings.Builder, vn *ast.VisualNode, depth int) {
	indent := strings.Repeat("  ", depth)
	// Extract tag name: "html.div" → "div", "button" → "button"
	tag := vn.Component
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
	for name, expr := range vn.Props {
		if name == "style" {
			continue
		}
		if expr.SNGL == nil {
			val := g.evalStaticString(vn.Props, name)
			switch name {
			case "innerText":
				staticInnerText = val
			case "innerHTML":
				staticInnerHTML = val
			default:
				if bv, ok := expr.Literal.(bool); ok {
					if bv {
						fmt.Fprintf(&attrs, " %s", html.EscapeString(name))
					}
				} else if expr.Literal != nil {
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
	if vn.Class != nil && vn.Class.Literal != nil {
		if s, ok := vn.Class.Literal.(string); ok && s != "" {
			fmt.Fprintf(b, " class=%q", s)
		}
	}
	if style != "" {
		fmt.Fprintf(b, " style=\"%s\"", style)
	}
	b.WriteString(attrs.String())
	if vn != nil {
		g.writeDataKey(b, vn.Key)
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
			for _, child := range vn.Children {
				g.renderStaticNode(b, child, depth+1)
			}
			fmt.Fprintf(b, "%s</%s>\n", indent, tag)
		}
	}

	if id == "" {
		return
	}

	// Reactive props: set via JS updaters
	for name, expr := range vn.Props {
		if name == "style" {
			continue
		}
		if expr.SNGL != nil {
			jsVal := g.exprToJS(expr)
			deps := g.dt.ExprDeps(expr)
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
	for name, expr := range vn.Events {
		switch name {
		case "click":
			g.addClickHandler(id, expr)
		case "input":
			g.addInputHandler(id, expr)
		case "change":
			g.addChangeHandler(id, expr)
		default:
			g.addClickHandler(id, expr) // fallback
		}
	}
}

func (g *htmlGen) renderStaticUserComponent(b *strings.Builder, vn *ast.VisualNode, depth int) {
	// Find the component definition
	comp := g.doc.FindComponent(vn.Component)
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
	// Handle component computed functions: rename and register as local
	for _, fn := range comp.Functions {
		if fn.Body.SNGL != nil && len(fn.Params) == 0 && !fn.IsStdlib {
			uniqueName := fn.Name + suffix
			g.scope.LocalVars[fn.Name] = true
			renames[fn.Name] = uniqueName
		}
	}
	g.scope.Renames = renames

	// Emit computed functions with renamed references
	for _, fn := range comp.Functions {
		if fn.Body.SNGL != nil && len(fn.Params) == 0 && !fn.IsStdlib {
			uniqueName := fn.Name + suffix
			body := g.exprToJS(fn.Body)
			g.componentParams = append(g.componentParams, componentParam{
				name:  uniqueName,
				value: body,
			})
		}
	}

	// Set slot children for abstract component body expansion
	savedSlot := g.slotChildren
	g.slotChildren = vn.Children

	// Inline the component body at the call site
	for _, child := range comp.Body {
		g.renderStaticNode(b, child, depth)
	}

	g.slotChildren = savedSlot

	// Restore locals
	g.scope.LocalVars = savedLocals
	g.scope.Renames = savedRenames
}

// emitScript writes the <script> block content.
func (g *htmlGen) emitScript(b *strings.Builder) {
	// Collect timers
	for i, t := range g.doc.Timers {
		ms := g.intervalToMs(t.Interval)
		stmts := g.lang.TranslateMutation(t.Body, g.scope)
		mutated := codegen.MutatedFields(t.Body)
		g.timers = append(g.timers, timerDef{
			index:      i,
			intervalMs: ms,
			activeVar:  t.Active,
			body:       strings.Join(stmts, "\n  "),
			mutated:    mutated,
		})
	}

	// Extern bindings (provided by host via window.__sngl_externs or WASM)
	hasExterns := false
	for _, d := range g.doc.Data {
		if d.Extern || d.IsFunc {
			hasExterns = true
			break
		}
	}
	if hasExterns {
		b.WriteString("// Extern bindings\nconst $ext = window.__sngl_externs || {};\n")

		// Group go:// extern functions by namespace for qualified access (e.g., purepkg.Greet)
		nsByName := map[string][]string{} // namespace → function names
		for ns, ni := range g.doc.NativeImports {
			for _, d := range ni.Data {
				if d.IsFunc && d.Extern && d.Purity != ast.PurityPure && (d.Resolved == nil || d.Resolved.NativePkg != "file") {
					nsByName[ns] = append(nsByName[ns], d.Name)
				}
			}
		}
		for ns, funcs := range nsByName {
			fmt.Fprintf(b, "const %s = {\n", ns)
			for _, fn := range funcs {
				fmt.Fprintf(b, "  %s: $ext.%s || function(){},\n", fn, fn)
			}
			b.WriteString("};\n")
		}

		// Non-namespaced extern functions (direct doc.Data externs)
		for _, d := range g.doc.Data {
			if !d.Extern || !d.IsFunc {
				continue
			}
			// Skip if already in a namespace
			inNS := false
			for _, funcs := range nsByName {
				for _, fn := range funcs {
					if fn == d.Name {
						inNS = true
					}
				}
			}
			if !inNS {
				fmt.Fprintf(b, "const %s = $ext.%s || function(){};\n", d.Name, d.Name)
			}
		}
		b.WriteString("\n")
	}

	// State initialization
	b.WriteString("// State\nlet state = {")
	var stateFields []string
	for _, d := range g.doc.Data {
		if d.IsFunc {
			continue
		}
		val := g.literalToJS(d.Init)
		if d.Extern {
			val = fmt.Sprintf("$ext.%s", d.Name)
		}
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

	// Computed functions (zero-arg expression-form)
	hasComputed := false
	for _, fn := range g.doc.Functions {
		if fn.Body.SNGL != nil && len(fn.Params) == 0 && !fn.IsStdlib {
			body := g.exprToJS(fn.Body)
			fmt.Fprintf(b, "function $%s() { return %s; }\n", fn.Name, body)
			hasComputed = true
		}
	}
	if hasComputed {
		b.WriteString("\n")
	}

	// User-defined functions (skip stdlib — codegens use native calls)
	emittedFuncs := false
	for _, fn := range g.doc.Functions {
		if fn.IsStdlib {
			continue
		}
		g.emitJSFunc(b, fn)
		emittedFuncs = true
	}
	if emittedFuncs {
		b.WriteString("\n")
	}

	// Setters — only emit for fields with triggers, timer controls, or in preview mode
	for _, d := range g.doc.Data {
		if d.IsFunc {
			continue
		}
		needsSetter := d.Trigger != "" || g.preview
		if !needsSetter {
			for _, t := range g.timers {
				if t.activeVar == d.Name {
					needsSetter = true
					break
				}
			}
		}
		if !needsSetter {
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
		// Sync timers controlled by this var
		for _, t := range g.timers {
			if t.activeVar == d.Name {
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

	// Helper functions — only emit if used
	if g.scope.NeededHelpers["String"] {
		b.WriteString("function String(v) { return \"\" + v; }\n\n")
	}

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
	deps := g.dt.ExprDeps(expr)
	name := fmt.Sprintf("$u_%s_text", elemID[1:])
	g.updates = append(g.updates, updateFunc{
		funcName: name,
		body:     fmt.Sprintf("%s.textContent = %s;", elemID, jsExpr),
		deps:     deps,
	})
}

func (g *htmlGen) addTextContentUpdater(elemID string, expr ast.Expr) {
	jsExpr := g.exprToJS(expr)
	deps := g.dt.ExprDeps(expr)
	name := fmt.Sprintf("$u_%s_text", elemID[1:])
	g.updates = append(g.updates, updateFunc{
		funcName: name,
		body:     fmt.Sprintf("%s.textContent = %s;", elemID, jsExpr),
		deps:     deps,
	})
}

func (g *htmlGen) addAttrUpdater(elemID, attr string, expr ast.Expr) {
	jsExpr := g.exprToJS(expr)
	deps := g.dt.ExprDeps(expr)
	name := fmt.Sprintf("$u_%s_%s", elemID[1:], attr)
	g.updates = append(g.updates, updateFunc{
		funcName: name,
		body:     fmt.Sprintf("%s.setAttribute(%q, %s);", elemID, attr, jsExpr),
		deps:     deps,
	})
}

func (g *htmlGen) addDisabledUpdater(elemID string, expr ast.Expr) {
	jsExpr := g.exprToJS(expr)
	deps := g.dt.ExprDeps(expr)
	name := fmt.Sprintf("$u_%s_disabled", elemID[1:])
	g.updates = append(g.updates, updateFunc{
		funcName: name,
		body:     fmt.Sprintf("%s.disabled = %s;", elemID, jsExpr),
		deps:     deps,
	})
}

func (g *htmlGen) addIfUpdater(elemID string, expr ast.Expr) {
	jsExpr := g.exprToJS(expr)
	deps := g.dt.ExprDeps(expr)
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
	deps := g.dt.ExprDeps(vn.For.Iterable)

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

func (g *htmlGen) addForElseUpdater(forElemID, elseElemID string, vn *ast.VisualNode) {
	iterableJS := g.exprToJS(vn.For.Iterable)
	deps := g.dt.ExprDeps(vn.For.Iterable)

	name := fmt.Sprintf("$u_%s_else", forElemID[1:])
	body := fmt.Sprintf(`%s.style.display = %s.length === 0 ? "" : "none";`,
		elseElemID, iterableJS)

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
				mutated := codegen.MutatedFields(changeEvt.SNGL)
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
	mutated := codegen.MutatedFields(expr.SNGL)
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
	mutated := codegen.MutatedFields(expr.SNGL)
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
	mutated := codegen.MutatedFields(expr.SNGL)
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
	merged := vn.StyleFields()
	if merged == nil {
		merged = make(map[string]ast.Expr)
	}

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
	// Check folded literal first
	if expr.Literal != nil {
		return literalToString(expr.Literal)
	}
	// Check SNGL literal expression (not yet folded)
	if lit, ok := expr.SNGL.(*ast.LiteralExpr); ok {
		return literalToString(lit.Value)
	}
	return ""
}

func literalToString(v any) string {
	switch val := v.(type) {
	case string:
		return val
	case int:
		return fmt.Sprintf("%d", val)
	case float64:
		return fmt.Sprintf("%v", val)
	case bool:
		if val {
			return "true"
		}
		return "false"
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

func (g *htmlGen) emitJSFunc(b *strings.Builder, fn *ast.FuncDef) {
	// Build param list
	params := make([]string, len(fn.Params))
	for i, p := range fn.Params {
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
	for _, p := range fn.Params {
		funcScope.LocalVars[p.Name] = true
	}

	// Mangle dotted names for JS: int.sqrt → int_sqrt
	jsName := strings.ReplaceAll(fn.Name, ".", "_")

	if fn.Body.SNGL != nil {
		// Single-expression function
		body := g.lang.TranslateExpr(fn.Body.SNGL, funcScope)
		fmt.Fprintf(b, "function %s(%s) { return %s; }\n", jsName, paramStr, body)
	} else if fn.Block != nil {
		fmt.Fprintf(b, "function %s(%s) {\n", jsName, paramStr)
		for _, stmt := range fn.Block.Stmts {
			switch s := stmt.(type) {
			case *ast.VarStmt:
				funcScope.LocalVars[s.Name] = true
				val := g.lang.TranslateExpr(s.Init, funcScope)
				fmt.Fprintf(b, "  let %s = %s;\n", s.Name, val)
			default:
				stmts := g.lang.TranslateMutation(stmt, funcScope)
				for _, line := range stmts {
					fmt.Fprintf(b, "  %s;\n", line)
				}
			}
		}
		if fn.Block.Return != nil {
			ret := g.lang.TranslateExpr(fn.Block.Return, funcScope)
			fmt.Fprintf(b, "  return %s;\n", ret)
		}
		b.WriteString("}\n")
	}
}

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
		case []any:
			return complexLiteralToJS(v)
		case map[string]any:
			return complexLiteralToJS(v)
		}
	}
	// SNGL init expression (e.g., struct literal)
	if expr.SNGL != nil {
		// null literal on a list type → empty array (avoids null.length errors)
		if lit, ok := expr.SNGL.(*ast.LiteralExpr); ok && lit.Kind == ast.LiteralNull {
			if strings.HasPrefix(expr.TypeHint, "list:") || strings.HasPrefix(expr.TypeHint, "[]") {
				return "[]"
			}
		}
		return g.exprToJS(expr)
	}
	if strings.HasPrefix(expr.TypeHint, "list:") || strings.HasPrefix(expr.TypeHint, "[]") {
		return "[]"
	}
	return `""`
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

// intervalToMs extracts the interval from a timer expression and converts to milliseconds.
func (g *htmlGen) intervalToMs(expr ast.Expr) int {
	if expr.SNGL == nil {
		return 0
	}
	lit, ok := expr.SNGL.(*ast.LiteralExpr)
	if !ok || lit.Kind != ast.LiteralUnit {
		return 0
	}
	ul, ok := lit.Value.(ast.UnitLiteral)
	if !ok {
		return 0
	}
	num := 0.0
	fmt.Sscanf(ul.Number, "%f", &num)
	switch ul.Suffix {
	case "ms":
		return int(num)
	case "s":
		return int(num * 1000)
	case "m":
		return int(num * 60000)
	case "h":
		return int(num * 3600000)
	}
	return int(num) // fallback: treat as ms
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
	return vn.ID != "" || vn.Class != nil
}

// addUserAttrUpdaters adds DOM updaters for user-specified id and class attributes.
func (g *htmlGen) addUserAttrUpdaters(elemID string, vn *ast.VisualNode) {
	if vn.Class != nil {
		jsExpr := g.exprToJS(*vn.Class)
		deps := g.dt.ExprDeps(*vn.Class)
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
