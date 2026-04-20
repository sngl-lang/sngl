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
	"git.duckfam.us/jonathan/sngl/internal/htmlutil"
	"git.duckfam.us/jonathan/sngl/internal/parser"
	"git.duckfam.us/jonathan/sngl/ir"
)

//go:embed html.sngl
var pkgSource string

var pkgDocs []*ast.Document

func init() {
	doc, _ := parser.Parse("html.sngl", []byte(pkgSource))
	if doc != nil {
		pkgDocs = []*ast.Document{doc}
	}
	codegen.RegisterPlatform(&Generator{})
}

// Generator implements codegen.PlatformGenerator for HTML output.
type Generator struct{}

func (g *Generator) PlatformIdentifier() string { return "html" }
func (g *Generator) SupportedLangs() []string   { return []string{"js"} }
func (g *Generator) Package() []*ast.Document   { return pkgDocs }
func (g *Generator) Resolve(identifier string) ir.Symbol {
	// HTML accepts any tag name as a valid element.
	return &ir.Component{Name: identifier}
}
func (g *Generator) IsLanguageSupported(l ir.Language) bool { return l.LanguageIdentifier() == "js" }

func (g *Generator) Generate(req *codegen.Request) (*codegen.Response, error) {
	if req.Lang.LanguageIdentifier() != "js" {
		return &codegen.Response{Error: fmt.Sprintf("html: unsupported lang %q", req.Lang.LanguageIdentifier())}, nil
	}

	var files []*codegen.OutputFile

	// Copy file:// assets resolved during optimization.
	for _, fa := range req.FileAssets {
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
	var wasmLoaderHTML string
	wasmPkgs := collectWASMPackages(req.Pkg)
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
	ctx := codegen.NewCodegenCtx(req, "html")

	// IR-driven rendering: one file per window. Packages with no main
	// component and no explicit windows still emit an empty index.html so
	// callers can verify codegen at least succeeded.
	irWindows := ctx.Windows()
	if len(irWindows) == 0 {
		gen := newHTMLGenFromCtx(ctx, req.Lang, req.Options)
		gen.wasmLoader = wasmLoaderHTML
		gen.stylesheet = stylesheetURL
		src := codegen.Header("html", req.Source, "<!-- ", " -->") + gen.generate()
		files = append(files, codegen.BytesFile("index.html", []byte(src)))
		return &codegen.Response{Files: files}, nil
	}
	singleWindow := len(irWindows) == 1
	for _, win := range irWindows {
		name := win.Name
		if singleWindow || name == "" || name == "main" || name == "index" {
			name = "index.html"
		} else if !strings.HasSuffix(name, ".html") {
			name = name + ".html"
		}
		gen := newHTMLGenFromCtx(ctx, req.Lang, req.Options)
		gen.wasmLoader = wasmLoaderHTML
		gen.stylesheet = stylesheetURL
		gen.irBodyStmts = win.Body
		if win.Window != nil {
			if s, ok := codegen.IRLiteralString(win.Window.Title); ok {
				gen.title = s
			}
			if s, ok := codegen.IRLiteralString(win.Window.Favicon); ok {
				gen.favicon = s
			}
		}
		src := codegen.Header("html", req.Source, "<!-- ", " -->") + gen.generate()
		files = append(files, codegen.BytesFile(name, []byte(src)))
	}
	return &codegen.Response{Files: files}, nil
}

// htmlGen holds all state for generating a single HTML file.
type htmlGen struct {
	lang codegen.LangTranslator

	// Type-checked package and expression context (IR-driven).
	pkg        *ir.Package
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

	// irSlotChildren holds the caller's children during component inlining;
	// the body-level `slot` pseudo-element projects them into position.
	irSlotChildren []ir.Stmt

	// irBodyStmts is the IR body rendered for the current window.
	irBodyStmts []ir.Stmt
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

func newHTMLGen(pkg *ir.Package, lang codegen.LangTranslator, opts map[string]string) *htmlGen {
	common := codegen.AnalyzeCommon(pkg)

	g := &htmlGen{
		pkg:            pkg,
		lang:           lang,
		CommonAnalysis: common,
		preview:        opts["preview"] == "true",
		testMode:       opts["test"] == "true",
	}

	g.scope = &codegen.ExprScope{
		ModelFields:    common.ModelFields,
		ComputedFields: common.ComputedFields,
		FuncNames:      common.FuncNames,
		ExternFuncs:    common.ExternFuncs,
		ExternVars:     common.ExternVars,
		LocalVars:      make(map[string]bool),
		NeededHelpers:  common.Helpers,
	}
	if pkg != nil {
		for _, c := range pkg.Consts {
			g.scope.LocalVars[c.Name] = true
		}
	}

	g.dt = common.DepTracker()
	g.ctx = codegen.NewExprCtx(pkg)

	return g
}

// newHTMLGenFromCtx creates an htmlGen from CodegenCtx (IR-first path).
func newHTMLGenFromCtx(ctx *codegen.CodegenCtx, lang codegen.LangTranslator, opts map[string]string) *htmlGen {
	g := newHTMLGen(ctx.Pkg, lang, opts)
	if main := ctx.MainComponent(); main != nil {
		g.irBodyStmts = main.Body
		g.ctx = ctx.ExprCtx.ForComponent(main)
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

	// Render static HTML body via IR dispatch.
	for _, s := range g.irBodyStmts {
		g.renderIRStmt(&b, s, 0)
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

// renderIRStmt is the IR-driven top-level dispatch. Structural statements
// (If/For/PlatformFilter/Slot/user-component inlining) are walked over IR;
// leaf NodeInst rendering currently delegates to the AST helpers via the
// original n.AST back-reference. Per-element helpers will be ported to IR
// incrementally.
func (g *htmlGen) renderIRStmt(b *strings.Builder, s ir.Stmt, depth int) {
	switch n := s.(type) {
	case *ir.NodeInst:
		g.renderIRNode(b, n, depth)
	case *ir.If:
		g.renderIRIf(b, n, depth)
	case *ir.For:
		g.renderIRFor(b, n, depth)
	case *ir.PlatformFilter:
		if n.Platform != "html" {
			return
		}
		for _, bs := range n.Body {
			g.renderIRStmt(b, bs, depth)
		}
	case *ir.SlotInst:
		for _, child := range g.irSlotChildren {
			g.renderIRStmt(b, child, depth)
		}
	case *ir.CallStmt:
		if syn := nodeFromIRCallStmt(n); syn != nil {
			g.renderIRNode(b, syn, depth)
		}
	}
}

// nodeFromIRCallStmt promotes an ir.CallStmt that targets a component
// (platform-resolved raw element or namespaced user component) into a
// synthetic ir.NodeInst so it can be rendered like any other visual
// statement. Returns nil for plain function calls.
func nodeFromIRCallStmt(n *ir.CallStmt) *ir.NodeInst {
	if n == nil || n.Call == nil {
		return nil
	}
	var name string
	switch f := n.Call.AST.Func.(type) {
	case *ast.IdentExpr:
		name = f.Name
	case *ast.SelectExpr:
		if id, ok := f.Operand.(*ast.IdentExpr); ok {
			name = id.Name + "." + f.Field
		}
	}
	if name == "" {
		return nil
	}
	props := make([]ir.Arg, 0, len(n.Call.Args))
	for _, a := range n.Call.Args {
		props = append(props, ir.Arg{Name: a.Name, Value: a.Value})
	}
	syn := &ir.NodeInst{
		AST:   n.AST,
		Name:  name,
		Props: props,
	}
	if f := n.Call.Func; f != nil && f.Receiver == "" {
		// no component resolution via call-side Func (Func is set only for
		// resolved function calls); Component stays nil → raw/stdlib path.
	}
	return syn
}

// renderIRNode dispatches a NodeInst to the appropriate renderer.
// User components (components with a source AST declaration) inline via
// their IR body; stdlib components still go through their AST-backed
// per-element helpers (port pending); raw/platform-resolved elements
// (html.div, html.img, arbitrary tags) are rendered IR-native.
func (g *htmlGen) renderIRNode(b *strings.Builder, n *ir.NodeInst, depth int) {
	if isUserIRComponent(n) {
		g.renderIRUserComponent(b, n, depth)
		return
	}
	if !isStdlibComponentName(n.Name) {
		// Raw HTML element / namespace-resolved element (html.div, html.img).
		g.renderRawElementIR(b, n, depth)
		return
	}
	g.renderStaticNode(b, n, depth)
}

// isUserIRComponent reports whether a NodeInst resolves to a user-defined
// component (local or imported) as opposed to a stdlib component or a
// platform-resolved raw element (html.div, html.img, ...).
func isUserIRComponent(n *ir.NodeInst) bool {
	if n.Component == nil || n.Component.AST == nil {
		return false
	}
	if isStdlibComponentName(n.Name) {
		return false
	}
	return true
}

// renderIRIf emits an if block whose branches recurse through IR.
func (g *htmlGen) renderIRIf(b *strings.Builder, n *ir.If, depth int) {
	indent := strings.Repeat("  ", depth)
	id := g.allocID()
	fmt.Fprintf(b, "%s<div id=\"%s\" style=\"display:none\">\n", indent, id)
	for _, s := range n.Body {
		g.renderIRStmt(b, s, depth+1)
	}
	fmt.Fprintf(b, "%s</div>\n", indent)
	g.addIfUpdater(id, n.Cond)
	if len(n.Else) > 0 {
		elseID := g.allocID()
		fmt.Fprintf(b, "%s<div id=\"%s\">\n", indent, elseID)
		for _, s := range n.Else {
			g.renderIRStmt(b, s, depth+1)
		}
		fmt.Fprintf(b, "%s</div>\n", indent)
		g.addElseUpdater(elseID, n.Cond)
	}
}

// renderIRFor emits a for-loop placeholder; updater is registered from the
// AST back-ref so the existing JS-emission path is reused.
func (g *htmlGen) renderIRFor(b *strings.Builder, n *ir.For, depth int) {
	indent := strings.Repeat("  ", depth)
	id := g.allocID()
	fmt.Fprintf(b, "%s<div id=\"%s\"></div>\n", indent, id)
	if n.AST != nil {
		g.addForStmtUpdater(id, n.AST)
		if len(n.Else) > 0 {
			elseID := g.allocID()
			fmt.Fprintf(b, "%s<div id=\"%s\">\n", indent, elseID)
			for _, s := range n.Else {
				g.renderIRStmt(b, s, depth+1)
			}
			fmt.Fprintf(b, "%s</div>\n", indent)
			g.addForElseStmtUpdater(id, elseID, n.AST)
		}
	}
}

// visualNodeFromIR synthesizes an ast.VisualNode from an ir.NodeInst so
// legacy AST-based per-element renderers observe post-optimization IR
// values (constant-folded props, expanded loops, etc.) rather than the
// raw parsed AST. Target, props, handlers, and children are reconstructed
// from IR; Pos is inherited from the original parse when available.
func visualNodeFromIR(n *ir.NodeInst) *ast.VisualNode {
	if n == nil {
		return nil
	}
	var target ast.TargetExpr
	if ns, field, ok := strings.Cut(n.Name, "."); ok {
		target = &ast.SelectExpr{
			Operand: &ast.IdentExpr{Name: ns},
			Field:   field,
			Kind:    ast.SelectField,
		}
	} else {
		target = &ast.IdentExpr{Name: n.Name}
	}

	var args []ast.ArgOrEventHandler
	for _, p := range n.Props {
		args = append(args, ast.Arg{
			Name:  p.Name,
			Value: ir.ConvertExpr(p.Value),
		})
	}
	for i := range n.Handlers {
		h := &n.Handlers[i]
		if h.AST != nil {
			args = append(args, h.AST)
		}
	}
	var block ast.StmtBlock
	if len(n.Children) > 0 {
		block.IsMultiline = true
		block.Pos = ast.Pos{Line: 1}
		for _, c := range n.Children {
			if astStmt := ir.ConvertStmt(c); astStmt != nil {
				block.Stmts = append(block.Stmts, astStmt)
			}
		}
	}

	vn := &ast.VisualNode{
		Target: target,
		ID:     n.ID,
		Block:  block,
	}
	if len(args) > 0 {
		vn.Args = ast.ArgList{Args: args, IsMultiline: len(args) > 3}
	}
	if src, ok := n.AST.(*ast.VisualNode); ok {
		vn.Pos = src.Pos
	} else if src, ok := n.AST.(*ast.CallStmt); ok {
		vn.Pos = src.Pos
	}
	return vn
}

// stateVars returns all state (non-const) variables for the compiled
// package — top-level pkg.Vars merged with the main component's Vars.
func (g *htmlGen) stateVars() []*ir.Var {
	var out []*ir.Var
	if g.pkg != nil {
		out = append(out, g.pkg.Vars...)
		if main := mainIRComponent(g.pkg); main != nil {
			out = append(out, main.Vars...)
		}
	}
	return out
}

// pkgStructs returns all struct definitions reachable from the main package.
func (g *htmlGen) pkgStructs() []*ir.StructDef {
	if g.pkg == nil {
		return nil
	}
	return g.pkg.Structs
}

// pkgFuncs returns user-defined top-level funcs plus main component funcs.
func (g *htmlGen) pkgFuncs() []*ir.Func {
	if g.pkg == nil {
		return nil
	}
	out := append([]*ir.Func{}, g.pkg.Funcs...)
	if main := mainIRComponent(g.pkg); main != nil {
		out = append(out, main.Funcs...)
	}
	return out
}

// pkgConsts returns all consts (top-level + main component).
func (g *htmlGen) pkgConsts() []*ir.Var {
	if g.pkg == nil {
		return nil
	}
	out := append([]*ir.Var{}, g.pkg.Consts...)
	if main := mainIRComponent(g.pkg); main != nil {
		for _, v := range main.Vars {
			if v.IsConst {
				out = append(out, v)
			}
		}
	}
	return out
}

// mainIRComponent returns the main component of a package, or nil.
func mainIRComponent(pkg *ir.Package) *ir.Component {
	for _, c := range pkg.Components {
		if c.Name == "main" {
			return c
		}
	}
	return nil
}

// irExprToAST reconstructs an AST expression from an IR expression so that
// existing AST-oriented helpers (literalToJS, exprToJS) can consume it. A
// thin wrapper over ir.ConvertExpr for readability at call sites.
func irExprToAST(e ir.Expr) ast.Expr {
	if e == nil {
		return nil
	}
	return ir.ConvertExpr(e)
}

// irPlatformBody returns the platform-specific override body if any
// PlatformFilter entries match platform; otherwise returns the original
// statements (with any non-matching PlatformFilters dropped).
func irPlatformBody(stmts []ir.Stmt, platform string) []ir.Stmt {
	var matches []ir.Stmt
	hasFilter := false
	for _, s := range stmts {
		if pf, ok := s.(*ir.PlatformFilter); ok {
			hasFilter = true
			if pf.Platform == platform {
				matches = append(matches, pf.Body...)
			}
		}
	}
	if hasFilter {
		return matches
	}
	return stmts
}

// isStdlibComponentName reports whether a name is a built-in stdlib
// component that the html platform handles via dedicated renderStaticX
// helpers rather than inlined user-component expansion.
func isStdlibComponentName(name string) bool {
	switch name {
	case "vbox", "hbox", "stack", "scroll", "spacer", "text", "button", "input",
		"image", "checkbox", "radio", "toggle", "select", "textarea", "progress",
		"spinner", "badge", "tabs", "link", "divider", "modal", "drawer",
		"tooltip", "popover", "accordion", "splitview", "table", "tree", "menu",
		"menubar", "toolbar", "datepicker", "chip", "avatar", "card", "slot",
		"window", "timer":
		return true
	}
	return false
}

// isComponent reports whether a call expression targets a known component
// (local, imported, or platform-resolved raw element like html.img).
func (g *htmlGen) isComponent(call *ast.CallExpr) bool {
	if g.pkg == nil || g.pkg.Symbols == nil {
		// No type info — assume component (legacy fallback).
		if call == nil {
			return false
		}
		_, ok := call.Func.(ast.TargetExpr)
		return ok
	}
	name := callTargetName(call)
	if name == "" {
		return false
	}
	if _, ok := g.pkg.Symbols.LookupComponent(name); ok {
		return true
	}
	// Qualified call: check imported package component or namespace Resolve()
	// (platform raw elements like html.img; package-exported components).
	ns, field, ok := strings.Cut(name, ".")
	if !ok {
		return false
	}
	for _, imp := range g.pkg.Imports {
		if imp.Alias != ns {
			continue
		}
		if imp.Pkg != nil {
			if _, ok := imp.Pkg.Symbols.LookupComponent(field); ok {
				return true
			}
		}
	}
	// Platform Resolve (html.div, html.img, etc.).
	if sym, ok := g.pkg.Symbols.Root.Lookup(ns); ok {
		if nsSym, ok := sym.(*ir.Namespace); ok && nsSym.Resolve != nil {
			if nsSym.Resolve(field) != nil {
				return true
			}
		}
	}
	return false
}

// isFunction reports whether a visual node name resolves to a function (not component).
func (g *htmlGen) isFunction(name string) bool {
	if g.pkg == nil || g.pkg.Symbols == nil {
		return false
	}
	if _, ok := g.pkg.Symbols.LookupComponent(name); ok {
		return false // it's a component
	}
	if sym, ok := g.pkg.Symbols.Root.Lookup(name); ok {
		_, isFunc := sym.(*ir.Func)
		return isFunc
	}
	return false
}

// callTargetName extracts the identifier name from a call expression target.
func callTargetName(call *ast.CallExpr) string {
	if call == nil {
		return ""
	}
	switch t := call.Func.(type) {
	case *ast.IdentExpr:
		return t.Name
	case *ast.SelectExpr:
		if id, ok := t.Operand.(*ast.IdentExpr); ok {
			return id.Name + "." + t.Field
		}
	}
	return ""
}


// renderStaticNode renders a NodeInst as static HTML.
func (g *htmlGen) renderStaticNode(b *strings.Builder, n *ir.NodeInst, depth int) {
	indent := strings.Repeat("  ", depth)
	_ = indent // used by generated template code below
	name := n.Name

	switch name {
	case "vbox":
		g.renderStaticBox(b, n, depth, true)
	case "hbox":
		g.renderStaticBox(b, n, depth, false)
	case "text":
		g.renderStaticText(b, n, depth)
	case "button":
		g.renderStaticButton(b, n, depth)
	case "input":
		g.renderStaticInput(b, n, depth)
	case "checkbox":
		g.renderStaticCheckbox(b, n, depth)
	case "image":
		g.renderStaticImage(b, n, depth)
	case "spacer":
		if g.preview {
			id := g.allocID()
			fmt.Fprintf(b, "%s<div id=\"%s\" style=\"flex:1\"%s></div>\n", indent, id, g.previewAttrsIR(n))
		} else {
			fmt.Fprintf(b, "%s<div style=\"flex:1\"></div>\n", indent)
		}
	case "scroll":
		id := ""
		style := g.buildCSSStyleIR(n)
		style = htmlutil.AppendCSS(style, "overflow", "auto")
		if g.nodeIsReactiveIR(n) {
			id = g.allocID()
		}
		g.writeOpenTagIR(b, "div", id, style, n, depth)
		for _, s := range n.Children {
			g.renderIRStmt(b, s, depth+1)
		}
		fmt.Fprintf(b, "%s</div>\n", indent)
	case "stack":
		style := g.buildCSSStyleIR(n)
		style = htmlutil.AppendCSS(style, "position", "relative")
		g.writeOpenTagIR(b, "div", "", style, n, depth)
		for _, s := range n.Children {
			g.renderIRStmt(b, s, depth+1)
		}
		fmt.Fprintf(b, "%s</div>\n", indent)

	// --- Tier 1: Core Input ---
	case "radio":
		g.renderStaticRadio(b, n, depth)
	case "toggle":
		g.renderStaticToggle(b, n, depth)
	case "select":
		g.renderStaticSelect(b, n, depth)
	case "textarea":
		g.renderStaticTextarea(b, n, depth)

	// --- Tier 2: Feedback & Navigation ---
	case "progress":
		style := g.buildCSSStyleIR(n)
		value := g.evalStaticStringIR(nodeProps(n), "value")
		maxVal := g.evalStaticStringIR(nodeProps(n), "max")
		if maxVal == "" {
			maxVal = "1"
		}
		id := ""
		if g.nodeIsReactiveIR(n) || g.preview {
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
		g.writeUserAttrsIR(b, id, n)
		b.WriteString(g.previewAttrsIR(n))
		b.WriteString("></progress>\n")
		if id != "" {
			if valExpr := codegen.NodeProp(n, "value"); valExpr != nil {
				g.addAttrUpdater(id, "value", valExpr)
			}
		}
	case "spinner":
		g.CommonAnalysis.AddStyle("@keyframes sngl-spin { to { transform: rotate(360deg); } }")
		g.CommonAnalysis.AddStyle(".sngl-spinner { display: inline-block; width: 1em; height: 1em; border: 2px solid currentColor; border-right-color: transparent; border-radius: 50%; animation: sngl-spin 0.75s linear infinite; vertical-align: middle; }")
		style := g.buildCSSStyleIR(n)
		label := g.evalStaticStringIR(nodeProps(n), "label")
		id := ""
		if g.preview {
			id = g.allocID()
		}
		g.writeOpenTagIR(b, "span", id, style, n, depth)
		fmt.Fprintf(b, "<span class=\"sngl-spinner\"></span> %s</span>\n", html.EscapeString(label))
	case "badge":
		style := g.buildCSSStyleIR(n)
		style = htmlutil.AppendCSS(style, "display", "inline-block")
		style = htmlutil.AppendCSS(style, "padding", "2px 8px")
		style = htmlutil.AppendCSS(style, "border-radius", "12px")
		style = htmlutil.AppendCSS(style, "font-size", "12px")
		value := g.evalStaticStringIR(nodeProps(n), "value")
		id := ""
		if g.nodeIsReactiveIR(n) || g.preview {
			id = g.allocID()
		}
		g.writeOpenTagIR(b, "span", id, style, n, depth)
		fmt.Fprintf(b, "%s</span>\n", html.EscapeString(value))
		if codegen.IRIsReactive(codegen.NodeProp(n, "value")) {
			g.addTextUpdater(id, codegen.NodeProp(n, "value"))
		}
	case "tabs":
		g.renderStaticTabs(b, n, depth)
	case "link":
		style := g.buildCSSStyleIR(n)
		text := g.evalStaticStringIR(nodeProps(n), "text")
		href := g.evalStaticStringIR(nodeProps(n), "href")
		target := g.evalStaticStringIR(nodeProps(n), "target")
		id := ""
		if g.nodeIsReactiveIR(n) || g.preview {
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
		g.writeUserAttrsIR(b, id, n)
		b.WriteString(g.previewAttrsIR(n))
		fmt.Fprintf(b, ">%s</a>\n", html.EscapeString(text))
		if h := codegen.NodeHandler(n, "click"); h != nil && h.AST != nil && len(h.AST.Body.Stmts) > 0 {
			if id == "" {
				id = g.allocID()
			}
			g.addClickHandler(id, h.Func.Block)
		}
	case "divider":
		style := g.buildCSSStyleIR(n)
		label := g.evalStaticStringIR(nodeProps(n), "label")
		id := ""
		if g.preview {
			id = g.allocID()
		}
		if label != "" {
			style = htmlutil.AppendCSS(style, "display", "flex")
			style = htmlutil.AppendCSS(style, "align-items", "center")
			style = htmlutil.AppendCSS(style, "gap", "8px")
			g.writeOpenTagIR(b, "div", id, style, n, depth)
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
			b.WriteString(g.previewAttrsIR(n))
			b.WriteString(" />\n")
		}

	// --- Tier 3: Overlays & Layout ---
	case "modal":
		g.renderStaticModal(b, n, depth)
	case "drawer":
		g.renderStaticConditionalContainer(b, n, depth, "div")
	case "tooltip":
		// Wrap child with title attribute
		title := g.evalStaticStringIR(nodeProps(n), "text")
		fmt.Fprintf(b, "%s<div title=\"%s\">\n", indent, html.EscapeString(title))
		for _, s := range n.Children {
			g.renderIRStmt(b, s, depth+1)
		}
		fmt.Fprintf(b, "%s</div>\n", indent)
	case "popover":
		g.renderStaticConditionalContainer(b, n, depth, "div")
	case "accordion":
		g.renderStaticAccordion(b, n, depth)
	case "splitview":
		style := g.buildCSSStyleIR(n)
		style = htmlutil.AppendCSS(style, "display", "flex")
		direction := g.evalStaticStringIR(nodeProps(n), "direction")
		if direction == "vertical" {
			style = htmlutil.AppendCSS(style, "flex-direction", "column")
		} else {
			style = htmlutil.AppendCSS(style, "flex-direction", "row")
		}
		id := ""
		if g.preview {
			id = g.allocID()
		}
		g.writeOpenTagIR(b, "div", id, style, n, depth)
		for i, s := range n.Children {
			if i > 0 {
				fmt.Fprintf(b, "%s  <div style=\"width:4px;background:#ccc;cursor:col-resize\"></div>\n", indent)
			}
			fmt.Fprintf(b, "%s  <div style=\"flex:1;overflow:auto\">\n", indent)
			g.renderIRStmt(b, s, depth+2)
			fmt.Fprintf(b, "%s  </div>\n", indent)
		}
		fmt.Fprintf(b, "%s</div>\n", indent)

	// --- Tier 4: Data & Desktop ---
	case "table":
		g.renderStaticTable(b, n, depth)
	case "tree":
		g.renderStaticTree(b, n, depth)
	case "menu":
		g.renderStaticConditionalContainer(b, n, depth, "div")
	case "menubar":
		style := g.buildCSSStyleIR(n)
		style = htmlutil.AppendCSS(style, "display", "flex")
		style = htmlutil.AppendCSS(style, "gap", "4px")
		id := ""
		if g.preview {
			id = g.allocID()
		}
		g.writeOpenTagIR(b, "nav", id, style, n, depth)
		fmt.Fprintf(b, "</nav>\n")
	case "toolbar":
		style := g.buildCSSStyleIR(n)
		style = htmlutil.AppendCSS(style, "display", "flex")
		style = htmlutil.AppendCSS(style, "gap", "4px")
		style = htmlutil.AppendCSS(style, "align-items", "center")
		id := ""
		if g.preview {
			id = g.allocID()
		}
		g.writeOpenTagIR(b, "div", id, style, n, depth)
		for _, s := range n.Children {
			g.renderIRStmt(b, s, depth+1)
		}
		fmt.Fprintf(b, "%s</div>\n", indent)

	// --- Tier 5: Mobile & Specialized ---
	case "datepicker":
		g.renderStaticDatepicker(b, n, depth)
	case "chip":
		style := g.buildCSSStyleIR(n)
		style = htmlutil.AppendCSS(style, "display", "inline-flex")
		style = htmlutil.AppendCSS(style, "align-items", "center")
		style = htmlutil.AppendCSS(style, "padding", "4px 12px")
		style = htmlutil.AppendCSS(style, "border-radius", "16px")
		style = htmlutil.AppendCSS(style, "border", "1px solid #ccc")
		style = htmlutil.AppendCSS(style, "font-size", "14px")
		label := g.evalStaticStringIR(nodeProps(n), "label")
		id := ""
		if g.nodeIsReactiveIR(n) || g.preview {
			id = g.allocID()
		}
		g.writeOpenTagIR(b, "span", id, style, n, depth)
		fmt.Fprintf(b, "%s", html.EscapeString(label))
		b.WriteString("</span>\n")
	case "avatar":
		style := g.buildCSSStyleIR(n)
		style = htmlutil.AppendCSS(style, "display", "inline-flex")
		style = htmlutil.AppendCSS(style, "align-items", "center")
		style = htmlutil.AppendCSS(style, "justify-content", "center")
		style = htmlutil.AppendCSS(style, "border-radius", "50%")
		style = htmlutil.AppendCSS(style, "width", "40px")
		style = htmlutil.AppendCSS(style, "height", "40px")
		style = htmlutil.AppendCSS(style, "background", "#ccc")
		style = htmlutil.AppendCSS(style, "font-weight", "bold")
		initials := g.evalStaticStringIR(nodeProps(n), "initials")
		if initials == "" {
			initials = g.evalStaticStringIR(nodeProps(n), "alt")
		}
		id := ""
		if g.preview {
			id = g.allocID()
		}
		g.writeOpenTagIR(b, "div", id, style, n, depth)
		fmt.Fprintf(b, "%s</div>\n", html.EscapeString(initials))
	case "card":
		style := g.buildCSSStyleIR(n)
		variant := g.evalStaticStringIR(nodeProps(n), "variant")
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
		if g.nodeIsReactiveIR(n) || g.preview {
			id = g.allocID()
		}
		g.writeOpenTagIR(b, "div", id, style, n, depth)
		for _, s := range n.Children {
			g.renderIRStmt(b, s, depth+1)
		}
		fmt.Fprintf(b, "%s</div>\n", indent)

	case "slot":
		// Project caller's children into this position.
		for _, s := range g.irSlotChildren {
			g.renderIRStmt(b, s, depth)
		}

	case "window":
		// Windows are handled at Generate level — skip inline rendering.

	default:
		// Stdlib component without a dedicated renderer above (e.g. timer)
		// or a user/abstract component reached via the AST path: if a
		// component definition is available, inline its body at the call
		// site; otherwise render as a raw element.
		if comp := g.findIRComponent(n.Name); comp != nil {
			// Bind the NodeInst to the component so the user-component
			// renderer can pick up params/vars/computed.
			if n.Component == nil {
				n.Component = comp
			}
			g.renderIRUserComponent(b, n, depth)
		} else {
			g.renderRawElementIR(b, n, depth)
		}
	}
}

func (g *htmlGen) renderStaticBox(b *strings.Builder, n *ir.NodeInst, depth int, vertical bool) {
	indent := strings.Repeat("  ", depth)
	style := g.buildCSSStyleIR(n)
	style = htmlutil.AppendCSS(style, "display", "flex")
	if vertical {
		style = htmlutil.AppendCSS(style, "flex-direction", "column")
	} else {
		style = htmlutil.AppendCSS(style, "flex-direction", "row")
	}

	if v := codegen.NodeProp(n, "scroll"); v != nil {
		if bv, ok := codegen.IRLiteralBool(v); ok && bv {
			if vertical {
				style = htmlutil.AppendCSS(style, "overflow-y", "auto")
			} else {
				style = htmlutil.AppendCSS(style, "overflow-x", "auto")
			}
		}
	}

	reactive := g.nodeIsReactiveIR(n)
	id := ""
	if reactive {
		id = g.allocID()
	}

	g.writeOpenTagIR(b, "div", id, style, n, depth)
	for _, s := range n.Children {
		g.renderIRStmt(b, s, depth+1)
	}
	fmt.Fprintf(b, "%s</div>\n", indent)
}

func (g *htmlGen) renderStaticText(b *strings.Builder, n *ir.NodeInst, depth int) {
	style := g.buildCSSStyleIR(n)
	val := g.evalStaticStringIR(nodeProps(n), "value")
	reactive := codegen.IRIsReactive(codegen.NodeProp(n, "value"))
	id := ""
	if reactive {
		id = g.allocID()
	}

	// In test mode, id/class need an element ID for updaters
	if g.testMode && id == "" && g.vnHasUserAttrs(visualNodeFromIR(n)) {
		id = g.allocID()
	}

	g.writeOpenTagIR(b, "span", id, style, n, depth)
	b.WriteString(html.EscapeString(val))
	b.WriteString("</span>\n")

	if codegen.IRIsReactive(codegen.NodeProp(n, "value")) {
		g.addTextUpdater(id, codegen.NodeProp(n, "value"))
	}
	if g.testMode && id != "" {
		g.addUserAttrUpdaters(id, visualNodeFromIR(n))
	}
}

func (g *htmlGen) renderStaticButton(b *strings.Builder, n *ir.NodeInst, depth int) {
	style := g.buildCSSStyleIR(n)
	text := g.evalStaticStringIR(nodeProps(n), "text")
	textExpr := codegen.NodeProp(n, "text")
	disabledExpr := codegen.NodeProp(n, "disabled")
	reactive := codegen.IRIsReactive(textExpr) || len(n.Handlers) > 0 || codegen.IRIsReactive(disabledExpr)
	id := ""
	if reactive {
		id = g.allocID()
	}

	disabled := ""
	if disabledExpr != nil {
		// Literal true → disabled. Reactive / non-literal → assume true (initial render).
		if bv, ok := codegen.IRLiteralBool(disabledExpr); ok {
			if bv {
				disabled = " disabled"
			}
		} else if !codegen.IRIsLiteral(disabledExpr) {
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

	g.writeUserAttrsIR(b, id, n)
	b.WriteString(g.previewAttrsIR(n))
	fmt.Fprintf(b, "%s>%s</button>\n", disabled, html.EscapeString(text))

	if codegen.IRIsReactive(textExpr) {
		g.addTextContentUpdater(id, textExpr)
	}
	if codegen.IRIsReactive(disabledExpr) {
		g.addDisabledUpdater(id, disabledExpr)
	}

	// Event handlers
	if clickEvt := codegen.NodeHandler(n, "click"); clickEvt != nil && clickEvt.AST != nil {
		if len(clickEvt.AST.Body.Stmts) > 0 {
			g.addClickHandler(id, clickEvt.Func.Block)
		}
	}
}

func (g *htmlGen) renderStaticInput(b *strings.Builder, n *ir.NodeInst, depth int) {
	style := g.buildCSSStyleIR(n)
	id := g.allocID() // inputs are always reactive

	placeholder := ""
	if v := codegen.NodeProp(n, "placeholder"); v != nil {
		if s, ok := codegen.IRLiteralString(v); ok {
			placeholder = s
		}
	}

	inputType := "text"
	if v := codegen.NodeProp(n, "type"); v != nil {
		if s, ok := codegen.IRLiteralString(v); ok {
			inputType = s
		}
	}

	value := g.evalStaticStringIR(nodeProps(n), "value")

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

	g.writeUserAttrsIR(b, id, n)
	b.WriteString(g.previewAttrsIR(n))
	b.WriteString(" />\n")

	// Add value sync updater if the input is bound to state via set()
	if inputEvt := codegen.NodeHandler(n, "input"); inputEvt != nil && inputEvt.AST != nil {
		if len(inputEvt.AST.Body.Stmts) > 0 {
			// Extract the set() target to determine the JS expression for the bound value
			if target, ok := extractSetTarget(inputEvt.AST.Body.Stmts[0]); ok {
				jsExpr := g.lang.TranslateExpr(target, g.scope)
				root := findRootIdentAST(target)
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
	if inputEvt := codegen.NodeHandler(n, "input"); inputEvt != nil && inputEvt.AST != nil {
		if len(inputEvt.AST.Body.Stmts) > 0 {
			g.addInputHandler(id, inputEvt.Func.Block)
		}
	}
}

func (g *htmlGen) renderStaticCheckbox(b *strings.Builder, n *ir.NodeInst, depth int) {
	style := g.buildCSSStyleIR(n)
	id := g.allocID()

	checked := false
	if v := codegen.NodeProp(n, "checked"); v != nil {
		if bv, ok := codegen.IRLiteralBool(v); ok {
			checked = bv
		} else if codegen.IRIsReactive(v) {
			checked = true
		}
	}
	label := g.evalStaticStringIR(nodeProps(n), "label")

	indent := strings.Repeat("  ", depth)
	fmt.Fprintf(b, "%s<label id=\"%s\"", indent, id)
	if style != "" {
		fmt.Fprintf(b, " style=\"%s\"", style)
	}

	g.writeUserAttrsIR(b, id, n)
	b.WriteString(g.previewAttrsIR(n))
	b.WriteString(">")
	if checked {
		b.WriteString("<input type=\"checkbox\" checked />")
	} else {
		b.WriteString("<input type=\"checkbox\" />")
	}
	fmt.Fprintf(b, " %s</label>\n", html.EscapeString(label))

	if changeEvt := codegen.NodeHandler(n, "change"); changeEvt != nil && changeEvt.AST != nil {
		if len(changeEvt.AST.Body.Stmts) > 0 {
			g.addChangeHandler(id, changeEvt.Func.Block)
		}
	}
}

func (g *htmlGen) renderStaticImage(b *strings.Builder, n *ir.NodeInst, depth int) {
	style := g.buildCSSStyleIR(n)
	alt := "image"
	if v := codegen.NodeProp(n, "alt"); v != nil {
		if s, ok := codegen.IRLiteralString(v); ok {
			alt = s
		}
	}
	src := ""
	if v := codegen.NodeProp(n, "src"); v != nil {
		if s, ok := codegen.IRLiteralString(v); ok {
			src = s
		}
	}
	if v := codegen.NodeProp(n, "fit"); v != nil {
		if s, ok := codegen.IRLiteralString(v); ok {
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

	g.writeUserAttrsIR(b, id, n)
	if g.preview {
		b.WriteString(g.previewAttrsIR(n))
	}
	b.WriteString(" />\n")
}

// --- New component renderers ---

func (g *htmlGen) renderStaticRadio(b *strings.Builder, n *ir.NodeInst, depth int) {
	style := g.buildCSSStyleIR(n)
	id := g.allocID()
	indent := strings.Repeat("  ", depth)
	g.writeOpenTagIR(b, "fieldset", id, style, n, depth)
	// Static options rendered if literal
	if v := codegen.NodeProp(n, "options"); codegen.IRIsLiteral(v) {
		value := g.evalStaticStringIR(nodeProps(n), "value")
		if items, ok := exprLiteralAnyIR(v).([]any); ok {
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
	if evt := codegen.NodeHandler(n, "change"); evt != nil && evt.AST != nil && len(evt.AST.Body.Stmts) > 0 {
		g.addChangeHandler(id, evt.Func.Block)
	}
}

func (g *htmlGen) renderStaticToggle(b *strings.Builder, n *ir.NodeInst, depth int) {
	style := g.buildCSSStyleIR(n)
	id := g.allocID()
	checked := false
	if v := codegen.NodeProp(n, "checked"); v != nil {
		if bv, ok := codegen.IRLiteralBool(v); ok {
			checked = bv
		} else if codegen.IRIsReactive(v) {
			checked = true
		}
	}
	label := g.evalStaticStringIR(nodeProps(n), "label")
	indent := strings.Repeat("  ", depth)
	checkedAttr := ""
	if checked {
		checkedAttr = " checked"
	}
	fmt.Fprintf(b, "%s<label id=\"%s\"", indent, id)
	if style != "" {
		fmt.Fprintf(b, " style=\"%s\"", style)
	}
	g.writeUserAttrsIR(b, id, n)
	b.WriteString(g.previewAttrsIR(n))
	fmt.Fprintf(b, "><input type=\"checkbox\" role=\"switch\"%s /> %s</label>\n", checkedAttr, html.EscapeString(label))
	if evt := codegen.NodeHandler(n, "change"); evt != nil && evt.AST != nil && len(evt.AST.Body.Stmts) > 0 {
		g.addChangeHandler(id, evt.Func.Block)
	}
}

func (g *htmlGen) renderStaticSelect(b *strings.Builder, n *ir.NodeInst, depth int) {
	style := g.buildCSSStyleIR(n)
	id := g.allocID()
	indent := strings.Repeat("  ", depth)
	value := g.evalStaticStringIR(nodeProps(n), "value")
	placeholder := g.evalStaticStringIR(nodeProps(n), "placeholder")
	fmt.Fprintf(b, "%s<select id=\"%s\"", indent, id)
	if style != "" {
		fmt.Fprintf(b, " style=\"%s\"", style)
	}
	g.writeUserAttrsIR(b, id, n)
	b.WriteString(g.previewAttrsIR(n))
	b.WriteString(">\n")
	if placeholder != "" {
		fmt.Fprintf(b, "%s  <option value=\"\" disabled selected>%s</option>\n", indent, html.EscapeString(placeholder))
	}
	if v := codegen.NodeProp(n, "options"); codegen.IRIsLiteral(v) {
		if items, ok := exprLiteralAnyIR(v).([]any); ok {
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
	if evt := codegen.NodeHandler(n, "change"); evt != nil && evt.AST != nil && len(evt.AST.Body.Stmts) > 0 {
		g.addChangeHandler(id, evt.Func.Block)
	}
}

func (g *htmlGen) renderStaticTextarea(b *strings.Builder, n *ir.NodeInst, depth int) {
	style := g.buildCSSStyleIR(n)
	id := g.allocID()
	indent := strings.Repeat("  ", depth)
	value := g.evalStaticStringIR(nodeProps(n), "value")
	placeholder := g.evalStaticStringIR(nodeProps(n), "placeholder")
	rows := "3"
	if v := codegen.NodeProp(n, "rows"); v != nil {
		if nv, ok := codegen.IRLiteralInt(v); ok {
			rows = fmt.Sprint(nv)
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
	g.writeUserAttrsIR(b, id, n)
	b.WriteString(g.previewAttrsIR(n))
	fmt.Fprintf(b, ">%s</textarea>\n", html.EscapeString(value))
	if evt := codegen.NodeHandler(n, "input"); evt != nil && evt.AST != nil && len(evt.AST.Body.Stmts) > 0 {
		g.addInputHandler(id, evt.Func.Block)
	}
}

func (g *htmlGen) renderStaticTabs(b *strings.Builder, n *ir.NodeInst, depth int) {
	style := g.buildCSSStyleIR(n)
	id := g.allocID()
	indent := strings.Repeat("  ", depth)
	g.writeOpenTagIR(b, "div", id, style, n, depth)
	// Tab bar
	fmt.Fprintf(b, "%s  <div role=\"tablist\" style=\"display:flex;gap:4px;border-bottom:1px solid #ccc\">\n", indent)
	if v := codegen.NodeProp(n, "items"); codegen.IRIsLiteral(v) {
		selected := 0
		if sv := codegen.NodeProp(n, "selected"); sv != nil {
			if nv, ok := codegen.IRLiteralInt(sv); ok {
				selected = nv
			}
		}
		if items, ok := exprLiteralAnyIR(v).([]any); ok {
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
	for _, s := range n.Children {
		g.renderIRStmt(b, s, depth+1)
	}
	fmt.Fprintf(b, "%s</div>\n", indent)
}

func (g *htmlGen) renderStaticModal(b *strings.Builder, n *ir.NodeInst, depth int) {
	style := g.buildCSSStyleIR(n)
	id := g.allocID()
	indent := strings.Repeat("  ", depth)
	open := true
	if v := codegen.NodeProp(n, "open"); v != nil {
		if bv, ok := codegen.IRLiteralBool(v); ok {
			open = bv
		}
	}
	display := ""
	if !open {
		display = "display:none;"
	}
	title := g.evalStaticStringIR(nodeProps(n), "title")
	overlayStyle := display + "position:fixed;top:0;left:0;right:0;bottom:0;background:rgba(0,0,0,0.5);display:flex;align-items:center;justify-content:center;z-index:1000"
	if !open {
		overlayStyle = "display:none"
	}
	fmt.Fprintf(b, "%s<div id=\"%s\" style=\"%s\"", indent, id, overlayStyle)
	g.writeUserAttrsIR(b, id, n)
	b.WriteString(g.previewAttrsIR(n))
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
	for _, s := range n.Children {
		g.renderIRStmt(b, s, depth+2)
	}
	fmt.Fprintf(b, "%s  </div>\n", indent)
	fmt.Fprintf(b, "%s</div>\n", indent)
	if codegen.IRIsReactive(codegen.NodeProp(n, "open")) {
		g.addIfUpdater(id, codegen.NodeProp(n, "open"))
	}
}

func (g *htmlGen) renderStaticConditionalContainer(b *strings.Builder, n *ir.NodeInst, depth int, tag string) {
	style := g.buildCSSStyleIR(n)
	id := g.allocID()
	indent := strings.Repeat("  ", depth)
	open := true
	if v := codegen.NodeProp(n, "open"); v != nil {
		if bv, ok := codegen.IRLiteralBool(v); ok {
			open = bv
		}
	}
	if v := codegen.NodeProp(n, "visible"); v != nil {
		if bv, ok := codegen.IRLiteralBool(v); ok {
			open = bv
		}
	}
	if !open {
		style = htmlutil.AppendCSS(style, "display", "none")
	}
	g.writeOpenTagIR(b, tag, id, style, n, depth)
	for _, s := range n.Children {
		g.renderIRStmt(b, s, depth+1)
	}
	fmt.Fprintf(b, "%s</%s>\n", indent, tag)
	if codegen.IRIsReactive(codegen.NodeProp(n, "open")) {
		g.addIfUpdater(id, codegen.NodeProp(n, "open"))
	}
}

func (g *htmlGen) renderStaticAccordion(b *strings.Builder, n *ir.NodeInst, depth int) {
	style := g.buildCSSStyleIR(n)
	id := ""
	if g.preview {
		id = g.allocID()
	}
	indent := strings.Repeat("  ", depth)
	g.writeOpenTagIR(b, "div", id, style, n, depth)
	if v := codegen.NodeProp(n, "items"); codegen.IRIsLiteral(v) {
		if items, ok := exprLiteralAnyIR(v).([]any); ok {
			// Extract child NodeInsts (the IR equivalent of vnChildNodes).
			var children []*ir.NodeInst
			for _, c := range n.Children {
				if cn, ok := c.(*ir.NodeInst); ok {
					children = append(children, cn)
				}
			}
			for i, item := range items {
				s := fmt.Sprint(item)
				fmt.Fprintf(b, "%s  <details>\n", indent)
				fmt.Fprintf(b, "%s    <summary>%s</summary>\n", indent, html.EscapeString(s))
				if i < len(children) {
					g.renderStaticNode(b, children[i], depth+2)
				}
				fmt.Fprintf(b, "%s  </details>\n", indent)
			}
		}
	}
	fmt.Fprintf(b, "%s</div>\n", indent)
}

func (g *htmlGen) renderStaticTable(b *strings.Builder, n *ir.NodeInst, depth int) {
	style := g.buildCSSStyleIR(n)
	style = htmlutil.AppendCSS(style, "border-collapse", "collapse")
	style = htmlutil.AppendCSS(style, "width", "100%")
	id := ""
	if g.nodeIsReactiveIR(n) || g.preview {
		id = g.allocID()
	}
	indent := strings.Repeat("  ", depth)
	g.writeOpenTagIR(b, "table", id, style, n, depth)
	// Header
	if v := codegen.NodeProp(n, "columns"); codegen.IRIsLiteral(v) {
		if cols, ok := exprLiteralAnyIR(v).([]any); ok {
			fmt.Fprintf(b, "%s  <thead><tr>\n", indent)
			for _, col := range cols {
				fmt.Fprintf(b, "%s    <th style=\"text-align:left;padding:8px;border-bottom:2px solid #ddd\">%s</th>\n", indent, html.EscapeString(fmt.Sprint(col)))
			}
			fmt.Fprintf(b, "%s  </tr></thead>\n", indent)
		}
	}
	// Body
	if v := codegen.NodeProp(n, "rows"); codegen.IRIsLiteral(v) {
		if rows, ok := exprLiteralAnyIR(v).([]any); ok {
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

func (g *htmlGen) renderStaticTree(b *strings.Builder, n *ir.NodeInst, depth int) {
	style := g.buildCSSStyleIR(n)
	id := ""
	if g.preview {
		id = g.allocID()
	}
	indent := strings.Repeat("  ", depth)
	style = htmlutil.AppendCSS(style, "list-style", "none")
	style = htmlutil.AppendCSS(style, "padding-left", "16px")
	g.writeOpenTagIR(b, "ul", id, style, n, depth)
	if v := codegen.NodeProp(n, "items"); codegen.IRIsLiteral(v) {
		if items, ok := exprLiteralAnyIR(v).([]any); ok {
			for _, item := range items {
				fmt.Fprintf(b, "%s  <li>▶ %s</li>\n", indent, html.EscapeString(fmt.Sprint(item)))
			}
		}
	}
	fmt.Fprintf(b, "%s</ul>\n", indent)
}

func (g *htmlGen) renderStaticDatepicker(b *strings.Builder, n *ir.NodeInst, depth int) {
	style := g.buildCSSStyleIR(n)
	id := g.allocID()
	indent := strings.Repeat("  ", depth)
	value := g.evalStaticStringIR(nodeProps(n), "value")
	placeholder := g.evalStaticStringIR(nodeProps(n), "placeholder")
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
	g.writeUserAttrsIR(b, id, n)
	b.WriteString(g.previewAttrsIR(n))
	b.WriteString(" />\n")
	if evt := codegen.NodeHandler(n, "change"); evt != nil && evt.AST != nil && len(evt.AST.Body.Stmts) > 0 {
		g.addChangeHandler(id, evt.Func.Block)
	}
}

const maxComponentDepth = 10

// renderRawElementIR renders an IR NodeInst as a raw HTML element. The
// component name (or its local part for qualified names like html.div) is
// used as the tag. Props and children come from IR; a synthesized AST
// node is built locally for helpers that still accept ast.VisualNode
// (writeUserAttrs / writeOpenTag / exprDeps).
func (g *htmlGen) renderRawElementIR(b *strings.Builder, n *ir.NodeInst, depth int) {
	indent := strings.Repeat("  ", depth)
	// Extract tag name: "html.div" → "div", "button" → "button"
	tag := n.Name
	if _, local, ok := strings.Cut(tag, "."); ok {
		tag = local
	}

	id := ""
	if g.nodeIsReactiveIR(n) || g.preview || g.testMode {
		id = g.allocID()
	}
	vn := visualNodeFromIR(n)
	style := g.buildCSSStyle(vn)

	// Build inline attributes from static props.
	// innerText and innerHTML are rendered as element content, not attributes.
	props := nodeProps(n)
	var attrs strings.Builder
	staticInnerText := ""
	staticInnerHTML := ""
	for name, expr := range props {
		// class is emitted separately below to avoid duplicate attrs.
		if name == "style" || name == "class" {
			continue
		}
		// Try resolving SNGL expressions (e.g. component param references)
		// to static values for the initial HTML render.
		if codegen.IRIsReactive(expr) {
			if val := g.evalInitialStringIR(expr); val != "" {
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
			continue
		}
		// Literal / static.
		val := g.evalStaticStringIR(props, name)
		switch name {
		case "innerText":
			staticInnerText = val
		case "innerHTML":
			staticInnerHTML = val
		default:
			if bv, ok := codegen.IRLiteralBool(expr); ok {
				if bv {
					fmt.Fprintf(&attrs, " %s", html.EscapeString(name))
				}
			} else if codegen.IRIsLiteral(expr) {
				fmt.Fprintf(&attrs, " %s=\"%s\"", html.EscapeString(name), html.EscapeString(val))
			} else if val != "" {
				fmt.Fprintf(&attrs, " %s=\"%s\"", html.EscapeString(name), html.EscapeString(val))
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
	if classExpr := props["class"]; codegen.IRIsLiteral(classExpr) {
		if s, ok := codegen.IRLiteralString(classExpr); ok && s != "" {
			fmt.Fprintf(b, " class=%q", s)
		}
	}
	if style != "" {
		fmt.Fprintf(b, " style=\"%s\"", style)
	}
	b.WriteString(attrs.String())
	g.writeUserAttrs(b, id, vn)
	pos := nodePos(n)
	if g.preview && pos.IsValid() {
		fmt.Fprintf(b, " data-sngl-line=\"%d\" data-sngl-col=\"%d\"", pos.Line, pos.Column)
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
			for _, s := range n.Children {
				g.renderIRStmt(b, s, depth+1)
			}
			fmt.Fprintf(b, "%s</%s>\n", indent, tag)
		}
	}

	if id == "" {
		return
	}

	// Reactive props: set via JS updaters
	for name, expr := range props {
		if name == "style" {
			continue
		}
		if codegen.IRIsReactive(expr) {
			jsVal := g.exprToJSIR(expr)
			deps := g.exprDeps(ir.ConvertExpr(expr))
			uname := fmt.Sprintf("$u_%s_%s", id[1:], name)
			var body string
			switch name {
			case "innerHTML", "innerText", "textContent", "value", "checked", "disabled":
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
	for i := range n.Handlers {
		h := &n.Handlers[i]
		if h.Func == nil {
			continue
		}
		switch h.Name {
		case "click":
			g.addClickHandler(id, h.Func.Block)
		case "input":
			g.addInputHandler(id, h.Func.Block)
		case "change":
			g.addChangeHandler(id, h.Func.Block)
		default:
			g.addClickHandler(id, h.Func.Block)
		}
	}
}

// findUserComponent resolves a component by name, supporting both bare
// ("Foo") and namespace-qualified ("pkg.Foo") forms. Backed entirely by the
// IR package's symbol table so imported-package components are reachable.
func (g *htmlGen) findUserComponent(name string) *ast.ComponentDecl {
	c := g.findIRComponent(name)
	if c == nil || c.AST == nil {
		return nil
	}
	return c.AST
}

// findIRComponent is the IR-first component lookup used by both the AST
// fallback (via findUserComponent) and the IR dispatch path.
func (g *htmlGen) findIRComponent(name string) *ir.Component {
	if name == "" || g.pkg == nil {
		return nil
	}
	if ns, field, ok := strings.Cut(name, "."); ok {
		for _, imp := range g.pkg.Imports {
			if imp.Alias != ns || imp.Pkg == nil {
				continue
			}
			if sym, ok := imp.Pkg.Symbols.LookupComponent(field); ok {
				if c, ok := sym.(*ir.Component); ok {
					return c
				}
			}
		}
		return nil
	}
	if g.pkg.Symbols == nil {
		return nil
	}
	if sym, ok := g.pkg.Symbols.LookupComponent(name); ok {
		if c, ok := sym.(*ir.Component); ok {
			return c
		}
	}
	return nil
}

// renderIRUserComponent inlines a user component at its call site using the
// IR component definition. Mirrors renderStaticUserComponent but walks the
// IR body via renderIRStmt and sources params/vars/computed from ir.Component
// (which includes imported packages the AST doc never sees).
func (g *htmlGen) renderIRUserComponent(b *strings.Builder, n *ir.NodeInst, depth int) {
	comp := n.Component
	if comp == nil {
		// Unresolved — fall through to raw element render.
		g.renderRawElementIR(b, n, depth)
		return
	}

	g.componentDepth++
	if g.componentDepth > maxComponentDepth {
		g.componentDepth--
		return
	}
	defer func() { g.componentDepth-- }()

	g.componentInvocations++
	suffix := fmt.Sprintf("_%d", g.componentInvocations)

	savedLocals := make(map[string]bool)
	maps.Copy(savedLocals, g.scope.LocalVars)
	savedRenames := g.scope.Renames
	renames := make(map[string]string)
	if savedRenames != nil {
		maps.Copy(renames, savedRenames)
	}

	// Component params: bind call-site prop values (IR) or defaults to
	// uniquely-renamed JS constants so the inlined body references them.
	for _, p := range comp.Props {
		uniqueName := p.Name + suffix
		g.scope.LocalVars[p.Name] = true
		renames[p.Name] = uniqueName

		var valueExpr ir.Expr
		for _, pa := range n.Props {
			if pa.Name == p.Name {
				valueExpr = pa.Value
				break
			}
		}
		if valueExpr == nil {
			valueExpr = p.Default
		}
		var jsVal string
		if valueExpr != nil {
			jsVal = g.exprToJS(ir.ConvertExpr(valueExpr))
		} else {
			jsVal = `""`
		}
		g.componentParams = append(g.componentParams, componentParam{
			name:  uniqueName,
			value: jsVal,
		})
	}

	// Component computed (zero-param) funcs: register renames, then emit bodies.
	for _, fn := range comp.Funcs {
		if len(fn.Params) == 0 && len(fn.Block) == 1 {
			if _, isRet := fn.Block[0].(*ir.Return); isRet {
				uniqueName := fn.Name + suffix
				g.scope.LocalVars[fn.Name] = true
				renames[fn.Name] = uniqueName
			}
		}
	}
	g.scope.Renames = renames
	for _, fn := range comp.Funcs {
		if len(fn.Params) == 0 && len(fn.Block) == 1 {
			if ret, isRet := fn.Block[0].(*ir.Return); isRet && ret.Value != nil {
				uniqueName := fn.Name + suffix
				body := g.exprToJS(ir.ConvertExpr(ret.Value))
				g.componentParams = append(g.componentParams, componentParam{
					name:  uniqueName,
					value: body,
				})
			}
		}
	}

	// State vars: promote to parent with unique names.
	dataRenames := make(map[string]string)
	for _, dv := range comp.Vars {
		uniqueName := dv.Name + suffix
		g.scope.ModelFields[uniqueName] = true
		g.dt.ModelFields[uniqueName] = true
		g.scope.LocalVars[dv.Name] = true
		renames[dv.Name] = "state." + uniqueName
		dataRenames[dv.Name] = uniqueName
	}
	g.scope.Renames = renames

	savedDataRenames := g.dataRenames
	g.dataRenames = dataRenames

	savedSlot := g.irSlotChildren
	g.irSlotChildren = n.Children

	for _, s := range irPlatformBody(comp.Body, "html") {
		g.renderIRStmt(b, s, depth)
	}

	g.irSlotChildren = savedSlot
	g.dataRenames = savedDataRenames

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
	stateVars := g.stateVars()
	for _, dv := range stateVars {
		val := g.literalToJS(irExprToAST(dv.Init))
		stateFields = append(stateFields, dv.Name+": "+val)
	}
	b.WriteString(strings.Join(stateFields, ", "))
	b.WriteString("};\n\n")

	// Struct constructors
	structs := g.pkgStructs()
	for _, sd := range structs {
		var params []string
		var body []string
		for _, f := range sd.Fields {
			params = append(params, f.Name)
			body = append(body, f.Name)
		}
		fmt.Fprintf(b, "function %s(%s) { return {%s}; }\n",
			sd.Name, strings.Join(params, ", "), strings.Join(body, ", "))
	}
	if len(structs) > 0 {
		b.WriteString("\n")
	}

	// Computed functions (zero-arg expression-form)
	funcs := g.pkgFuncs()
	hasComputed := false
	for _, fn := range funcs {
		if len(fn.Params) == 0 && len(fn.Block) == 1 {
			if ret, ok := fn.Block[0].(*ir.Return); ok && ret.Value != nil {
				body := g.exprToJS(irExprToAST(ret.Value))
				fmt.Fprintf(b, "function $%s() { return %s; }\n", fn.Name, body)
				hasComputed = true
			}
		}
	}
	if hasComputed {
		b.WriteString("\n")
	}

	// User-defined functions
	emittedFuncs := false
	for _, fn := range funcs {
		if fn.AST != nil {
			g.emitJSFunc(b, fn.AST)
			emittedFuncs = true
		}
	}
	if emittedFuncs {
		b.WriteString("\n")
	}

	// Setters — emit for fields with @change handlers, timer controls, or preview mode
	for _, dv := range stateVars {
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
			if h.Name == "change" && h.AST != nil {
				for _, s := range h.AST.Body.Stmts {
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
	consts := g.pkgConsts()
	for _, c := range consts {
		val := g.literalToJS(irExprToAST(c.Init))
		fmt.Fprintf(b, "const %s = %s;\n", c.Name, val)
	}
	if len(consts) > 0 {
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
func (g *htmlGen) addTextUpdater(elemID string, expr ir.Expr) {
	jsExpr := g.lang.TranslateIRExpr(expr, g.scope)
	deps := g.exprDepsIR(expr)
	name := fmt.Sprintf("$u_%s_text", elemID[1:])
	g.updates = append(g.updates, updateFunc{
		funcName: name,
		body:     fmt.Sprintf("%s.textContent = %s;", elemID, jsExpr),
		deps:     deps,
	})
}

func (g *htmlGen) addTextContentUpdater(elemID string, expr ir.Expr) {
	jsExpr := g.lang.TranslateIRExpr(expr, g.scope)
	deps := g.exprDepsIR(expr)
	name := fmt.Sprintf("$u_%s_text", elemID[1:])
	g.updates = append(g.updates, updateFunc{
		funcName: name,
		body:     fmt.Sprintf("%s.textContent = %s;", elemID, jsExpr),
		deps:     deps,
	})
}

func (g *htmlGen) addAttrUpdater(elemID, attr string, expr ir.Expr) {
	jsExpr := g.lang.TranslateIRExpr(expr, g.scope)
	deps := g.exprDepsIR(expr)
	name := fmt.Sprintf("$u_%s_%s", elemID[1:], attr)
	g.updates = append(g.updates, updateFunc{
		funcName: name,
		body:     fmt.Sprintf("%s.setAttribute(%q, %s);", elemID, attr, jsExpr),
		deps:     deps,
	})
}

func (g *htmlGen) addDisabledUpdater(elemID string, expr ir.Expr) {
	jsExpr := g.lang.TranslateIRExpr(expr, g.scope)
	deps := g.exprDepsIR(expr)
	name := fmt.Sprintf("$u_%s_disabled", elemID[1:])
	g.updates = append(g.updates, updateFunc{
		funcName: name,
		body:     fmt.Sprintf("%s.disabled = %s;", elemID, jsExpr),
		deps:     deps,
	})
}

func (g *htmlGen) addIfUpdater(elemID string, expr ir.Expr) {
	jsExpr := g.lang.TranslateIRExpr(expr, g.scope)
	deps := g.exprDepsIR(expr)
	name := fmt.Sprintf("$u_%s_if", elemID[1:])
	g.updates = append(g.updates, updateFunc{
		funcName: name,
		body:     fmt.Sprintf("%s.style.display = %s ? \"\" : \"none\";", elemID, jsExpr),
		deps:     deps,
	})
}

// addElseUpdater adds a display updater for the else branch of an if statement.
func (g *htmlGen) addElseUpdater(elemID string, cond ir.Expr) {
	jsExpr := g.lang.TranslateIRExpr(cond, g.scope)
	deps := g.exprDepsIR(cond)
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
				mutated := mutatedFieldsAST(changeEvt.Body.Stmts[0])
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
	deps := exprDepsAST(expr, g.dt.ModelFields, g.dt.ComputedFields, g.dt.ComputedDeps)
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

func (g *htmlGen) addClickHandler(elemID string, body []ir.Stmt) {
	if len(body) == 0 {
		return
	}
	var stmts []string
	var mutated map[string]bool
	for _, s := range body {
		stmts = append(stmts, g.lang.TranslateIRMutation(s, g.scope)...)
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

func (g *htmlGen) addInputHandler(elemID string, body []ir.Stmt) {
	if len(body) == 0 {
		return
	}
	savedEvent := g.scope.EventVar
	g.scope.EventVar = "e.target"
	var stmts []string
	mutated := make(map[string]bool)
	for _, s := range body {
		stmts = append(stmts, g.lang.TranslateIRMutation(s, g.scope)...)
		maps.Copy(mutated, codegen.MutatedFields(s))
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

func (g *htmlGen) addChangeHandler(elemID string, body []ir.Stmt) {
	if len(body) == 0 {
		return
	}
	var stmts []string
	mutated := make(map[string]bool)
	for _, s := range body {
		stmts = append(stmts, g.lang.TranslateIRMutation(s, g.scope)...)
		maps.Copy(mutated, codegen.MutatedFields(s))
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
