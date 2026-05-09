package html

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"html"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/internal/asset"
	"git.duckfam.us/jonathan/sngl/internal/htmlutil"
	"git.duckfam.us/jonathan/sngl/internal/lower"
	"git.duckfam.us/jonathan/sngl/internal/parser"
	"git.duckfam.us/jonathan/sngl/ir"
)

//go:embed html.sngl
var pkgSource string

var pkgDocs []*ast.Document

func init() {
	doc, err := parser.Parse("html.sngl", []byte(pkgSource))
	if err != nil {
		panic(fmt.Errorf("platform html init: parsing html.sngl: %w", err))
	}
	pkgDocs = []*ast.Document{doc}
	codegen.RegisterPlatform(&Generator{})
}

// Generator implements codegen.PlatformGenerator for HTML output.
type Generator struct{}

func (g *Generator) PlatformIdentifier() string { return "html" }
func (g *Generator) Description() string {
	return "Web output. Static site by default, or a language-driven HTTP server when paired with a language that implements HTTPCompiler."
}
func (g *Generator) Package() []*ast.Document { return pkgDocs }
func (g *Generator) Resolve(identifier string) ir.Symbol {
	// HTML accepts any tag name as a valid element.
	return &ir.Component{Name: identifier}
}
func (g *Generator) Capabilities() lower.Caps {
	return lower.Caps{NoReactivity: true, NoAsyncReactive: true}
}

// SupportedLangs returns "none" (static-site default) plus any registered
// language whose translator implements codegen.HTTPCompiler. The first entry
// is the default when --lang is omitted.
func (g *Generator) SupportedLangs() []string {
	out := []string{"none"}
	var httpLangs []string
	for _, name := range codegen.Langs() {
		if _, ok := codegen.LookupLang(name).(codegen.HTTPCompiler); ok {
			httpLangs = append(httpLangs, name)
		}
	}
	sort.Strings(httpLangs)
	return append(out, httpLangs...)
}

func (g *Generator) IsLanguageSupported(l ir.Language) bool {
	if l.LanguageIdentifier() == "none" {
		return true
	}
	_, ok := l.(codegen.HTTPCompiler)
	return ok
}

func (g *Generator) Generate(req *codegen.Request) (*codegen.Response, error) {
	if req.Lang.LanguageIdentifier() == "none" {
		if err := rejectDynamicHrefs(req); err != nil {
			return nil, err
		}
		c := &compilation{}
		m, err := c.BuildMutationModel(req, codegen.AnalyzeCommon(req.Pkg))
		if err != nil {
			return &codegen.Response{Error: err.Error()}, nil
		}
		return c.EmitFromMutation(m, req)
	}
	if _, ok := req.Lang.(codegen.HTTPCompiler); ok {
		return g.generateRoutes(req)
	}
	return nil, fmt.Errorf("html: unsupported lang %q", req.Lang.LanguageIdentifier())
}

// rejectDynamicHrefs errors when static mode (lang=none) encounters a window
// whose href isn't a literal string — the build can't resolve {param} routes
// without a server.
func rejectDynamicHrefs(req *codegen.Request) error {
	ctx := codegen.NewCodegenCtx(req, "html")
	for _, win := range ctx.Windows() {
		if win.Window == nil || win.Window.Href == nil {
			continue
		}
		if _, ok := codegen.IRLiteralString(win.Window.Href); !ok {
			return fmt.Errorf("html: window %q has a dynamic href — static site cannot serve it; compile with a server language (e.g. --lang go)", win.Name)
		}
	}
	return nil
}

// NewMutationCompiler returns a fresh per-request MutationModelEmitter.
func (g *Generator) NewMutationCompiler() codegen.MutationModelEmitter {
	return &compilation{}
}

// compilation holds per-request build state flowing between
// BuildMutationModel and EmitFromMutation.
type compilation struct {
	assetFiles []*codegen.OutputFile
	windows    []htmlWindowOutput
}

// htmlConfig captures the html platform's options. Field names mirror
// html.sngl options (camelCase → PascalCase via codegen.ApplyOptions).
type htmlConfig struct {
	Preview     bool
	Test        bool
	Stylesheet  string
	ProjectDir  string
	Package     string
	Main        bool
	Framework   string
	Listen      string
	Minify      bool
	NoCacheBust bool

	// Stdlib globals (lib/options.sngl).
	Name        string
	Icon        string
	Description string
	Version     string
}

type htmlWindowOutput struct {
	name  string
	bytes []byte
}

var (
	_ codegen.MutationModelEmitter    = (*compilation)(nil)
	_ codegen.MutationCompilerFactory = (*Generator)(nil)
)

func (c *compilation) BuildMutationModel(req *codegen.Request, analysis *codegen.CommonAnalysis) (*codegen.MutationModel, error) {
	jsLang := codegen.LookupLang("js")
	if jsLang == nil {
		return nil, fmt.Errorf("html: js translator not registered")
	}

	var opts htmlConfig
	if err := codegen.ApplyOptions(&opts, req.Options); err != nil {
		return nil, fmt.Errorf("html: %w", err)
	}

	// Copy file:// assets resolved during optimization. The optimizer has
	// already chosen the OutPath (with cache-bust hash if enabled) and may
	// have stashed the file bytes; fall back to a fresh read when absent.
	for _, fa := range req.FileAssets {
		data := fa.Data
		if data == nil {
			d, err := os.ReadFile(fa.SrcPath)
			if err != nil {
				continue
			}
			data = d
		}
		c.assetFiles = append(c.assetFiles, codegen.BytesFile(fa.OutPath, data))
	}

	// Resolve stylesheet option: source path relative to project dir.
	stylesheetURL := ""
	if opts.Stylesheet != "" {
		absPath := opts.Stylesheet
		if opts.ProjectDir != "" && !filepath.IsAbs(opts.Stylesheet) {
			absPath = filepath.Join(opts.ProjectDir, opts.Stylesheet)
		}
		data, err := os.ReadFile(absPath)
		if err == nil {
			base := filepath.Base(opts.Stylesheet)
			if !opts.NoCacheBust {
				base = asset.HashedName(base, data)
			}
			outName := "assets/" + base
			stylesheetURL = "/" + outName
			c.assetFiles = append(c.assetFiles, codegen.BytesFile(outName, data))
		}
	}

	// Build WASM for imported packages that have runtime-used functions.
	var wasmLoaderHTML string
	projectDir := opts.ProjectDir
	projectFS := req.ProjectFS
	if projectFS == nil && projectDir != "" {
		projectFS = os.DirFS(projectDir)
	}
	wasmPkgs := collectWASMPackages(req.Pkg, projectFS, projectDir)
	if len(wasmPkgs) > 0 {
		wasmExecURL := ""
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

			if wasmExecURL == "" {
				wasmExecData, err := wc.WASMExecJS()
				if err == nil {
					name := "wasm_exec.js"
					if !opts.NoCacheBust {
						name = asset.HashedName(name, wasmExecData)
					}
					outPath := "assets/" + name
					c.assetFiles = append(c.assetFiles, codegen.BytesFile(outPath, wasmExecData))
					wasmExecURL = "/" + outPath
				}
			}

			wasmBytes, err := wc.BuildWASM(projectDir, wp.importPath, wp.funcs)
			if err != nil {
				return nil, fmt.Errorf("wasm build: %v", err)
			}
			wasmName := wp.namespace + ".wasm"
			if !opts.NoCacheBust {
				wasmName = asset.HashedName(wasmName, wasmBytes)
			}
			wasmFile := "assets/" + wasmName
			c.assetFiles = append(c.assetFiles, codegen.BytesFile(wasmFile, wasmBytes))
			loaderScripts = append(loaderScripts, fmt.Sprintf(
				`  const _go_%s = new Go();
  WebAssembly.instantiateStreaming(fetch("/%s"), _go_%s.importObject).then(r => { _go_%s.run(r.instance); });`,
				wp.namespace, wasmFile, wp.namespace, wp.namespace))
		}
		if len(loaderScripts) > 0 {
			wasmLoaderHTML = fmt.Sprintf(
				"  <script src=\"%s\"></script>\n  <script>\n  window.__sngl_externs = window.__sngl_externs || {};\n%s\n  </script>\n",
				wasmExecURL, strings.Join(loaderScripts, "\n"))
		}
	}
	ctx := codegen.NewCodegenCtx(req, "html")

	// IR-driven rendering: one file per window. Packages with no main
	// component and no explicit windows still emit an empty index.html so
	// callers can verify codegen at least succeeded.
	irWindows := ctx.Windows()
	if len(irWindows) == 0 {
		gen := newHTMLGenFromCtx(ctx, jsLang, opts)
		gen.wasmLoader = wasmLoaderHTML
		gen.wasmPkgs = wasmPkgs
		gen.stylesheet = stylesheetURL
		gen.projectDir = projectDir
		gen.projectFS = projectFS
		body, err := gen.generate()
		if err != nil {
			return nil, err
		}
		src := codegen.Header("html", req.Source, "<!-- ", " -->") + body
		if opts.Minify {
			min, err := minifyHTML(src)
			if err != nil {
				return nil, err
			}
			src = min
		}
		c.windows = append(c.windows, htmlWindowOutput{name: "index.html", bytes: []byte(src)})
		return ctx.BuildMutation(nil), nil
	}
	singleWindow := len(irWindows) == 1
	staticMode := req.Lang.LanguageIdentifier() == "none"
	var mainStmts []ir.Stmt
	seenPaths := map[string]ast.Pos{}
	for i, win := range irWindows {
		var name string
		switch {
		case !staticMode:
			// Route mode: file paths aren't used by the language compiler
			// (it indexes by WindowIdx and reads bytes directly). Skip
			// literal-href + collision checks; dynamic /{param} routes are
			// expected here.
			name = fmt.Sprintf("window_%d", i)
		case singleWindow && (win.Window == nil || win.Window.Href == nil):
			name = "index.html"
		default:
			href, ok := codegen.IRLiteralString(win.Window.Href)
			if !ok {
				return nil, fmt.Errorf("html: window %q has a non-literal href after folding (internal error)", win.Name)
			}
			name = pathFromHref(href)
		}
		if staticMode {
			if prev, dup := seenPaths[name]; dup {
				pos := ast.Pos{}
				if win.Window != nil && win.Window.AST != nil {
					pos = win.Window.AST.Pos
				}
				return nil, fmt.Errorf("html: window output path collision: %q emitted by both %s and %s", name, prev, pos)
			}
			if win.Window != nil && win.Window.AST != nil {
				seenPaths[name] = win.Window.AST.Pos
			} else {
				seenPaths[name] = ast.Pos{}
			}
		}
		gen := newHTMLGenFromCtx(ctx, jsLang, opts)
		gen.wasmLoader = wasmLoaderHTML
		gen.wasmPkgs = wasmPkgs
		gen.stylesheet = stylesheetURL
		gen.projectDir = projectDir
		gen.projectFS = projectFS
		gen.irBodyStmts = win.Body
		if win.Window != nil {
			if s, ok := codegen.IRLiteralString(win.Window.Title); ok {
				gen.title = s
			}
			if s, ok := codegen.IRLiteralString(win.Window.Favicon); ok {
				gen.favicon = s
			}
		}
		body, err := gen.generate()
		if err != nil {
			return nil, err
		}
		src := codegen.Header("html", req.Source, "<!-- ", " -->") + body
		if opts.Minify {
			min, err := minifyHTML(src)
			if err != nil {
				return nil, err
			}
			src = min
		}
		c.windows = append(c.windows, htmlWindowOutput{name: name, bytes: []byte(src)})
		if mainStmts == nil {
			mainStmts = win.Body
		}
	}
	return ctx.BuildMutation(mainStmts), nil
}

func (c *compilation) EmitFromMutation(_ *codegen.MutationModel, req *codegen.Request) (*codegen.Response, error) {
	files := append([]*codegen.OutputFile{}, c.assetFiles...)
	for _, w := range c.windows {
		files = append(files, codegen.BytesFile(w.name, w.bytes))
	}
	return &codegen.Response{Files: files}, nil
}

// htmlGen holds all state for generating a single HTML file.
type htmlGen struct {
	lang codegen.LangTranslator

	// Type-checked package and expression context (IR-driven).
	pkg *ir.Package
	ctx *codegen.ExprCtx

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

	// Inlined component state fields (unique name → initial JS value)
	inlinedStateInits []componentParam

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

	// WASM packages needing extern bindings at the top of the <script> block.
	wasmPkgs []wasmPackage

	// Project root for resolving native module specifiers (`./lib`, etc.)
	// when bundling the inline <script> through esbuild. projectFS is the
	// matching filesystem; both are used together so the same code path
	// works for CLI (os.DirFS) and the in-memory playground.
	projectDir string
	projectFS  fs.FS

	// Preview mode: add data-sngl-line/col attributes, ensure all elements have IDs
	preview bool

	// Test mode: emit data-key, id, class attributes for test element lookup
	testMode bool

	// Minify inline JS via esbuild (and HTML in BuildMutationModel).
	minify bool

	// Component invocation counter for unique param names
	componentInvocations int

	// dataRenames maps original component var names to promoted unique names
	// during component inlining, so handler MutatedFields can be remapped.
	dataRenames map[string]string

	// Component nesting depth for recursion protection
	componentDepth int

	// usesI18n is true when the IR package contains i18n intrinsic calls.
	// The bundle entry will include the i18n runtime import and optional
	// manifest initializer.
	usesI18n bool

	// irSlotChildren holds the caller's children during component inlining;
	// the body-level `slot` pseudo-element projects them into position.
	irSlotChildren []ir.Stmt

	// irBodyStmts is the IR body rendered for the current window.
	irBodyStmts []ir.Stmt

	// idToNode maps each emitted element id (either an alloc'd "$N" or a
	// pre-assigned "__nN" from internal/lower NoReactivity) back to its
	// originating NodeInst. Task 4 reads this when translating
	// reactive-update Assigns inside handler / timer / change-setter
	// bodies, where the only context is the id string. $N entries are
	// harmless noise: NoReactivity never produces IsElementRef Assigns
	// against $-prefixed names, so Task 4's lookup never targets them.
	idToNode map[string]*ir.NodeInst

	// loweredRefs collects every __n* id that appears as the target of a
	// reactive-update Assign translated by translateHandlerStmt. Each
	// unique id gets a top-level `const __nN = document.querySelector(...)`
	// declaration in emitScript's element-references block, so handlers
	// emit a bare identifier rather than a fresh querySelector per write.
	loweredRefs map[string]bool
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
	initOnly bool // run only on initial sync; lowering already injects mutation-side updates inline
}

type eventHandler struct {
	elemID  string
	event   string // "click", "input", "change"
	body    string // JS statements
	mutated map[string]bool
	isAsync bool
}

type timerDef struct {
	index      int
	intervalMs int
	activeVar  string
	body       string
	mutated    map[string]bool
	bodyAsync  bool
}

func newHTMLGen(pkg *ir.Package, lang codegen.LangTranslator, opts htmlConfig) *htmlGen {
	common := codegen.AnalyzeCommon(pkg)

	g := &htmlGen{
		pkg:            pkg,
		lang:           lang,
		CommonAnalysis: common,
		preview:        opts.Preview,
		testMode:       opts.Test,
		minify:         opts.Minify,
		idToNode:       make(map[string]*ir.NodeInst),
		loweredRefs:    make(map[string]bool),
		usesI18n:       hasI18nCalls(pkg),
	}

	g.scope = &codegen.ExprScope{
		ModelFields:       common.ModelFields,
		ComputedFields:    common.ComputedFields,
		FuncNames:         common.FuncNames,
		ExternFuncs:       common.ExternFuncs,
		ExternVars:        common.ExternVars,
		LocalVars:         make(map[string]bool),
		NeededHelpers:     common.Helpers,
		BundledNativePkgs: collectBundledNativePkgs(pkg),
		Pkg:               pkg,
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
func newHTMLGenFromCtx(ctx *codegen.CodegenCtx, lang codegen.LangTranslator, opts htmlConfig) *htmlGen {
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

// prewalkNodes seeds g.idToNode with every NodeInst in the package that
// carries an `__n*` id from the NoReactivity lowering. See generate().
func (g *htmlGen) prewalkNodes() {
	if g.pkg == nil {
		return
	}
	var visit func(s ir.Stmt)
	visitStmts := func(stmts []ir.Stmt) {
		for _, s := range stmts {
			visit(s)
		}
	}
	visit = func(s ir.Stmt) {
		switch n := s.(type) {
		case *ir.NodeInst:
			if n == nil {
				return
			}
			if strings.HasPrefix(n.ID, "__n") {
				g.idToNode[n.ID] = n
			}
			visitStmts(n.Children)
			for _, h := range n.Handlers {
				if h.Func != nil {
					visitStmts(h.Func.Block)
				}
			}
		case *ir.If:
			visitStmts(n.Body)
			visitStmts(n.Else)
		case *ir.For:
			visitStmts(n.Body)
			visitStmts(n.Else)
		case *ir.Window:
			visitStmts(n.Body)
		case *ir.SlotInst:
			visitStmts(n.Children)
		case *ir.ErrorBoundary:
			visitStmts(n.Children)
		}
	}
	for _, c := range g.pkg.Components {
		if c == nil {
			continue
		}
		visitStmts(c.Body)
		for _, fn := range c.Funcs {
			if fn != nil {
				visitStmts(fn.Block)
			}
		}
	}
	for _, w := range g.pkg.Windows {
		if w == nil {
			continue
		}
		visitStmts(w.Body)
	}
	for _, fn := range g.pkg.Funcs {
		if fn != nil {
			visitStmts(fn.Block)
		}
	}
}

// nodeID returns n.ID when NoReactivity has pre-assigned one (`__n*`),
// otherwise allocates a fresh `$N`. Records the chosen id in g.idToNode
// so reactive-update Assigns inside handler bodies can be translated
// against the originating NodeInst (see translateHandlerStmt). When n
// is nil the id is allocated but not recorded — Task 4's lookup falls
// through to the JS-default write for that id; callers passing a real
// NodeInst always get an idToNode entry.
func (g *htmlGen) nodeID(n *ir.NodeInst) string {
	var id string
	if n != nil && strings.HasPrefix(n.ID, "__n") {
		id = n.ID
	} else {
		id = g.allocID()
	}
	if n != nil {
		g.idToNode[id] = n
	}
	return id
}

func (g *htmlGen) generate() (string, error) {
	// Prewalk every NodeInst with a NoReactivity-assigned __n* id so
	// idToNode is fully populated before any handler translates a
	// reactive-update Assign. Without this, handlers emitted earlier in
	// the static walk than the destination text node fall through to the
	// JS-default `el.value = …` path — wrong for spans, which need
	// `el.textContent = …`.
	g.prewalkNodes()

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
		const defaultCSS = "* { margin: 0; padding: 0; box-sizing: border-box; }\n" +
			"body { font-family: system-ui, sans-serif; }\n"
		css, err := maybeMinifyCSS(defaultCSS, g.minify)
		if err != nil {
			return "", err
		}
		b.WriteString("  <style>\n")
		b.WriteString(css)
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
		var cssBuf strings.Builder
		for _, css := range g.Styles {
			cssBuf.WriteString(css)
			cssBuf.WriteByte('\n')
		}
		css, err := maybeMinifyCSS(cssBuf.String(), g.minify)
		if err != nil {
			return "", err
		}
		b.WriteString("<style>\n")
		b.WriteString(css)
		b.WriteString("</style>\n")
	}

	// Only emit <script> if there's actual runtime JS to execute. When the
	// script references js:// imports, prepend ES `import * as` lines for
	// each module and send the whole script through esbuild so module
	// bodies are inlined and tree-shaken.
	var scriptBuf strings.Builder
	g.emitScript(&scriptBuf)
	script := scriptBuf.String()

	// i18n runtime preamble: when i18n intrinsics are used, prepend the
	// bundled runtime as a `var i18n = (()=>{...})()` IIFE so that the
	// generated `i18n.getTranslator().tr(...)` calls resolve without forcing
	// the main script through a separate esbuild pass (which would reject
	// pre-existing codegen quirks in test fixtures).
	if g.usesI18n && strings.TrimSpace(script) != "" {
		snippet, err := i18nRuntimeSnippet()
		if err != nil {
			return "", fmt.Errorf("i18n runtime bundle: %w", err)
		}
		var preamble strings.Builder
		// Manifest init runs before getTranslator() is first called.
		if manifestJS := i18nManifestJS(g.projectDir, g.projectFS); manifestJS != "" {
			preamble.WriteString(manifestJS)
		}
		preamble.WriteString(snippet)
		preamble.WriteString("\n")
		script = preamble.String() + script
	}

	if strings.TrimSpace(script) != "" {
		// Run through esbuild when there are real imports to resolve, or when
		// minify is on. Otherwise emit the script verbatim — esbuild surfaces
		// errors on a few pre-existing codegen quirks (e.g. `5.clamp(...)` in
		// test-mode output) that don't matter for the SNGL test runner.
		if len(g.scope.NativeImports) > 0 || g.minify {
			var preludeBuf strings.Builder
			if len(g.scope.NativeImports) > 0 {
				mods := make([]string, 0, len(g.scope.NativeImports))
				for m := range g.scope.NativeImports {
					mods = append(mods, m)
				}
				sort.Strings(mods)
				for _, m := range mods {
					fmt.Fprintf(&preludeBuf, "import * as %s from %q;\n", codegen.NativeAlias(m), m)
				}
				preludeBuf.WriteString("\n")
			}
			bundled, err := bundleNativeScript(preludeBuf.String()+script, g.projectFS, g.minify)
			if err != nil {
				return "", err
			}
			script = bundled
		}
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

	return result, nil
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
	case *ir.ErrorBoundary:
		for _, child := range n.Children {
			g.renderIRStmt(b, child, depth)
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
	name := irCallName(n.Call)
	if name == "" {
		return nil
	}
	props := make([]ir.Arg, 0, len(n.Call.Args))
	for _, a := range n.Call.Args {
		props = append(props, ir.Arg{Name: a.Name, Value: a.Value})
	}
	return &ir.NodeInst{
		AST:   n.AST,
		Name:  name,
		Props: props,
	}
}

// irCallName extracts the bare or namespace-qualified name from an IR Call
// expression. Namespace-resolved element calls (ns.Foo where ns is resolved
// via Resolve()) leave Call.Func nil and Call.Receiver set to the namespace
// ident — reassemble "ns.Foo" from Receiver + the AST back-reference.
func irCallName(call *ir.Call) string {
	if call == nil {
		return ""
	}
	if call.Func != nil {
		return call.Func.Name
	}
	if call.Receiver != nil && call.AST != nil {
		if sel, ok := call.AST.Func.(*ast.SelectExpr); ok {
			if id, ok := sel.Operand.(*ast.IdentExpr); ok {
				return id.Name + "." + sel.Field
			}
		}
	}
	return ""
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
		// Try resolving a user component by name (covers bodyless calls
		// promoted from ir.CallStmt that don't carry Component/AST back-refs).
		if comp := g.findIRComponent(n.Name); comp != nil && comp.AST != nil {
			if n.Component == nil {
				n.Component = comp
			}
			g.renderIRUserComponent(b, n, depth)
			return
		}
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

// renderIRFor emits a for-loop placeholder and registers list/else updaters
// from the IR body.
func (g *htmlGen) renderIRFor(b *strings.Builder, n *ir.For, depth int) {
	indent := strings.Repeat("  ", depth)
	id := g.allocID()
	fmt.Fprintf(b, "%s<div id=\"%s\"></div>\n", indent, id)
	g.addForStmtUpdater(id, n)
	if len(n.Else) > 0 {
		elseID := g.allocID()
		fmt.Fprintf(b, "%s<div id=\"%s\">\n", indent, elseID)
		for _, s := range n.Else {
			g.renderIRStmt(b, s, depth+1)
		}
		fmt.Fprintf(b, "%s</div>\n", indent)
		g.addForElseStmtUpdater(id, elseID, n)
	}
}

// stateVars returns all state (non-const) variables for the compiled
// pts returns the funcvar points-to analysis results for the package, or nil
// if not yet computed. Callers use this with ir.BlockHasFuncvarAsyncCall.
func (g *htmlGen) pts() *ir.PointsToInfo {
	if g.pkg == nil {
		return nil
	}
	return g.pkg.PointsTo
}

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

// preservesWhitespace reports whether a raw HTML tag treats whitespace in
// its content as significant. For such tags the pretty-printer must not
// inject indentation/newlines between the open tag and children, because
// those characters render literally.
func preservesWhitespace(tag string) bool {
	switch tag {
	case "pre", "textarea":
		return true
	}
	return false
}

// stripInterTagWhitespace collapses whitespace that sits between adjacent
// HTML tags in s, so children of a <pre> emitted by the pretty-printer do
// not carry their indentation into the rendered output. Whitespace inside
// text nodes (i.e. not bounded by `>` and `<`) is preserved.
func stripInterTagWhitespace(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	n := len(s)
	i := 0
	for i < n {
		c := s[i]
		if c == '>' {
			b.WriteByte('>')
			j := i + 1
			for j < n && (s[j] == ' ' || s[j] == '\t' || s[j] == '\n' || s[j] == '\r') {
				j++
			}
			if j < n && s[j] == '<' {
				i = j
				continue
			}
			i++
			continue
		}
		b.WriteByte(c)
		i++
	}
	out := b.String()
	return strings.TrimRight(out, " \t\n\r")
}

// isStdlibComponentName reports whether a name is a built-in stdlib
// component that the html platform handles via dedicated renderStaticX
// helpers rather than inlined user-component expansion.
func isStdlibComponentName(name string) bool {
	switch name {
	case "vbox", "hbox", "stack", "scroll", "spacer", "text", "button", "input",
		"image", "checkbox", "radio", "toggle", "select", "textarea", "progress",
		"spinner", "badge", "tabs", "link", "divider", "modal", "drawer",
		"tooltip", "popover", "splitview", "table", "tree", "menu",
		"menubar", "toolbar", "datepicker", "chip", "avatar", "card", "slot",
		"window", "timer":
		return true
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
			fmt.Fprintf(b, "%s<div id=%q style=\"flex:1\"%s></div>\n", indent, id, g.previewAttrs(n))
		} else {
			fmt.Fprintf(b, "%s<div style=\"flex:1\"></div>\n", indent)
		}
	case "scroll":
		id := ""
		style := g.buildCSSStyle(n)
		style = htmlutil.AppendCSS(style, "overflow", "auto")
		if g.nodeIsReactive(n) {
			id = g.nodeID(n)
		}
		g.writeOpenTag(b, "div", id, style, n, depth)
		for _, s := range n.Children {
			g.renderIRStmt(b, s, depth+1)
		}
		fmt.Fprintf(b, "%s</div>\n", indent)
	case "stack":
		style := g.buildCSSStyle(n)
		style = htmlutil.AppendCSS(style, "position", "relative")
		g.writeOpenTag(b, "div", "", style, n, depth)
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
		style := g.buildCSSStyle(n)
		value := g.evalStaticString(nodeProps(n), "value")
		maxVal := g.evalStaticString(nodeProps(n), "max")
		if maxVal == "" {
			maxVal = "1"
		}
		id := ""
		if g.nodeIsReactive(n) || g.preview {
			id = g.nodeID(n)
		}
		fmt.Fprintf(b, "%s<progress", indent)
		if id != "" {
			fmt.Fprintf(b, " id=%q", id)
			if strings.HasPrefix(id, "__n") {
				fmt.Fprintf(b, " data-sngl-id=%q", id)
			}
		}
		if style != "" {
			fmt.Fprintf(b, " style=\"%s\"", style)
		}
		fmt.Fprintf(b, " value=\"%s\" max=\"%s\"", value, maxVal)
		g.writeUserAttrs(b, id, n)
		b.WriteString(g.previewAttrs(n))
		b.WriteString("></progress>\n")
		if id != "" {
			if valExpr := codegen.NodeProp(n, "value"); valExpr != nil && codegen.IRIsReactive(valExpr) {
				g.addAttrUpdater(id, "value", valExpr)
			}
		}
	case "spinner":
		g.CommonAnalysis.AddStyle("@keyframes sngl-spin { to { transform: rotate(360deg); } }")
		g.CommonAnalysis.AddStyle(".sngl-spinner { display: inline-block; width: 1em; height: 1em; border: 2px solid currentColor; border-right-color: transparent; border-radius: 50%; animation: sngl-spin 0.75s linear infinite; vertical-align: middle; }")
		style := g.buildCSSStyle(n)
		label := g.evalStaticString(nodeProps(n), "label")
		id := ""
		if g.preview {
			id = g.allocID()
		}
		g.writeOpenTag(b, "span", id, style, n, depth)
		fmt.Fprintf(b, "<span class=\"sngl-spinner\"></span> %s</span>\n", html.EscapeString(label))
	case "badge":
		style := g.buildCSSStyle(n)
		style = htmlutil.AppendCSS(style, "display", "inline-block")
		style = htmlutil.AppendCSS(style, "padding", "2px 8px")
		style = htmlutil.AppendCSS(style, "border-radius", "12px")
		style = htmlutil.AppendCSS(style, "font-size", "12px")
		value := g.evalStaticString(nodeProps(n), "value")
		id := ""
		if g.nodeIsReactive(n) || g.preview {
			id = g.nodeID(n)
		}
		g.writeOpenTag(b, "span", id, style, n, depth)
		fmt.Fprintf(b, "%s</span>\n", html.EscapeString(value))
		if codegen.IRIsReactive(codegen.NodeProp(n, "value")) {
			g.addTextUpdater(id, codegen.NodeProp(n, "value"))
		}
	case "tabs":
		g.renderStaticTabs(b, n, depth)
	case "link":
		style := g.buildCSSStyle(n)
		text := g.evalStaticString(nodeProps(n), "text")
		href := g.evalStaticString(nodeProps(n), "href")
		target := g.evalStaticString(nodeProps(n), "target")
		id := ""
		if g.nodeIsReactive(n) || g.preview {
			id = g.nodeID(n)
		}
		fmt.Fprintf(b, "%s<a", indent)
		if id != "" {
			fmt.Fprintf(b, " id=%q", id)
			if strings.HasPrefix(id, "__n") {
				fmt.Fprintf(b, " data-sngl-id=%q", id)
			}
		}
		if style != "" {
			fmt.Fprintf(b, " style=\"%s\"", style)
		}
		fmt.Fprintf(b, " href=\"%s\"", html.EscapeString(href))
		if target == "blank" {
			b.WriteString(` target="_blank" rel="noopener"`)
		}
		g.writeUserAttrs(b, id, n)
		b.WriteString(g.previewAttrs(n))
		fmt.Fprintf(b, ">%s</a>\n", html.EscapeString(text))
		if h := codegen.NodeHandler(n, "click"); h != nil && h.Func != nil && len(h.Func.Block) > 0 {
			if id == "" {
				id = g.nodeID(n)
			}
			g.addClickHandler(id, h.Func.Block)
		}
	case "divider":
		style := g.buildCSSStyle(n)
		label := g.evalStaticString(nodeProps(n), "label")
		id := ""
		if g.preview {
			id = g.allocID()
		}
		if label != "" {
			style = htmlutil.AppendCSS(style, "display", "flex")
			style = htmlutil.AppendCSS(style, "align-items", "center")
			style = htmlutil.AppendCSS(style, "gap", "8px")
			g.writeOpenTag(b, "div", id, style, n, depth)
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
			b.WriteString(g.previewAttrs(n))
			b.WriteString(" />\n")
		}

	// --- Tier 3: Overlays & Layout ---
	case "modal":
		g.renderStaticModal(b, n, depth)
	case "drawer":
		g.renderStaticConditionalContainer(b, n, depth, "div")
	case "tooltip":
		// Wrap child with title attribute
		title := g.evalStaticString(nodeProps(n), "text")
		fmt.Fprintf(b, "%s<div title=\"%s\">\n", indent, html.EscapeString(title))
		for _, s := range n.Children {
			g.renderIRStmt(b, s, depth+1)
		}
		fmt.Fprintf(b, "%s</div>\n", indent)
	case "popover":
		g.renderStaticConditionalContainer(b, n, depth, "div")
	case "splitview":
		style := g.buildCSSStyle(n)
		style = htmlutil.AppendCSS(style, "display", "flex")
		direction := g.evalStaticString(nodeProps(n), "direction")
		if direction == "vertical" {
			style = htmlutil.AppendCSS(style, "flex-direction", "column")
		} else {
			style = htmlutil.AppendCSS(style, "flex-direction", "row")
		}
		id := ""
		if g.preview {
			id = g.allocID()
		}
		g.writeOpenTag(b, "div", id, style, n, depth)
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
		style := g.buildCSSStyle(n)
		style = htmlutil.AppendCSS(style, "display", "flex")
		style = htmlutil.AppendCSS(style, "gap", "4px")
		id := ""
		if g.preview {
			id = g.allocID()
		}
		g.writeOpenTag(b, "nav", id, style, n, depth)
		fmt.Fprintf(b, "</nav>\n")
	case "toolbar":
		style := g.buildCSSStyle(n)
		style = htmlutil.AppendCSS(style, "display", "flex")
		style = htmlutil.AppendCSS(style, "gap", "4px")
		style = htmlutil.AppendCSS(style, "align-items", "center")
		id := ""
		if g.preview {
			id = g.allocID()
		}
		g.writeOpenTag(b, "div", id, style, n, depth)
		for _, s := range n.Children {
			g.renderIRStmt(b, s, depth+1)
		}
		fmt.Fprintf(b, "%s</div>\n", indent)

	// --- Tier 5: Mobile & Specialized ---
	case "datepicker":
		g.renderStaticDatepicker(b, n, depth)
	case "chip":
		style := g.buildCSSStyle(n)
		style = htmlutil.AppendCSS(style, "display", "inline-flex")
		style = htmlutil.AppendCSS(style, "align-items", "center")
		style = htmlutil.AppendCSS(style, "padding", "4px 12px")
		style = htmlutil.AppendCSS(style, "border-radius", "16px")
		style = htmlutil.AppendCSS(style, "border", "1px solid #ccc")
		style = htmlutil.AppendCSS(style, "font-size", "14px")
		label := g.evalStaticString(nodeProps(n), "label")
		id := ""
		if g.nodeIsReactive(n) || g.preview {
			id = g.nodeID(n)
		}
		g.writeOpenTag(b, "span", id, style, n, depth)
		fmt.Fprintf(b, "%s", html.EscapeString(label))
		b.WriteString("</span>\n")
	case "avatar":
		style := g.buildCSSStyle(n)
		style = htmlutil.AppendCSS(style, "display", "inline-flex")
		style = htmlutil.AppendCSS(style, "align-items", "center")
		style = htmlutil.AppendCSS(style, "justify-content", "center")
		style = htmlutil.AppendCSS(style, "border-radius", "50%")
		style = htmlutil.AppendCSS(style, "width", "40px")
		style = htmlutil.AppendCSS(style, "height", "40px")
		style = htmlutil.AppendCSS(style, "background", "#ccc")
		style = htmlutil.AppendCSS(style, "font-weight", "bold")
		initials := g.evalStaticString(nodeProps(n), "initials")
		if initials == "" {
			initials = g.evalStaticString(nodeProps(n), "alt")
		}
		id := ""
		if g.preview {
			id = g.allocID()
		}
		g.writeOpenTag(b, "div", id, style, n, depth)
		fmt.Fprintf(b, "%s</div>\n", html.EscapeString(initials))
	case "card":
		style := g.buildCSSStyle(n)
		variant := g.evalStaticString(nodeProps(n), "variant")
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
		if g.nodeIsReactive(n) || g.preview {
			id = g.nodeID(n)
		}
		g.writeOpenTag(b, "div", id, style, n, depth)
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
	style := g.buildCSSStyle(n)
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

	reactive := g.nodeIsReactive(n)
	id := ""
	if reactive {
		id = g.nodeID(n)
	}

	g.writeOpenTag(b, "div", id, style, n, depth)
	for _, s := range n.Children {
		g.renderIRStmt(b, s, depth+1)
	}
	fmt.Fprintf(b, "%s</div>\n", indent)
}

func (g *htmlGen) renderStaticText(b *strings.Builder, n *ir.NodeInst, depth int) {
	style := g.buildCSSStyle(n)
	val := g.evalStaticString(nodeProps(n), "value")
	reactive := codegen.IRIsReactive(codegen.NodeProp(n, "value"))
	id := ""
	if reactive {
		id = g.nodeID(n)
	}

	// In test mode, id/class need an element ID for updaters
	if g.testMode && id == "" && g.nodeHasUserAttrs(n) {
		id = g.nodeID(n)
	}

	g.writeOpenTag(b, "span", id, style, n, depth)
	b.WriteString(html.EscapeString(val))
	b.WriteString("</span>\n")

	if codegen.IRIsReactive(codegen.NodeProp(n, "value")) {
		g.addTextUpdater(id, codegen.NodeProp(n, "value"))
	}
	if g.testMode && id != "" && !strings.HasPrefix(id, "__n") {
		g.addUserAttrUpdaters(id, n)
	}
}

func (g *htmlGen) renderStaticButton(b *strings.Builder, n *ir.NodeInst, depth int) {
	style := g.buildCSSStyle(n)
	text := g.evalStaticString(nodeProps(n), "text")
	textExpr := codegen.NodeProp(n, "text")
	disabledExpr := codegen.NodeProp(n, "disabled")
	reactive := codegen.IRIsReactive(textExpr) || len(n.Handlers) > 0 || codegen.IRIsReactive(disabledExpr)
	id := ""
	if reactive {
		id = g.nodeID(n)
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
		id = g.nodeID(n)
	}
	indent := strings.Repeat("  ", depth)
	if id != "" {
		fmt.Fprintf(b, "%s<button id=%q", indent, id)
		if strings.HasPrefix(id, "__n") {
			fmt.Fprintf(b, " data-sngl-id=%q", id)
		}
	} else {
		fmt.Fprintf(b, "%s<button", indent)
	}
	if style != "" {
		fmt.Fprintf(b, " style=\"%s\"", style)
	}

	g.writeUserAttrs(b, id, n)
	b.WriteString(g.previewAttrs(n))
	fmt.Fprintf(b, "%s>%s</button>\n", disabled, html.EscapeString(text))

	if codegen.IRIsReactive(textExpr) {
		g.addTextContentUpdater(id, textExpr)
	}
	if codegen.IRIsReactive(disabledExpr) {
		g.addDisabledUpdater(id, disabledExpr)
	}

	// Event handlers
	if clickEvt := codegen.NodeHandler(n, "click"); clickEvt != nil && clickEvt.Func != nil && len(clickEvt.Func.Block) > 0 {
		g.addClickHandler(id, clickEvt.Func.Block)
	}
}

func (g *htmlGen) renderStaticInput(b *strings.Builder, n *ir.NodeInst, depth int) {
	style := g.buildCSSStyle(n)
	id := g.nodeID(n) // inputs are always reactive

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

	value := g.evalStaticString(nodeProps(n), "value")

	indent := strings.Repeat("  ", depth)
	fmt.Fprintf(b, "%s<input id=%q", indent, id)
	if strings.HasPrefix(id, "__n") {
		fmt.Fprintf(b, " data-sngl-id=%q", id)
	}
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

	g.writeUserAttrs(b, id, n)
	b.WriteString(g.previewAttrs(n))
	b.WriteString(" />\n")

	// Add value sync updater if the input is bound to state via an
	// assignment in its input handler.
	if inputEvt := codegen.NodeHandler(n, "input"); inputEvt != nil && inputEvt.Func != nil && len(inputEvt.Func.Block) > 0 {
		if target, ok := extractSetTarget(inputEvt.Func.Block[0]); ok && !strings.HasPrefix(id, "__n") {
			jsExpr := g.lang.TranslateIRExpr(target, g.scope)
			root := codegen.FindRootIdent(target)
			name := fmt.Sprintf("$u_%s_val", id[1:])
			g.updates = append(g.updates, updateFunc{
				funcName: name,
				body:     fmt.Sprintf("%s.value = %s;", id, jsExpr),
				deps:     map[string]bool{root: true},
			})
		}
	}

	// Input event handler
	if inputEvt := codegen.NodeHandler(n, "input"); inputEvt != nil && inputEvt.Func != nil && len(inputEvt.Func.Block) > 0 {
		g.addInputHandler(id, inputEvt.Func)
	}
}

func (g *htmlGen) renderStaticCheckbox(b *strings.Builder, n *ir.NodeInst, depth int) {
	style := g.buildCSSStyle(n)
	id := g.nodeID(n)

	checked := false
	if v := codegen.NodeProp(n, "checked"); v != nil {
		if bv, ok := codegen.IRLiteralBool(v); ok {
			checked = bv
		} else if codegen.IRIsReactive(v) {
			checked = true
		}
	}
	label := g.evalStaticString(nodeProps(n), "label")

	indent := strings.Repeat("  ", depth)
	fmt.Fprintf(b, "%s<label id=%q", indent, id)
	if strings.HasPrefix(id, "__n") {
		fmt.Fprintf(b, " data-sngl-id=%q", id)
	}
	if style != "" {
		fmt.Fprintf(b, " style=\"%s\"", style)
	}

	g.writeUserAttrs(b, id, n)
	b.WriteString(g.previewAttrs(n))
	b.WriteString(">")
	if checked {
		b.WriteString("<input type=\"checkbox\" checked />")
	} else {
		b.WriteString("<input type=\"checkbox\" />")
	}
	fmt.Fprintf(b, " %s</label>\n", html.EscapeString(label))

	if changeEvt := codegen.NodeHandler(n, "change"); changeEvt != nil && changeEvt.Func != nil && len(changeEvt.Func.Block) > 0 {
		g.addChangeHandler(id, changeEvt.Func)
	}
}

func (g *htmlGen) renderStaticImage(b *strings.Builder, n *ir.NodeInst, depth int) {
	style := g.buildCSSStyle(n)
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
		fmt.Fprintf(b, " id=%q", id)
	}
	if src != "" {
		fmt.Fprintf(b, " src=\"%s\"", html.EscapeString(src))
	}
	fmt.Fprintf(b, " alt=\"%s\"", html.EscapeString(alt))
	if style != "" {
		fmt.Fprintf(b, " style=\"%s\"", style)
	}

	g.writeUserAttrs(b, id, n)
	if g.preview {
		b.WriteString(g.previewAttrs(n))
	}
	b.WriteString(" />\n")
}

// --- New component renderers ---

func (g *htmlGen) renderStaticRadio(b *strings.Builder, n *ir.NodeInst, depth int) {
	style := g.buildCSSStyle(n)
	id := g.nodeID(n)
	indent := strings.Repeat("  ", depth)
	g.writeOpenTag(b, "fieldset", id, style, n, depth)
	// Static options rendered if literal
	if v := codegen.NodeProp(n, "options"); codegen.IRIsLiteral(v) {
		value := g.evalStaticString(nodeProps(n), "value")
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
	if evt := codegen.NodeHandler(n, "change"); evt != nil && evt.Func != nil && len(evt.Func.Block) > 0 {
		g.addChangeHandler(id, evt.Func)
	}
}

func (g *htmlGen) renderStaticToggle(b *strings.Builder, n *ir.NodeInst, depth int) {
	style := g.buildCSSStyle(n)
	id := g.nodeID(n)
	checked := false
	if v := codegen.NodeProp(n, "checked"); v != nil {
		if bv, ok := codegen.IRLiteralBool(v); ok {
			checked = bv
		} else if codegen.IRIsReactive(v) {
			checked = true
		}
	}
	label := g.evalStaticString(nodeProps(n), "label")
	indent := strings.Repeat("  ", depth)
	checkedAttr := ""
	if checked {
		checkedAttr = " checked"
	}
	fmt.Fprintf(b, "%s<label id=%q", indent, id)
	if strings.HasPrefix(id, "__n") {
		fmt.Fprintf(b, " data-sngl-id=%q", id)
	}
	if style != "" {
		fmt.Fprintf(b, " style=\"%s\"", style)
	}
	g.writeUserAttrs(b, id, n)
	b.WriteString(g.previewAttrs(n))
	fmt.Fprintf(b, "><input type=\"checkbox\" role=\"switch\"%s /> %s</label>\n", checkedAttr, html.EscapeString(label))
	if evt := codegen.NodeHandler(n, "change"); evt != nil && evt.Func != nil && len(evt.Func.Block) > 0 {
		g.addChangeHandler(id, evt.Func)
	}
}

func (g *htmlGen) renderStaticSelect(b *strings.Builder, n *ir.NodeInst, depth int) {
	style := g.buildCSSStyle(n)
	id := g.nodeID(n)
	indent := strings.Repeat("  ", depth)
	value := g.evalStaticString(nodeProps(n), "value")
	placeholder := g.evalStaticString(nodeProps(n), "placeholder")
	fmt.Fprintf(b, "%s<select id=%q", indent, id)
	if strings.HasPrefix(id, "__n") {
		fmt.Fprintf(b, " data-sngl-id=%q", id)
	}
	if style != "" {
		fmt.Fprintf(b, " style=\"%s\"", style)
	}
	g.writeUserAttrs(b, id, n)
	b.WriteString(g.previewAttrs(n))
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
	if evt := codegen.NodeHandler(n, "change"); evt != nil && evt.Func != nil && len(evt.Func.Block) > 0 {
		g.addChangeHandler(id, evt.Func)
	}
}

func (g *htmlGen) renderStaticTextarea(b *strings.Builder, n *ir.NodeInst, depth int) {
	style := g.buildCSSStyle(n)
	id := g.nodeID(n)
	indent := strings.Repeat("  ", depth)
	value := g.evalStaticString(nodeProps(n), "value")
	placeholder := g.evalStaticString(nodeProps(n), "placeholder")
	rows := "3"
	if v := codegen.NodeProp(n, "rows"); v != nil {
		if nv, ok := codegen.IRLiteralInt(v); ok {
			rows = fmt.Sprint(nv)
		}
	}
	fmt.Fprintf(b, "%s<textarea id=%q", indent, id)
	if strings.HasPrefix(id, "__n") {
		fmt.Fprintf(b, " data-sngl-id=%q", id)
	}
	if style != "" {
		fmt.Fprintf(b, " style=\"%s\"", style)
	}
	fmt.Fprintf(b, " rows=\"%s\"", rows)
	if placeholder != "" {
		fmt.Fprintf(b, " placeholder=\"%s\"", html.EscapeString(placeholder))
	}
	g.writeUserAttrs(b, id, n)
	b.WriteString(g.previewAttrs(n))
	fmt.Fprintf(b, ">%s</textarea>\n", html.EscapeString(value))
	if evt := codegen.NodeHandler(n, "input"); evt != nil && evt.Func != nil && len(evt.Func.Block) > 0 {
		g.addInputHandler(id, evt.Func)
	}
}

func (g *htmlGen) renderStaticTabs(b *strings.Builder, n *ir.NodeInst, depth int) {
	style := g.buildCSSStyle(n)
	id := g.nodeID(n)
	indent := strings.Repeat("  ", depth)
	g.writeOpenTag(b, "div", id, style, n, depth)
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
	style := g.buildCSSStyle(n)
	id := g.nodeID(n)
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
	title := g.evalStaticString(nodeProps(n), "title")
	overlayStyle := display + "position:fixed;top:0;left:0;right:0;bottom:0;background:rgba(0,0,0,0.5);display:flex;align-items:center;justify-content:center;z-index:1000"
	if !open {
		overlayStyle = "display:none"
	}
	fmt.Fprintf(b, "%s<div id=%q style=\"%s\"", indent, id, overlayStyle)
	if strings.HasPrefix(id, "__n") {
		fmt.Fprintf(b, " data-sngl-id=%q", id)
	}
	g.writeUserAttrs(b, id, n)
	b.WriteString(g.previewAttrs(n))
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
	style := g.buildCSSStyle(n)
	id := g.nodeID(n)
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
	g.writeOpenTag(b, tag, id, style, n, depth)
	for _, s := range n.Children {
		g.renderIRStmt(b, s, depth+1)
	}
	fmt.Fprintf(b, "%s</%s>\n", indent, tag)
	if codegen.IRIsReactive(codegen.NodeProp(n, "open")) {
		g.addIfUpdater(id, codegen.NodeProp(n, "open"))
	}
}

func (g *htmlGen) renderStaticTable(b *strings.Builder, n *ir.NodeInst, depth int) {
	style := g.buildCSSStyle(n)
	style = htmlutil.AppendCSS(style, "border-collapse", "collapse")
	style = htmlutil.AppendCSS(style, "width", "100%")
	id := ""
	if g.nodeIsReactive(n) || g.preview {
		id = g.nodeID(n)
	}
	indent := strings.Repeat("  ", depth)
	g.writeOpenTag(b, "table", id, style, n, depth)
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
	style := g.buildCSSStyle(n)
	id := ""
	if g.preview {
		id = g.allocID()
	}
	indent := strings.Repeat("  ", depth)
	style = htmlutil.AppendCSS(style, "list-style", "none")
	style = htmlutil.AppendCSS(style, "padding-left", "16px")
	g.writeOpenTag(b, "ul", id, style, n, depth)
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
	style := g.buildCSSStyle(n)
	id := g.nodeID(n)
	indent := strings.Repeat("  ", depth)
	value := g.evalStaticString(nodeProps(n), "value")
	placeholder := g.evalStaticString(nodeProps(n), "placeholder")
	fmt.Fprintf(b, "%s<input id=%q type=\"date\"", indent, id)
	if strings.HasPrefix(id, "__n") {
		fmt.Fprintf(b, " data-sngl-id=%q", id)
	}
	if style != "" {
		fmt.Fprintf(b, " style=\"%s\"", style)
	}
	if value != "" {
		fmt.Fprintf(b, " value=\"%s\"", html.EscapeString(value))
	}
	if placeholder != "" {
		fmt.Fprintf(b, " placeholder=\"%s\"", html.EscapeString(placeholder))
	}
	g.writeUserAttrs(b, id, n)
	b.WriteString(g.previewAttrs(n))
	b.WriteString(" />\n")
	if evt := codegen.NodeHandler(n, "change"); evt != nil && evt.Func != nil && len(evt.Func.Block) > 0 {
		g.addChangeHandler(id, evt.Func)
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
	if g.nodeIsReactive(n) || g.preview || g.testMode {
		id = g.nodeID(n)
	}
	style := g.buildCSSStyle(n)

	// Build inline attributes from static props.
	// innerText and innerHTML are rendered as element content, not attributes.
	props := nodeProps(n)
	var attrs strings.Builder
	staticInnerText := ""
	staticInnerHTML := ""
	for _, name := range slices.Sorted(maps.Keys(props)) {
		expr := props[name]
		// class is emitted separately below to avoid duplicate attrs.
		if name == "style" || name == "class" {
			continue
		}
		// Try resolving SNGL expressions (e.g. component param references)
		// to static values for the initial HTML render.
		if codegen.IRIsReactive(expr) {
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
			continue
		}
		// Literal / static.
		val := g.evalStaticString(props, name)
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
		id = g.nodeID(n)
	}
	fmt.Fprintf(b, "%s<%s", indent, tag)
	if id != "" {
		fmt.Fprintf(b, " id=%q", id)
		if strings.HasPrefix(id, "__n") {
			fmt.Fprintf(b, " data-sngl-id=%q", id)
		}
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
	g.writeUserAttrs(b, id, n)
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
		} else if preservesWhitespace(tag) {
			// For whitespace-sensitive tags (<pre>, <textarea>), render children
			// inline without inter-tag indentation. Otherwise the pretty-printer
			// injects visible newlines/spaces between the open tag and children.
			var sub strings.Builder
			for _, s := range n.Children {
				g.renderIRStmt(&sub, s, 0)
			}
			b.WriteString(stripInterTagWhitespace(sub.String()))
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

	// Reactive props: set via JS updaters.
	// For NoReactivity-assigned __nN ids, lowering already injected
	// `#__nN.<key> = expr` Assigns inside handler/timer/setter bodies, so
	// the updater is registered as initOnly: it runs once at initial sync
	// to populate the DOM from initial state, but is skipped by
	// findAffectedUpdaters so mutations don't double-fire.
	lowered := loweredID(id)
	for _, name := range slices.Sorted(maps.Keys(props)) {
		expr := props[name]
		if name == "style" {
			continue
		}
		if codegen.IRIsReactive(expr) {
			jsVal := g.exprToJS(expr)
			deps := g.exprDeps(expr)
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
				initOnly: lowered,
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
			g.addInputHandler(id, h.Func)
		case "change":
			g.addChangeHandler(id, h.Func)
		default:
			g.addClickHandler(id, h.Func.Block)
		}
	}
}

// findIRComponent resolves a component by name, supporting both bare
// ("Foo") and namespace-qualified ("pkg.Foo") forms. Backed entirely by the
// IR package's symbol table so imported-package components are reachable.
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
			jsVal = g.exprToJS(valueExpr)
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
				body := g.exprToJS(ret.Value)
				g.componentParams = append(g.componentParams, componentParam{
					name:  uniqueName,
					value: body,
				})
			}
		}
	}

	// State vars: promote to parent with unique names, and record their
	// initial values so emitScript can populate the state object.
	dataRenames := make(map[string]string)
	for _, dv := range comp.Vars {
		uniqueName := dv.Name + suffix
		g.scope.ModelFields[uniqueName] = true
		g.dt.ModelFields[uniqueName] = true
		g.scope.LocalVars[dv.Name] = true
		renames[dv.Name] = "state." + uniqueName
		dataRenames[dv.Name] = uniqueName
		g.inlinedStateInits = append(g.inlinedStateInits, componentParam{
			name:  uniqueName,
			value: g.literalToJS(dv.Init),
		})
	}
	g.scope.Renames = renames

	savedDataRenames := g.dataRenames
	g.dataRenames = dataRenames

	savedSlot := g.irSlotChildren
	g.irSlotChildren = n.Children

	// Collect timers declared by this component; translation uses the
	// current scope/renames so state references land on the unique names.
	for _, t := range comp.Timers {
		g.addIRTimer(t)
	}

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
	// Collect document-level timers (package + main component). Inlined
	// component timers were already appended during rendering.
	if g.pkg != nil {
		for _, t := range g.pkg.Timers {
			g.addIRTimer(t)
		}
		if main := mainIRComponent(g.pkg); main != nil {
			for _, t := range main.Timers {
				g.addIRTimer(t)
			}
		}
	}
	for i := range g.timers {
		g.timers[i].index = i
	}

	// Run IR optimizer on collected updaters, handlers, and timers.
	g.optimizeIR()

	// Extern bindings: surface impure native funcs through the shared
	// __sngl_externs object that WASM instantiation populates at load time.
	if len(g.wasmPkgs) > 0 {
		b.WriteString("// Extern bindings\nconst $ext = window.__sngl_externs || {};\n")
		for _, wp := range g.wasmPkgs {
			fmt.Fprintf(b, "const %s = {\n", wp.namespace)
			for _, f := range wp.funcs {
				fmt.Fprintf(b, "  %s: $ext.%s || function(){},\n", f.Name, f.Name)
			}
			b.WriteString("};\n")
		}
		b.WriteString("\n")
	}

	// State initialization. Vars whose initializer is a pure literal go
	// into the object literal directly; vars whose initializer references
	// other state (e.g. an i18n.tr call reading a sibling field) are
	// emitted as separate `state.X = ...;` statements after the object
	// literal, so they can read those siblings without hitting a
	// temporal-dead-zone reference.
	b.WriteString("// State\nlet state = {")
	var stateFields []string
	var deferredInits []struct{ name, value string }
	stateVars := g.stateVars()
	for _, dv := range stateVars {
		val := g.literalToJS(dv.Init)
		if codegen.IRIsLiteral(dv.Init) {
			stateFields = append(stateFields, dv.Name+": "+val)
		} else {
			// Seed the field with `null` so the object shape is correct
			// for any code that walks the keys before init completes.
			stateFields = append(stateFields, dv.Name+": null")
			deferredInits = append(deferredInits, struct{ name, value string }{dv.Name, val})
		}
	}
	for _, s := range g.inlinedStateInits {
		stateFields = append(stateFields, s.name+": null")
		deferredInits = append(deferredInits, struct{ name, value string }{s.name, s.value})
	}
	b.WriteString(strings.Join(stateFields, ", "))
	b.WriteString("};\n")
	for _, di := range deferredInits {
		fmt.Fprintf(b, "state.%s = %s;\n", di.name, di.value)
	}
	b.WriteString("\n")

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
				body := g.exprToJS(ret.Value)
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
		g.emitJSFunc(b, fn)
		emittedFuncs = true
	}
	if emittedFuncs {
		b.WriteString("\n")
	}

	// Async kickers from NoAsyncReactive lowering.
	if g.pkg != nil && len(g.pkg.AsyncKickers) > 0 {
		for _, k := range g.pkg.AsyncKickers {
			g.emitJSFunc(b, k.Func)
		}
		b.WriteString("\n")
	}

	// Setters — emit for fields with @change handlers, timer controls, async kicker deps, or preview mode
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
		if !needsSetter && g.pkg != nil {
			for _, k := range g.pkg.AsyncKickers {
				if slices.Contains(k.Deps, dv.Name) {
					needsSetter = true
					break
				}
			}
		}
		if !needsSetter {
			continue
		}
		g.emitSetter(b, dv)
	}
	if len(stateFields) > 0 {
		b.WriteString("\n")
	}

	b.WriteString("\n")

	// Constants
	consts := g.pkgConsts()
	for _, c := range consts {
		val := g.literalToJS(c.Init)
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

	// Element references. An id may appear in both the regular `refs`
	// set (collected from updater bodies) and the `loweredRefs` set
	// (lowered __nN ids reached via data-sngl-id). Prefer the
	// querySelector form for those — emitting both produces a duplicate
	// `const __nN` declaration that throws at parse time.
	refs := g.collectReferencedIDs()
	for _, id := range refs {
		if _, lowered := g.loweredRefs[id]; lowered {
			continue
		}
		fmt.Fprintf(b, "const %s = document.getElementById(\"%s\");\n", id, id)
	}
	loweredRefs := make([]string, 0, len(g.loweredRefs))
	for id := range g.loweredRefs {
		loweredRefs = append(loweredRefs, id)
	}
	sort.Strings(loweredRefs)
	for _, id := range loweredRefs {
		fmt.Fprintf(b, "const %s = document.querySelector('[data-sngl-id=%q]');\n", id, id)
	}
	if len(refs) > 0 || len(loweredRefs) > 0 {
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
	g.emitHandlers(b)

	// Timers
	g.emitTimers(b)

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

	// Async kicker startup: fire-and-forget each kicker once so initial values
	// are fetched. Runs after DOM updaters are wired so kicker bodies can call
	// setters and trigger updates.
	if g.pkg != nil && len(g.pkg.AsyncKickers) > 0 {
		b.WriteString("\n// Async kicker startup\n")
		for _, k := range g.pkg.AsyncKickers {
			fmt.Fprintf(b, "%s();\n", k.Func.Name)
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

func (g *htmlGen) emitHandlers(b *strings.Builder) {
	for _, h := range g.handlers {
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
		keyword := "function"
		if h.isAsync {
			keyword = "async function"
		}
		if h.event == "input" || h.event == "change" {
			fmt.Fprintf(b, "%s.addEventListener(\"%s\", %s(e) {\n  %s\n});\n", h.elemID, h.event, keyword, body)
		} else {
			fmt.Fprintf(b, "%s.addEventListener(\"%s\", %s() {\n  %s\n});\n", h.elemID, h.event, keyword, body)
		}
	}
}

func (g *htmlGen) emitTimers(b *strings.Builder) {
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
		tickKw := "function"
		if t.bodyAsync {
			tickKw = "async function"
		}
		fmt.Fprintf(b, "\nlet $timer_%d = null;\n", t.index)
		fmt.Fprintf(b, "%s $timer_%d_tick() {\n  %s\n}\n", tickKw, t.index, tickBody)
		fmt.Fprintf(b, "function $timer_%d_sync() {\n", t.index)
		fmt.Fprintf(b, "  if (state.%s && !$timer_%d) {\n", t.activeVar, t.index)
		fmt.Fprintf(b, "    $timer_%d = setInterval($timer_%d_tick, %d);\n", t.index, t.index, t.intervalMs)
		fmt.Fprintf(b, "  } else if (!state.%s && $timer_%d) {\n", t.activeVar, t.index)
		fmt.Fprintf(b, "    clearInterval($timer_%d);\n", t.index)
		fmt.Fprintf(b, "    $timer_%d = null;\n", t.index)
		b.WriteString("  }\n}\n")
	}
}

func (g *htmlGen) emitSetter(b *strings.Builder, dv *ir.Var) {
	setterAsync := false
	for _, h := range dv.Handlers {
		if h.Name == "change" && h.Func != nil && ir.BlockHasFuncvarAsyncCall(h.Func.Block, g.pts()) {
			setterAsync = true
			break
		}
	}
	keyword := "function"
	if setterAsync {
		keyword = "async function"
	}
	fmt.Fprintf(b, "%s $set_%s(v) {\n", keyword, dv.Name)
	fmt.Fprintf(b, "  state.%s = v;\n", dv.Name)
	mutated := map[string]bool{dv.Name: true}
	for _, u := range g.findAffectedUpdaters(mutated) {
		fmt.Fprintf(b, "  %s();\n", u.funcName)
	}
	for _, h := range dv.Handlers {
		if h.Name == "change" && h.Func != nil {
			for _, s := range h.Func.Block {
				for _, js := range g.translateHandlerStmt(s, g.scope) {
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
	// Fire-and-forget any async kickers that depend on this state var.
	if g.pkg != nil {
		for _, k := range g.pkg.AsyncKickers {
			if slices.Contains(k.Deps, dv.Name) {
				fmt.Fprintf(b, "  %s();\n", k.Func.Name)
			}
		}
	}
	if g.preview {
		b.WriteString("  __sngl_sync_state();\n")
	}
	b.WriteString("}\n")
}

func (g *htmlGen) collectReferencedIDs() []string {
	seen := make(map[string]bool)
	var ids []string
	addID := func(id string) {
		if id == "" || seen[id] {
			return
		}
		seen[id] = true
		ids = append(ids, id)
	}

	for _, u := range g.updates {
		// Extract the owning element ID from the func name: $u_N_xxx → $N.
		// Lowered ids ($u__nN_xxx) reference data-sngl-id'd elements; mark
		// them so the loweredRefs emitter declares the const for them.
		parts := strings.SplitN(u.funcName, "_", 3)
		if len(parts) >= 2 && parts[1] != "" && allDigits(parts[1]) {
			addID("$" + parts[1])
		} else if len(parts) >= 3 && parts[1] == "" && strings.HasPrefix(parts[2], "n") {
			// $u__nN_text → __nN
			rest := parts[2]
			tail := strings.IndexByte(rest, '_')
			if tail < 0 {
				tail = len(rest)
			}
			suffix := rest[1:tail]
			if allDigits(suffix) {
				g.loweredRefs["__n"+suffix] = true
			}
		}
		// Also scan the body — some updaters (for-else, if-else) manipulate
		// a *different* element than the one encoded in their name.
		for _, id := range extractElemIDs(u.body) {
			addID(id)
		}
	}
	for _, h := range g.handlers {
		addID(h.elemID)
	}

	sort.Strings(ids)
	return ids
}

func allDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// extractElemIDs scans a JS snippet for `$N` identifiers (the generated IDs
// used for DOM element handles) and returns each unique match.
func extractElemIDs(js string) []string {
	var out []string
	seen := make(map[string]bool)
	for i := 0; i < len(js); i++ {
		if js[i] != '$' {
			continue
		}
		// Only match $ followed by a digit (our allocID format is $<int>).
		j := i + 1
		if j >= len(js) || js[j] < '0' || js[j] > '9' {
			continue
		}
		// $ must not be preceded by an identifier character (avoid matching
		// inside longer names like $u_0_list).
		if i > 0 {
			p := js[i-1]
			if (p >= 'a' && p <= 'z') || (p >= 'A' && p <= 'Z') || (p >= '0' && p <= '9') || p == '_' || p == '$' {
				continue
			}
		}
		for j < len(js) && js[j] >= '0' && js[j] <= '9' {
			j++
		}
		id := js[i:j]
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
		i = j - 1
	}
	return out
}

// optimizeIR builds a MutationModel from the collected updaters, handlers,
// and timers, runs the IR optimizer, then writes the results back.
func (g *htmlGen) optimizeIR() {
	// Convert platform types → IR types.
	updaters := make([]codegen.Updater, len(g.updates))
	for i, u := range g.updates {
		updaters[i] = codegen.Updater{
			Name:     u.funcName,
			Body:     u.body,
			Deps:     u.deps,
			InitOnly: u.initOnly,
		}
	}
	// Keep handler body strings keyed by elemID+event (IR uses ast.Node,
	// but HTML has already translated to JS strings).
	handlerBodyMap := make(map[string]string)
	handlerAsyncMap := make(map[string]bool)
	handlers := make([]codegen.Handler, len(g.handlers))
	for i, h := range g.handlers {
		key := h.elemID + ":" + h.event
		handlerBodyMap[key] = h.body
		handlerAsyncMap[key] = h.isAsync
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
			initOnly: u.InitOnly,
		}
	}

	// Write back optimized handlers, restoring body strings.
	g.handlers = make([]eventHandler, len(m.Handlers))
	for i, h := range m.Handlers {
		key := h.NodeID + ":" + h.Event
		g.handlers[i] = eventHandler{
			elemID:  h.NodeID,
			event:   h.Event,
			body:    handlerBodyMap[key],
			mutated: h.Mutated,
			isAsync: handlerAsyncMap[key],
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
		if u.initOnly {
			continue
		}
		for dep := range u.deps {
			if expanded[dep] {
				result = append(result, u)
				break
			}
		}
	}
	return result
}

// loweredID reports whether id was assigned by NoReactivity lowering. Such
// ids already have inline mutation updates injected into handler/timer/setter
// bodies; updaters registered for them are init-only.
func loweredID(id string) bool { return strings.HasPrefix(id, "__n") }

// addTextUpdater adds an updater that sets el.textContent from an expression.
func (g *htmlGen) addTextUpdater(elemID string, expr ir.Expr) {
	jsExpr := g.lang.TranslateIRExpr(expr, g.scope)
	deps := g.exprDeps(expr)
	name := fmt.Sprintf("$u_%s_text", elemID[1:])
	g.updates = append(g.updates, updateFunc{
		funcName: name,
		body:     fmt.Sprintf("%s.textContent = %s;", elemID, jsExpr),
		deps:     deps,
		initOnly: loweredID(elemID),
	})
}

func (g *htmlGen) addTextContentUpdater(elemID string, expr ir.Expr) {
	jsExpr := g.lang.TranslateIRExpr(expr, g.scope)
	deps := g.exprDeps(expr)
	name := fmt.Sprintf("$u_%s_text", elemID[1:])
	g.updates = append(g.updates, updateFunc{
		funcName: name,
		body:     fmt.Sprintf("%s.textContent = %s;", elemID, jsExpr),
		deps:     deps,
		initOnly: loweredID(elemID),
	})
}

func (g *htmlGen) addAttrUpdater(elemID, attr string, expr ir.Expr) {
	jsExpr := g.lang.TranslateIRExpr(expr, g.scope)
	deps := g.exprDeps(expr)
	name := fmt.Sprintf("$u_%s_%s", elemID[1:], attr)
	g.updates = append(g.updates, updateFunc{
		funcName: name,
		body:     fmt.Sprintf("%s.setAttribute(%q, %s);", elemID, attr, jsExpr),
		deps:     deps,
		initOnly: loweredID(elemID),
	})
}

func (g *htmlGen) addDisabledUpdater(elemID string, expr ir.Expr) {
	jsExpr := g.lang.TranslateIRExpr(expr, g.scope)
	deps := g.exprDeps(expr)
	name := fmt.Sprintf("$u_%s_disabled", elemID[1:])
	g.updates = append(g.updates, updateFunc{
		funcName: name,
		body:     fmt.Sprintf("%s.disabled = %s;", elemID, jsExpr),
		deps:     deps,
		initOnly: loweredID(elemID),
	})
}

func (g *htmlGen) addIfUpdater(elemID string, expr ir.Expr) {
	jsExpr := g.lang.TranslateIRExpr(expr, g.scope)
	deps := g.exprDeps(expr)
	name := fmt.Sprintf("$u_%s_if", elemID[1:])
	g.updates = append(g.updates, updateFunc{
		funcName: name,
		body:     fmt.Sprintf("%s.style.display = %s ? \"\" : \"none\";", elemID, jsExpr),
		deps:     deps,
		initOnly: loweredID(elemID),
	})
}

// addElseUpdater adds a display updater for the else branch of an if statement.
func (g *htmlGen) addElseUpdater(elemID string, cond ir.Expr) {
	jsExpr := g.lang.TranslateIRExpr(cond, g.scope)
	deps := g.exprDeps(cond)
	name := fmt.Sprintf("$u_%s_else", elemID[1:])
	g.updates = append(g.updates, updateFunc{
		funcName: name,
		body:     fmt.Sprintf("%s.style.display = %s ? \"none\" : \"\";", elemID, jsExpr),
		deps:     deps,
	})
}

// addForStmtUpdater adds a list updater for an ir.For statement.
func (g *htmlGen) addForStmtUpdater(elemID string, stmt *ir.For) {
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
func (g *htmlGen) addForElseStmtUpdater(forElemID, elseElemID string, stmt *ir.For) {
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

// emitForStmtBody generates the inner HTML creation code for an ir.For body.
func (g *htmlGen) emitForStmtBody(b *strings.Builder, stmt *ir.For, iterVar, indexVar, containerID string) {
	savedLocals := make(map[string]bool)
	maps.Copy(savedLocals, g.scope.LocalVars)
	g.scope.LocalVars[iterVar] = true
	g.scope.LocalVars[indexVar] = true
	defer func() { g.scope.LocalVars = savedLocals }()

	for _, s := range stmt.Body {
		if n, ok := s.(*ir.NodeInst); ok {
			g.emitForLoopBody(b, n, iterVar, indexVar, containerID)
		}
	}
}

func (g *htmlGen) emitForLoopBody(b *strings.Builder, n *ir.NodeInst, iterVar, indexVar, containerID string) {
	savedLocals := make(map[string]bool)
	maps.Copy(savedLocals, g.scope.LocalVars)
	g.scope.LocalVars[iterVar] = true
	g.scope.LocalVars[indexVar] = true
	defer func() { g.scope.LocalVars = savedLocals }()

	listFuncName := fmt.Sprintf("$u_%s_list", containerID[1:])

	switch n.Name {
	case "checkbox":
		checked := "false"
		if v := codegen.NodeProp(n, "checked"); v != nil {
			checked = g.exprToJS(v)
		}
		label := `""`
		if v := codegen.NodeProp(n, "label"); v != nil {
			label = g.exprToJS(v)
		}
		style := g.buildCSSStyle(n)

		fmt.Fprintf(b, "    const row = document.createElement(\"label\");\n")
		style = htmlutil.AppendCSS(style, "display", "block")
		fmt.Fprintf(b, "    row.style.cssText = %q;\n", style)
		fmt.Fprintf(b, "    const cb = document.createElement(\"input\");\n")
		fmt.Fprintf(b, "    cb.type = \"checkbox\";\n")
		fmt.Fprintf(b, "    cb.checked = %s;\n", checked)

		if changeEvt := codegen.NodeHandler(n, "change"); changeEvt != nil && changeEvt.Func != nil && len(changeEvt.Func.Block) > 0 {
			first := changeEvt.Func.Block[0]
			stmts := g.translateHandlerStmt(first, g.scope)
			mutated := codegen.MutatedFields(first)
			var handlerLines []string
			for _, s := range stmts {
				handlerLines = append(handlerLines, s+";")
			}
			handlerLines = append(handlerLines, listFuncName+"();")
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

		fmt.Fprintf(b, "    row.appendChild(cb);\n")
		fmt.Fprintf(b, "    row.appendChild(document.createTextNode(\" \" + %s));\n", label)
		fmt.Fprintf(b, "    %s.appendChild(row);\n", containerID)

	case "text":
		tag := "span"
		fmt.Fprintf(b, "    const el = document.createElement(%q);\n", tag)
		val := `""`
		if v := codegen.NodeProp(n, "value"); v != nil {
			val = g.exprToJS(v)
		}
		fmt.Fprintf(b, "    el.textContent = %s;\n", val)
		fmt.Fprintf(b, "    %s.appendChild(el);\n", containerID)

	default:
		fmt.Fprintf(b, "    const el = document.createElement(\"div\");\n")
		fmt.Fprintf(b, "    el.textContent = String(%s);\n", iterVar)
		fmt.Fprintf(b, "    %s.appendChild(el);\n", containerID)
	}
}

// exprDeps extracts model field dependencies, remapping through dataRenames
// when inside a component scope so deps use promoted field names.
func (g *htmlGen) exprDeps(expr ir.Expr) map[string]bool {
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

// domWriteFor maps a (SNGL component name, prop key) pair to the JS
// expression that writes that prop on the rendered DOM element.
// el is the JS expression yielding the element (e.g.
// document.querySelector('[data-sngl-id="__n0"]') or a cached const);
// value is the JS expression to write. Returns the full statement
// without trailing semicolon.
//
// Only props that NoReactivity may inject as #__nN.<key> Assigns are
// listed. Unknown (componentName, key) pairs fall through to a generic
// property assignment el.<key> = value, matching JS lang's default —
// surface a TODO if a real test produces a wrong write so the table
// can be extended.
func domWriteFor(componentName, key, el, value string) string {
	switch componentName {
	case "text", "badge":
		if key == "value" {
			return fmt.Sprintf("%s.textContent = %s", el, value)
		}
	case "button":
		if key == "text" {
			return fmt.Sprintf("%s.textContent = %s", el, value)
		}
		if key == "disabled" {
			return fmt.Sprintf("%s.disabled = %s", el, value)
		}
	case "input":
		if key == "value" {
			return fmt.Sprintf("%s.value = %s", el, value)
		}
		if key == "disabled" {
			return fmt.Sprintf("%s.disabled = %s", el, value)
		}
	case "progress":
		if key == "value" {
			return fmt.Sprintf("%s.setAttribute(\"value\", %s)", el, value)
		}
	case "checkbox", "toggle":
		if key == "checked" {
			// The __n* id is on the wrapping <label>, not the inner <input>;
			// descend to the input to actually flip the checked state.
			return fmt.Sprintf("%s.querySelector('input').checked = %s", el, value)
		}
	case "modal", "drawer", "popover", "menu":
		if key == "open" {
			// Reactive open toggles visibility. Match the legacy
			// addIfUpdater body shape so test-mode and styling stay
			// consistent.
			return fmt.Sprintf("%s.style.display = (%s) ? \"\" : \"none\"", el, value)
		}
	}
	return fmt.Sprintf("%s.%s = %s", el, key, value)
}

// translateHandlerStmt translates one handler-block IR stmt to a JS
// snippet (no trailing semicolon). Reactive-update Assigns of the form
// *ir.Assign{Target: *ir.Select{Operand: *ir.Ident{IsElementRef:true}, Field:F}}
// are routed through domWriteFor using g.idToNode[id] to choose the
// DOM-correct write. Everything else falls through to
// lang.TranslateIRMutation with the provided scope.
func (g *htmlGen) translateHandlerStmt(s ir.Stmt, scope *codegen.ExprScope) []string {
	a, ok := s.(*ir.Assign)
	if !ok {
		return g.lang.TranslateIRMutation(s, scope)
	}
	sel, ok := a.Target.(*ir.Select)
	if !ok {
		return g.lang.TranslateIRMutation(s, scope)
	}
	idn, ok := sel.Operand.(*ir.Ident)
	if !ok || !idn.IsElementRef {
		return g.lang.TranslateIRMutation(s, scope)
	}
	node, ok := g.idToNode[idn.Name]
	if !ok || node == nil {
		// Pre-assigned __n* id but renderer never seeded the map — fall
		// through to JS default (querySelector + .field = …) and rely
		// on the generic write to be correct enough.
		return g.lang.TranslateIRMutation(s, scope)
	}
	g.loweredRefs[idn.Name] = true
	el := idn.Name
	value := g.lang.TranslateIRExpr(a.Value, scope)
	return []string{domWriteFor(node.Name, sel.Field, el, value)}
}

func (g *htmlGen) addClickHandler(elemID string, body []ir.Stmt) {
	if len(body) == 0 {
		return
	}
	var stmts []string
	var mutated map[string]bool
	for _, s := range body {
		stmts = append(stmts, g.translateHandlerStmt(s, g.scope)...)
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
		isAsync: ir.BlockHasFuncvarAsyncCall(body, g.pts()),
	})
}

func (g *htmlGen) addInputHandler(elemID string, fn *ir.Func) {
	if fn == nil || len(fn.Block) == 0 {
		return
	}
	savedEvent := g.scope.EventVar
	g.scope.EventVar = "e.target"
	if g.scope.Renames == nil {
		g.scope.Renames = make(map[string]string)
	}
	var savedLocal []string
	var savedRename []struct {
		name string
		val  string
		had  bool
	}
	for _, p := range fn.Params {
		if p == nil || p.Name == "" {
			continue
		}
		if !g.scope.LocalVars[p.Name] {
			savedLocal = append(savedLocal, p.Name)
			g.scope.LocalVars[p.Name] = true
		}
		prev, had := g.scope.Renames[p.Name]
		savedRename = append(savedRename, struct {
			name string
			val  string
			had  bool
		}{p.Name, prev, had})
		g.scope.Renames[p.Name] = "e.target"
	}
	var stmts []string
	mutated := make(map[string]bool)
	for _, s := range fn.Block {
		stmts = append(stmts, g.translateHandlerStmt(s, g.scope)...)
		maps.Copy(mutated, codegen.MutatedFields(s))
	}
	g.scope.EventVar = savedEvent
	for _, n := range savedLocal {
		delete(g.scope.LocalVars, n)
	}
	for _, r := range savedRename {
		if r.had {
			g.scope.Renames[r.name] = r.val
		} else {
			delete(g.scope.Renames, r.name)
		}
	}
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
		isAsync: ir.BlockHasFuncvarAsyncCall(fn.Block, g.pts()),
	})
}

func (g *htmlGen) addChangeHandler(elemID string, fn *ir.Func) {
	if fn == nil || len(fn.Block) == 0 {
		return
	}
	// Rename any declared param (typically `e`) so references like
	// `e.value` translate to `e.target.value` in the emitted JS — same
	// shape addInputHandler uses for input events.
	savedEvent := g.scope.EventVar
	g.scope.EventVar = "e.target"
	if g.scope.Renames == nil {
		g.scope.Renames = make(map[string]string)
	}
	var savedLocal []string
	var savedRename []struct {
		name string
		val  string
		had  bool
	}
	for _, p := range fn.Params {
		if p == nil || p.Name == "" {
			continue
		}
		if !g.scope.LocalVars[p.Name] {
			savedLocal = append(savedLocal, p.Name)
			g.scope.LocalVars[p.Name] = true
		}
		prev, had := g.scope.Renames[p.Name]
		savedRename = append(savedRename, struct {
			name string
			val  string
			had  bool
		}{p.Name, prev, had})
		g.scope.Renames[p.Name] = "e.target"
	}
	var stmts []string
	mutated := make(map[string]bool)
	for _, s := range fn.Block {
		stmts = append(stmts, g.translateHandlerStmt(s, g.scope)...)
		maps.Copy(mutated, codegen.MutatedFields(s))
	}
	g.scope.EventVar = savedEvent
	for _, n := range savedLocal {
		delete(g.scope.LocalVars, n)
	}
	for _, r := range savedRename {
		if r.had {
			g.scope.Renames[r.name] = r.val
		} else {
			delete(g.scope.Renames, r.name)
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
		isAsync: ir.BlockHasFuncvarAsyncCall(fn.Block, g.pts()),
	})
}

// addIRTimer collects an IR timer into g.timers, translating the handler
// body through the current scope so state references resolve to the unique
// renamed field names.
func (g *htmlGen) addIRTimer(t *ir.Timer) {
	if t == nil || t.Handler == nil {
		return
	}
	var stmts []string
	mutated := make(map[string]bool)
	for _, s := range t.Handler.Block {
		stmts = append(stmts, g.translateHandlerStmt(s, g.scope)...)
		maps.Copy(mutated, codegen.MutatedFields(s))
	}
	mutated = g.remapMutated(mutated, g.dataRenames)
	var lines []string
	for _, s := range stmts {
		lines = append(lines, s)
	}
	activeVar := ""
	if id, ok := t.Enabled.(*ir.Ident); ok {
		if renamed, ok := g.dataRenames[id.Name]; ok {
			activeVar = renamed
		} else {
			activeVar = id.Name
		}
	}
	g.timers = append(g.timers, timerDef{
		intervalMs: codegen.IntervalToMs(t.Interval),
		activeVar:  activeVar,
		body:       strings.Join(lines, "\n  "),
		mutated:    mutated,
		bodyAsync:  ir.BlockHasFuncvarAsyncCall(t.Handler.Block, g.pts()),
	})
}

// CSS building

func (g *htmlGen) buildCSSStyle(n *ir.NodeInst) string {
	if n == nil {
		return ""
	}
	return htmlutil.BuildCSSStyleIR(n.Props)
}

// Expression evaluation helpers

func (g *htmlGen) emitJSFunc(b *strings.Builder, fn *ir.Func) {
	params := make([]string, len(fn.Params))
	for i, p := range fn.Params {
		params[i] = p.Name
	}
	paramStr := strings.Join(params, ", ")

	funcScope := &codegen.ExprScope{
		ModelFields:    g.scope.ModelFields,
		ComputedFields: g.scope.ComputedFields,
		FuncNames:      g.scope.FuncNames,
		LocalVars:      make(map[string]bool),
		Pkg:            g.scope.Pkg,
	}
	for k := range g.scope.LocalVars {
		funcScope.LocalVars[k] = true
	}
	for _, p := range fn.Params {
		funcScope.LocalVars[p.Name] = true
	}

	// Mangle dotted names for JS: int.sqrt → int_sqrt
	jsName := strings.ReplaceAll(fn.Name, ".", "_")

	keyword := "function"
	if fn.IsAsync {
		keyword = "async function"
	}

	// Single-return expression body.
	if len(fn.Block) == 1 {
		if ret, ok := fn.Block[0].(*ir.Return); ok && ret.Value != nil {
			body := g.lang.TranslateIRExpr(ret.Value, funcScope)
			fmt.Fprintf(b, "%s %s(%s) { return %s; }\n", keyword, jsName, paramStr, body)
			return
		}
	}
	if len(fn.Block) == 0 {
		return
	}
	fmt.Fprintf(b, "%s %s(%s) {\n", keyword, jsName, paramStr)
	for _, stmt := range fn.Block {
		switch s := stmt.(type) {
		case *ir.LocalVar:
			funcScope.LocalVars[s.Name] = true
			val := g.lang.TranslateIRExpr(s.Init, funcScope)
			fmt.Fprintf(b, "  let %s = %s;\n", s.Name, val)
		case *ir.Return:
			if s.Value != nil {
				ret := g.lang.TranslateIRExpr(s.Value, funcScope)
				fmt.Fprintf(b, "  return %s;\n", ret)
			} else {
				b.WriteString("  return;\n")
			}
		default:
			stmts := g.translateHandlerStmt(stmt, funcScope)
			for _, line := range stmts {
				fmt.Fprintf(b, "  %s;\n", line)
			}
		}
	}
	b.WriteString("}\n")
}

func (g *htmlGen) exprToJS(expr ir.Expr) string {
	if codegen.IRIsReactive(expr) {
		return g.lang.TranslateIRExpr(expr, g.scope)
	}
	if codegen.IRIsLiteral(expr) {
		return g.lang.TranslateIRLiteral(expr)
	}
	return `""`
}

func (g *htmlGen) literalToJS(expr ir.Expr) string {
	if expr == nil {
		return `""`
	}
	if lit, ok := expr.(*ir.Literal); ok {
		if lit.Type != nil {
			switch lit.Type.Kind {
			case ir.TypeString:
				if s, ok := codegen.IRLiteralString(expr); ok {
					return fmt.Sprintf("%q", s)
				}
			case ir.TypeInt, ir.TypeFloat, ir.TypeBool:
				return lit.Raw
			case ir.TypeNull:
				return "null"
			case ir.TypeUnit:
				return fmt.Sprintf("%q", lit.Raw)
			case ir.TypeColor,
				ir.TypeDate, ir.TypeTime, ir.TypeDateTime, ir.TypeDuration,
				ir.TypeURL, ir.TypeEmail, ir.TypeUUID, ir.TypeRegex, ir.TypeBase64,
				ir.TypeIPV4, ir.TypeIPV6, ir.TypeHostname, ir.TypeDecimal:
				return fmt.Sprintf("%q", lit.Raw)
			}
		}
		return lit.Raw
	}
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

func (g *htmlGen) evalStaticString(props map[string]ir.Expr, key string) string {
	if props == nil {
		return ""
	}
	v, ok := props[key]
	if !ok {
		return ""
	}
	if codegen.IRIsLiteral(v) {
		if s, ok := codegen.IRLiteralString(v); ok {
			return s
		}
		return fmt.Sprintf("%v", exprLiteralAnyIR(v))
	}
	// For SNGL expressions, evaluate with initial state
	if codegen.IRIsReactive(v) {
		return g.evalInitialString(v)
	}
	return ""
}

func (g *htmlGen) evalInitialString(expr ir.Expr) string {
	if !codegen.IRIsReactive(expr) {
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

// extractSetTarget finds the target expression of an assignment statement.
func extractSetTarget(s ir.Stmt) (ir.Expr, bool) {
	if s == nil {
		return nil, false
	}
	if n, ok := s.(*ir.Assign); ok {
		return n.Target, true
	}
	return nil, false
}

func (g *htmlGen) writeOpenTag(b *strings.Builder, tag, id, style string, n *ir.NodeInst, depth int) {
	if g.preview && id == "" {
		id = g.allocID()
	}
	indent := strings.Repeat("  ", depth)
	fmt.Fprintf(b, "%s<%s", indent, tag)
	if id != "" {
		fmt.Fprintf(b, " id=%q", id)
		if strings.HasPrefix(id, "__n") {
			fmt.Fprintf(b, " data-sngl-id=%q", id)
		}
	}
	if style != "" {
		fmt.Fprintf(b, " style=\"%s\"", style)
	}
	if n != nil {
		g.writeUserAttrs(b, id, n)
	}
	pos := nodePos(n)
	if g.preview && pos.IsValid() {
		fmt.Fprintf(b, " data-sngl-line=\"%d\" data-sngl-col=\"%d\"", pos.Line, pos.Column)
	}
	b.WriteString(">\n")
}

// nodeHasUserAttrs returns true if a NodeInst has a user-specified #id or
// a static class prop.
func (g *htmlGen) nodeHasUserAttrs(n *ir.NodeInst) bool {
	return n.ID != "" || codegen.NodeProp(n, "class") != nil
}

// addUserAttrUpdaters adds DOM updaters for a user-specified class prop.
func (g *htmlGen) addUserAttrUpdaters(elemID string, n *ir.NodeInst) {
	classExpr := codegen.NodeProp(n, "class")
	if classExpr == nil {
		return
	}
	jsExpr := g.exprToJS(classExpr)
	deps := g.exprDeps(classExpr)
	name := fmt.Sprintf("$u_%s_cls", elemID[1:])
	g.updates = append(g.updates, updateFunc{
		funcName: name,
		body:     fmt.Sprintf("%s.className = %s;", elemID, jsExpr),
		deps:     deps,
	})
}

// writeUserAttrs emits user-specified id and class attributes in test mode.
func (g *htmlGen) writeUserAttrs(b *strings.Builder, internalID string, n *ir.NodeInst) {
	if !g.testMode {
		return
	}
	classExpr := codegen.NodeProp(n, "class")
	if codegen.IRIsLiteral(classExpr) {
		if s, ok := codegen.IRLiteralString(classExpr); ok {
			fmt.Fprintf(b, " class=%q", s)
		}
	}
	if n.ID != "" && !strings.HasPrefix(n.ID, "__n") {
		fmt.Fprintf(b, " data-sngl-id=%q", n.ID)
	}
}

// previewAttrs returns the preview-mode position data attributes for a
// node (`data-sngl-line`/`data-sngl-col`), or "" outside preview mode.
func (g *htmlGen) previewAttrs(n *ir.NodeInst) string {
	if !g.preview {
		return ""
	}
	pos := nodePos(n)
	if !pos.IsValid() {
		return ""
	}
	return fmt.Sprintf(" data-sngl-line=\"%d\" data-sngl-col=\"%d\"", pos.Line, pos.Column)
}
