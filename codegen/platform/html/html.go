package html

import (
	"context"
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
	"git.duckfam.us/jonathan/sngl/codegen/canvasutil"
	snglI18n "git.duckfam.us/jonathan/sngl/codegen/i18n"
	"git.duckfam.us/jonathan/sngl/codegen/lang/javascript"
	"git.duckfam.us/jonathan/sngl/internal/asset"
	"git.duckfam.us/jonathan/sngl/internal/htmlutil"
	"git.duckfam.us/jonathan/sngl/internal/lower"
	"git.duckfam.us/jonathan/sngl/ir"
)

func init() {
	codegen.RegisterPlatform(&Generator{})
}

// Generator implements codegen.PlatformGenerator for HTML output.
type Generator struct{}

func (g *Generator) PlatformIdentifier() string { return "html" }
func (g *Generator) Description() string {
	return "Web output. Static site by default, or a language-driven HTTP server when paired with a language that implements HTTPCompiler."
}

func (g *Generator) Capabilities(lang codegen.LangTranslator) lower.Features {
	f := lang.Capabilities()
	// Both modes write the tree as markup: static mode writes a file, route
	// mode writes the same markup into a handler, and the only loop either can
	// emit is a hole over an expression that varies with the request.
	f.ViewStatements = false
	// A platform has the last word, and html's output is HTML and JS: the
	// language emits the server half of route mode, not the markup or the
	// script. So a restriction that exists because the *language* lacks a
	// construct does not apply to what html itself renders -- Go has no
	// ternary and asks for NoTernary, which rewrote `class=cond ? "a" : ""`
	// into a temporary the render model could not see through, for an
	// expression the JS that fills the hole writes verbatim.
	//
	// ListLambdas is the same: Go withdraws it for a lambda behind an
	// interface surface, but the emitter writes an inline one directly (a
	// typed IIFE over the slice), so a `.filter` in a server action compiles
	// either way -- and lowering it built the same kind of temporary.
	f.Ternary = true
	f.ListLambdas = true
	f.AsyncReactive = false
	f.ImplicitRecv = false
	f.InlineComponents = false
	f.Reactivity = false
	f.StructSpread = false
	f.StructComponents = true
	f.StdlibContextParam = true
	f.Canvas = true
	f.ReactiveCanvas = true
	return f
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

func (g *Generator) Generate(req *codegen.Request, sink codegen.Sink) error {
	// Agent mode suppresses the user's main() entry so the testagent's drives
	// the page.
	agentMode := codegen.OptionBool(req.Options, "test") && codegen.OptionString(req.Options, "testMode") == "agent"
	if agentMode {
		if req.Options == nil {
			req.Options = &ir.StructLit{}
		}
		codegen.SetOptionField(req.Options, "main", false)
	}

	if req.Pkg != nil {
		if err := validateRawElements(req.Pkg); err != nil {
			return err
		}
	}

	if err := checkPlacementDirectives(req.Pkg); err != nil {
		return err
	}

	if req.Lang.LanguageIdentifier() == "none" {
		if err := rejectDynamicHrefs(req); err != nil {
			return err
		}
		// Static mode has no server to host the route's POST handler.
		if win, ok := backendHandlerWindow(req.Pkg, codegen.NewCodegenCtx(req, "html").Windows()); ok {
			return fmt.Errorf("html: window %q has a server-side handler (calls a non-js: import) but the build target %q has no server — compile with a server language (e.g. --lang go) or wrap the call in html.frontend(...)", win, req.Lang.LanguageIdentifier())
		}
		c := &compilation{}
		m, err := c.BuildMutationModel(req, codegen.AnalyzeCommon(req.Pkg))
		if err != nil {
			return err
		}
		if agentMode {
			injectTestagentBootstrap(c)
		}
		if err := c.EmitFromMutation(m, req, sink); err != nil {
			return err
		}
		if agentMode {
			modelType := "main"
			if main := codegen.NewCodegenCtx(req, "html").MainComponent(); main != nil {
				modelType = main.Name
			}
			if err := emitTestagentFiles(sink, req.Pkg, modelType); err != nil {
				return err
			}
		}
		return nil
	}
	if _, ok := req.Lang.(codegen.HTTPCompiler); ok {
		return g.generateRoutes(req, sink)
	}
	return fmt.Errorf("html: unsupported lang %q", req.Lang.LanguageIdentifier())
}

// injectTestagentBootstrap appends the testagent module script to every
// assembled window. Agent mode only.
func injectTestagentBootstrap(c *compilation) {
	const bootstrap = `<script type="module">
import { main } from './testagent/testagent.js';
import './testagent_main.js';
import './snapshot.js';
import './current_model.js';
document.addEventListener('DOMContentLoaded', () => main());
</script>
`
	for i, w := range c.windows {
		src := string(w.bytes)
		idx := strings.LastIndex(src, "</body>")
		if idx < 0 {
			src = src + "\n" + bootstrap
		} else {
			src = src[:idx] + bootstrap + src[idx:]
		}
		c.windows[i].bytes = []byte(src)
	}
}

// rejectDynamicHrefs errors in static mode: {param} routes need a server.
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

type compilation struct {
	assetFiles []htmlAssetFile
	windows    []htmlWindowOutput
}

// windowShared caches what a window's codegen recomputes but that does not
// vary by window. projectDir and projectFS live here because everything cached
// below is a function of them, so no cache key has to carry them.
type windowShared struct {
	projectDir string
	// projectFS is the FS the js: virtual root is mounted on; it is the OS
	// filesystem for the CLI and an in-memory one for the playground.
	projectFS fs.FS

	bundles      map[bundleKey]bundleResult
	i18nManifest string
	i18nLoaded   bool
	// Both describe the package, which no window changes.
	usedComponents map[string]bool
	prewalked      map[string]*ir.NodeInst
	slotsRewritten bool
}

func newWindowShared(projectDir string, projectFS fs.FS) *windowShared {
	return &windowShared{
		projectDir: projectDir,
		projectFS:  projectFS,
		bundles:    map[bundleKey]bundleResult{},
	}
}

// bundleKey is every input bundleNativeScript takes that windowShared does not
// already fix.
type bundleKey struct {
	entry  string
	minify bool
	maps   bool
	outDir string
}

type bundleResult struct {
	out string
	err error
}

type htmlAssetFile struct {
	name  string
	bytes []byte
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

func (c *compilation) BuildMutationModel(req *codegen.Request, analysis *codegen.CommonAnalysis) (*codegen.MutationModel, error) {
	jsLang := codegen.LookupLang("js")
	if jsLang == nil {
		return nil, fmt.Errorf("html: js translator not registered")
	}

	var opts htmlConfig
	if err := codegen.ApplyOptions(&opts, req.Options); err != nil {
		return nil, fmt.Errorf("html: %w", err)
	}

	// The optimizer already chose the OutPath and may have stashed the bytes;
	// fall back to a fresh read when absent.
	for _, fa := range req.FileAssets {
		data := fa.Data
		if data == nil {
			d, err := os.ReadFile(fa.SrcPath)
			if err != nil {
				continue
			}
			data = d
		}
		c.assetFiles = append(c.assetFiles, htmlAssetFile{name: fa.OutPath, bytes: data})
	}

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
			c.assetFiles = append(c.assetFiles, htmlAssetFile{name: outName, bytes: data})
		}
	}

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
					c.assetFiles = append(c.assetFiles, htmlAssetFile{name: outPath, bytes: wasmExecData})
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
			c.assetFiles = append(c.assetFiles, htmlAssetFile{name: wasmFile, bytes: wasmBytes})
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
	shared := newWindowShared(projectDir, projectFS)

	ctx := codegen.NewCodegenCtx(req, "html")

	// A package with no main component and no windows still emits an empty
	// index.html, so callers can verify codegen succeeded.
	irWindows := ctx.Windows()
	if len(irWindows) == 0 {
		gen := newHTMLGenFromCtx(ctx, jsLang, opts, shared)
		gen.wasmLoader = wasmLoaderHTML
		gen.wasmPkgs = wasmPkgs
		gen.stylesheet = stylesheetURL
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
	staticMode := req.Lang.LanguageIdentifier() == "none"
	var mainStmts []ir.Stmt
	seenPaths := map[string]ast.Pos{}
	for i, win := range irWindows {
		var name string
		switch {
		case !staticMode:
			// In route mode the language compiler indexes by WindowIdx and
			// ignores file paths, and dynamic /{param} routes are expected.
			name = fmt.Sprintf("window_%d", i)
		case win.Window == nil || win.Window.Href == nil:
			// No declaration to take an href from: the package body's root
			// window, or a lone main component's. It is the document the site
			// opens at, whether or not others sit beside it.
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
		gen := newHTMLGenFromCtx(ctx, jsLang, opts, shared)
		gen.wasmLoader = wasmLoaderHTML
		gen.wasmPkgs = wasmPkgs
		gen.stylesheet = stylesheetURL
		gen.irBodyStmts = win.Body
		gen.irWindowFuncs = win.Funcs
		gen.irWindow = win.Window
		if win.Window != nil {
			gen.ctx = gen.ctx.ForWindow(win.Window)
		}
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

func (c *compilation) EmitFromMutation(_ *codegen.MutationModel, req *codegen.Request, sink codegen.Sink) error {
	for _, f := range c.assetFiles {
		if err := writeSinkFile(sink, f.name, f.bytes); err != nil {
			return err
		}
	}
	for _, w := range c.windows {
		if err := writeSinkFile(sink, w.name, w.bytes); err != nil {
			return err
		}
	}
	return nil
}

func writeSinkFile(sink codegen.Sink, name string, content []byte) error {
	w, err := sink.Create(name)
	if err != nil {
		return err
	}
	if _, err := w.Write(content); err != nil {
		w.Close()
		return err
	}
	return w.Close()
}

// htmlGen holds all state for generating a single HTML file.
type htmlGen struct {
	lang codegen.LangTranslator

	pkg *ir.Package
	ctx *codegen.ExprCtx

	// The Analysis is shared across windows; the Emission is per-window.
	*codegen.CommonAnalysis
	*codegen.Emission
	dt *codegen.DepTracker

	nextID int

	initWrites []updateFunc

	handlers []eventHandler

	timers []timerDef

	componentParams []componentParam

	inlinedStateInits []componentParam

	title string

	stylesheet string

	favicon string

	wasmLoader string

	wasmPkgs []wasmPackage

	// shared is never nil: newHTMLGen allocates one when the caller has no
	// compilation to share.
	shared *windowShared

	preview bool

	testMode bool

	minify bool

	maps bool

	// outDir is what the inline source map's `sources` resolve against. It
	// only makes them a good label: a window with `href="/about"` lands a
	// directory lower, and that name is decided after the map is rendered.
	// The map's sourcesContent is what actually carries the source.
	outDir string

	componentInvocations int

	// dataRenames maps original component var names to promoted unique names
	// so handler MutatedFields can be remapped after inlining.
	dataRenames map[string]string

	// currentComp resolves implicit `this` for exprDeps / MutatedFields.
	currentComp *ir.Component

	// rootComp is the component whose body this document renders. It is the
	// one named "main" for an ordinary build and the component under test for
	// a test build, which is a distinction only CodegenCtx.MainComponent
	// makes: html used to answer it by name in eight places, so a test of a
	// component not called "main" collected no state and rendered no binding.
	rootComp *ir.Component

	componentDepth int

	usesI18n bool

	// irSlotChildren holds the caller's children during component inlining,
	// for the body-level `slot` pseudo-element to project into position.
	irSlotChildren []ir.Stmt

	irBodyStmts []ir.Stmt

	// irWindowFuncs holds synthesized funcs lowerCanvas placed on the window
	// IR node rather than on the package or main component.
	irWindowFuncs []*ir.Func

	// irWindow is the window this generator emits a document for, or nil when
	// it is emitting a main component's body. A window is the third place
	// state is declared, beside the package and the main component, and it is
	// per-document: static mode emits one file per window.
	irWindow *ir.Window

	// snglIDByElem maps a JS element variable ($1) to the id a program wrote
	// on that node (#inc). Only ids a test could name are in it.
	snglIDByElem map[string]string

	// idToNode maps each emitted element id back to its NodeInst, which is
	// all a reactive-update Assign inside a handler body has to go on.
	idToNode map[string]*ir.NodeInst

	// elemDecl is the declaration this window's raw elements resolved to,
	// taken from the tree during prewalkNodes. It answers for the nodes the
	// lowering creates after that walk, which carry no id it could key on.
	elemDecl *ir.Component

	// loweredRefs are the __n* ids a reactive-update Assign targets. Each gets
	// one top-level `const __nN = document.querySelector(...)` in emitScript,
	// so handlers emit a bare identifier rather than a querySelector per write.
	loweredRefs map[string]bool

	canvasSetups []canvasSetup
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
	// requires is carried through OptimizeMutation so a dropped updater
	// contributes nothing to the final helper/import set.
	requires codegen.Requirement
}

type eventHandler struct {
	elemID string
	event  string // the DOM event name passed to addEventListener
	body   string // JS statements
	// hasParam follows the handler's declared parameter, not the event name,
	// because that is what translation binds `e` from. A declared parameter
	// the body never reads still sets it.
	hasParam bool
	mutated  map[string]bool
	isAsync  bool
}

type timerDef struct {
	index      int
	intervalMs int
	activeVar  string
	body       string
	mutated    map[string]bool
	bodyAsync  bool
}

// newHTMLGen builds the generator for one window. A caller with no compilation
// to share passes a nil shared and gets one of its own, so there is only ever
// one code path below.
func newHTMLGen(pkg *ir.Package, lang codegen.LangTranslator, opts htmlConfig, shared *windowShared) *htmlGen {
	if shared == nil {
		shared = newWindowShared("", nil)
	}
	var ao codegen.AnalyzeOpts
	ao.UsedComponents = shared.usedComponents
	common := codegen.AnalyzeCommonFor(pkg, ao)
	if shared.usedComponents == nil {
		shared.usedComponents = maps.Clone(common.UsedComponents)
	}

	g := &htmlGen{
		pkg:            pkg,
		lang:           lang,
		CommonAnalysis: common,
		Emission:       codegen.NewEmission(),
		preview:        opts.Preview,
		testMode:       opts.Test,
		minify:         opts.Minify,
		idToNode:       make(map[string]*ir.NodeInst),
		loweredRefs:    make(map[string]bool),
		usesI18n:       hasI18nCalls(pkg),
		shared:         shared,
	}

	g.dt = codegen.NewDepTrackerFromPkg(pkg)
	g.rootComp = mainIRComponent(pkg)
	g.currentComp = g.rootComp
	g.ctx = codegen.NewExprCtx(pkg)
	g.ctx.Platform = "html"
	if pkg != nil {
		for _, c := range pkg.Consts {
			g.ctx.Locals[c.Name] = true
		}
	}
	if g.ctx.NativeImports == nil {
		g.ctx.NativeImports = map[string]map[string]bool{}
	}
	// The JsIRContext path writes jc.Ctx.Helpers and the emit check reads
	// g.ctx.Helpers; one map makes a flag from either path reach the check.
	g.ctx.Helpers = g.Helpers

	return g
}

func newHTMLGenFromCtx(ctx *codegen.CodegenCtx, lang codegen.LangTranslator, opts htmlConfig, shared *windowShared) *htmlGen {
	g := newHTMLGen(ctx.Pkg, lang, opts, shared)
	g.maps = ctx.ExprCtx.Maps
	g.outDir = ctx.ExprCtx.OutDir
	// CodegenCtx is the one that knows about RootComponent, so its answer wins
	// over the by-name lookup newHTMLGen had to fall back on.
	g.rootComp = ctx.MainComponent()
	g.currentComp = g.rootComp
	if main := ctx.MainComponent(); main != nil {
		g.irBodyStmts = main.Body
		// ForComponent re-clones, replacing newHTMLGen's wiring with
		// ctx.ExprCtx's own maps, so capture it first.
		helpers := g.ctx.Helpers
		native := g.ctx.NativeImports
		g.ctx = ctx.ExprCtx.ForComponent(main)
		g.ctx.Helpers = helpers
		g.ctx.NativeImports = native
	}
	return g
}

// prewalkShared seeds idToNode from the cached package walk. The __n* ids are
// the same for every window; the $N ids allocated while rendering are not, so
// each window still gets its own map to add them to.
func (g *htmlGen) prewalkShared() {
	if g.shared.prewalked == nil {
		g.prewalkNodes()
		g.shared.prewalked = maps.Clone(g.idToNode)
		return
	}
	maps.Copy(g.idToNode, g.shared.prewalked)
}

// rewriteSlotCallsOnce runs the slot retarget for the first window only: the
// rewrite is a package-wide IR edit.
func (g *htmlGen) rewriteSlotCallsOnce() {
	if !g.shared.slotsRewritten {
		g.rewriteSlotCallsToAnchors()
		g.shared.slotsRewritten = true
	}
}

// bundleScript runs the window's script through esbuild, memoized on the
// script text: every page of a static site shares the same runtime.
func (g *htmlGen) bundleScript(entry string) (string, error) {
	key := bundleKey{entry: entry, minify: g.minify, maps: g.maps, outDir: g.outDir}
	if got, ok := g.shared.bundles[key]; ok {
		return got.out, got.err
	}
	out, err := bundleNativeScript(entry, g.shared.projectFS, g.minify, g.maps, g.outDir)
	g.shared.bundles[key] = bundleResult{out: out, err: err}
	return out, err
}

func (g *htmlGen) i18nManifest() string {
	if !g.shared.i18nLoaded {
		g.shared.i18nManifest = i18nManifestJS(g.shared.projectDir, g.shared.projectFS)
		g.shared.i18nLoaded = true
	}
	return g.shared.i18nManifest
}

func (g *htmlGen) allocID() string {
	id := fmt.Sprintf("$%d", g.nextID)
	g.nextID++
	return id
}

// prewalkNodes seeds g.idToNode with every NodeInst carrying an `__n*` id from
// the NoReactivity lowering. See generate().
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
			// A node the lowering creates later — a `for` body's — gets its
			// id then, so it never reaches idToNode. Every raw element of a
			// package shares one declaration, so keeping the one seen here
			// answers for those too.
			if g.elemDecl == nil && isElement(n.Component) {
				g.elemDecl = n.Component
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
		case *ir.ContextProvider:
			visitStmts(n.Children)
		case *ir.Assign, *ir.CallStmt, *ir.LocalVar, *ir.Return, *ir.Emit, *ir.Toggle, *ir.CanvasRedrawStmt,
			*ir.Break, *ir.Continue:
		default:
			panic(fmt.Sprintf("html.collectNodeIDs: unhandled ir.Stmt %T", n))
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

func slotIndexFromRenderFunc(name string) string {
	if !strings.HasPrefix(name, "__renderSlot") {
		return ""
	}
	return strings.TrimPrefix(name, "__renderSlot")
}

func slotAnchorVar(n string) string {
	return "__slotAnchor_" + n
}

// rewriteSlotCallsToAnchors retargets every `__renderSlotN(parentRef)` call to
// the slot's own `display:contents` anchor. passReactivity threads `parent`
// into both the AppendChild and RemoveChild calls, so binding it to the anchor
// puts create and teardown in the right DOM position and drops the dependence
// on the unbound `__root` sentinel.
//
// Runs before the static body walk so init calls, handler/timer re-fires and
// the placeholder-position emission all observe the rewrite.
func (g *htmlGen) rewriteSlotCallsToAnchors() {
	if g.pkg == nil {
		return
	}
	var visit func(s ir.Stmt)
	visitStmts := func(stmts []ir.Stmt) {
		for _, s := range stmts {
			visit(s)
		}
	}
	rewriteCall := func(call *ir.Call) {
		if call == nil || call.Func == nil {
			return
		}
		n := slotIndexFromRenderFunc(call.Func.Name)
		if n == "" || len(call.Args) != 1 {
			return
		}
		call.Args[0].Value = &ir.Ident{
			Name:         slotAnchorVar(n),
			Type:         ir.TypDyn,
			IsElementRef: true,
			Synthesized:  true,
		}
	}
	visit = func(s ir.Stmt) {
		switch n := s.(type) {
		case *ir.CallStmt:
			rewriteCall(n.Call)
			// An inline-closure handler carries slot re-fire calls in its body.
			if n.Call != nil {
				for _, a := range n.Call.Args {
					switch lam := a.Value.(type) {
					case *ir.Lambda:
						if lam.Func != nil {
							visitStmts(lam.Func.Block)
						}
					case *ir.Closure:
						if lam.Func != nil {
							visitStmts(lam.Func.Block)
						}
					}
				}
			}
		case *ir.NodeInst:
			if n == nil {
				return
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
		case *ir.ContextProvider:
			visitStmts(n.Children)
		default:
		}
	}
	// Every block where the reactivity pass may have spliced a __renderSlotN
	// re-fire — mirrors lowerReactivity's pass-2 coverage. Timer handlers
	// included: a missed one removeChilds from the wrong node.
	visitHandlers := func(vars []*ir.Var) {
		for _, v := range vars {
			for _, h := range v.Handlers {
				if h.Func != nil {
					visitStmts(h.Func.Block)
				}
			}
		}
	}
	visitTimers := func(timers []*ir.Timer) {
		for _, t := range timers {
			if t != nil && t.Handler != nil {
				visitStmts(t.Handler.Block)
			}
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
		visitTimers(c.Timers)
		visitHandlers(c.Vars)
	}
	for _, w := range g.pkg.Windows {
		if w == nil {
			continue
		}
		visitStmts(w.Body)
		for _, fn := range w.Funcs {
			if fn != nil {
				visitStmts(fn.Block)
			}
		}
		visitHandlers(w.Vars)
		if w.ErrorHandler != nil && w.ErrorHandler.Func != nil {
			visitStmts(w.ErrorHandler.Func.Block)
		}
	}
	for _, fn := range g.pkg.Funcs {
		if fn != nil {
			visitStmts(fn.Block)
		}
	}
	visitHandlers(g.pkg.Vars)
}

// nodeID returns n.ID when NoReactivity pre-assigned one, otherwise allocates
// a fresh `$N`, recording it in g.idToNode. A nil n allocates without
// recording, so that id falls through to the JS-default write.
func (g *htmlGen) nodeID(n *ir.NodeInst) string {
	var id string
	if n != nil && strings.HasPrefix(n.ID, "__n") {
		id = n.ID
	} else {
		id = g.allocID()
	}
	if n != nil {
		g.idToNode[id] = n
		g.noteSnglID(id, n)
	}
	return id
}

func (g *htmlGen) generate() (string, error) {
	// idToNode must be complete before any handler translates a reactive-update
	// Assign: a handler emitted before its destination text node would fall
	// through to `el.value = …`, wrong for a span.
	g.prewalkShared()

	// Must precede the body walk so the handler/timer re-fire calls collected
	// there are rewritten too.
	g.rewriteSlotCallsOnce()

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
		defaultCSS := "* { margin: 0; padding: 0; box-sizing: border-box; }\n" +
			"body { font-family: system-ui, sans-serif; }\n"
		// A root element asking for a share of its parent has no parent to
		// take it from: `body` is not a flex container and neither it nor
		// `html` has a height, so `flex: 1` on the outermost div did nothing
		// and the page sat at its content height. The share it is asking for
		// is the viewport, which is the reading Compose's fillMaxSize() gets
		// from the same source.
		if g.rootFlexes() {
			defaultCSS += "html, body { height: 100%; }\n" +
				"body { display: flex; flex-direction: column; }\n"
		}
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

	for _, s := range g.irBodyStmts {
		g.renderIRStmt(&b, s, 0)
	}

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

	var scriptBuf strings.Builder
	g.emitScript(&scriptBuf)
	script := scriptBuf.String()

	// The i18n runtime is prepended as an IIFE so the generated calls resolve
	// without forcing the main script through a separate esbuild pass.
	if g.usesI18n && strings.TrimSpace(script) != "" {
		snippet, err := i18nRuntimeSnippet()
		if err != nil {
			return "", fmt.Errorf("i18n runtime bundle: %w", err)
		}
		var preamble strings.Builder
		// Manifest init runs before getTranslator() is first called.
		if manifestJS := g.i18nManifest(); manifestJS != "" {
			preamble.WriteString(manifestJS)
		}
		preamble.WriteString(snippet)
		preamble.WriteString("\n")
		script = preamble.String() + script
	}

	if strings.TrimSpace(script) != "" {
		// Convert the JS translator's `/*@SNGL:file:line@*/` markers to an
		// inline sourceMappingURL now, so esbuild chains through to a
		// SNGL→bundled output map.
		if g.maps {
			script = inlineSourceMapFromMarkers(script, g.outDir)
		}
		// Always run esbuild, so output is consistent whether or not the
		// source uses native imports. Its IIFE wrapping deterministically
		// renames shadowed parameters, which is harmless: param names are
		// scope-local.
		var preludeBuf strings.Builder
		if len(g.ctx.NativeImports) > 0 {
			mods := make([]string, 0, len(g.ctx.NativeImports))
			for m := range g.ctx.NativeImports {
				mods = append(mods, m)
			}
			sort.Strings(mods)
			for _, m := range mods {
				fmt.Fprintf(&preludeBuf, "import * as %s from %q;\n", codegen.NativeAlias(m), m)
			}
			preludeBuf.WriteString("\n")
		}
		bundled, err := g.bundleScript(preludeBuf.String() + script)
		if err != nil {
			return "", err
		}
		script = bundled
		b.WriteString("\n<script>\n")
		b.WriteString(script)
		b.WriteString("</script>\n")
	}
	b.WriteString("</body></html>\n")

	result := b.String()
	refSet := map[string]bool{}
	for _, id := range g.collectReferencedIDs() {
		refSet[id] = true
	}
	return stripUnreferencedIDs(result, refSet, g.nextID), nil
}

// stripUnreferencedIDs removes every ` id="$N"` attribute allocID handed out
// that no handler or updater refers to, in one scan of the document (a
// strings.Replace per allocated id is quadratic in the page).
//
// Only an id allocID could have issued is a candidate — `$4` on a page that
// allocated three is left alone, as is any spelling it cannot produce (`$00`).
// Anything else in an ` id="` belongs to whatever emitted it.
func stripUnreferencedIDs(doc string, referenced map[string]bool, nextID int) string {
	const attr = ` id="$`
	if !strings.Contains(doc, attr) {
		return doc
	}
	var out strings.Builder
	out.Grow(len(doc))
	dropped := map[string]bool{}
	rest := doc
	for {
		i := strings.Index(rest, attr)
		if i < 0 {
			out.WriteString(rest)
			break
		}
		head := i + len(attr)
		end := strings.IndexByte(rest[head:], '"')
		if end < 0 || !isAllocatedID(rest[head:head+end], nextID) {
			out.WriteString(rest[:head])
			rest = rest[head:]
			continue
		}
		id := "$" + rest[head:head+end]
		out.WriteString(rest[:i])
		if referenced[id] || dropped[id] {
			out.WriteString(rest[i : head+end+1])
		} else {
			dropped[id] = true
		}
		rest = rest[head+end+1:]
	}
	return out.String()
}

func isAllocatedID(digits string, nextID int) bool {
	if digits == "" || len(digits) > 1 && digits[0] == '0' {
		return false
	}
	n := 0
	for i := 0; i < len(digits); i++ {
		c := digits[i]
		if c < '0' || c > '9' {
			return false
		}
		n = n*10 + int(c-'0')
		if n >= nextID {
			return false
		}
	}
	return true
}

// renderIRStmt is the IR-driven top-level dispatch. *ir.If and *ir.For never
// reach it in production: const branches are eliminated by internal/optimize
// and runtime ones are rewired into __renderSlot by passReactivity.
func (g *htmlGen) renderIRStmt(b *strings.Builder, s ir.Stmt, depth int) {
	switch n := s.(type) {
	case *ir.NodeInst:
		g.renderIRNode(b, n, depth)
	case *ir.SlotInst:
		for _, child := range g.irSlotChildren {
			g.renderIRStmt(b, child, depth)
		}
	case *ir.CallStmt:
		// A __renderSlotN call marks a reactive if/for slot position; its
		// content is appended into this anchor at runtime.
		if n.Call != nil && n.Call.Func != nil {
			if idx := slotIndexFromRenderFunc(n.Call.Func.Name); idx != "" {
				fmt.Fprintf(b, "%s<span data-sngl-slot=\"%s\" style=\"display:contents\"></span>\n",
					strings.Repeat("  ", depth), idx)
				return
			}
		}
		if syn := nodeFromIRCallStmt(n); syn != nil {
			g.renderIRNode(b, syn, depth)
		}
	case *ir.ErrorBoundary:
		for _, child := range n.Children {
			g.renderIRStmt(b, child, depth)
		}
	case *ir.If:
		// A recursive component can still surface a literal-cond If here.
		for _, child := range n.Body {
			g.renderIRStmt(b, child, depth)
		}
		for _, child := range n.Else {
			g.renderIRStmt(b, child, depth)
		}
	case *ir.For:
		for _, child := range n.Body {
			g.renderIRStmt(b, child, depth)
		}
	case *ir.Window:
		panic(fmt.Sprintf("html.renderIRStmt: unexpected nested Window: %#v", n))
	case *ir.ContextProvider:
		// passNoContext eliminates these before codegen.
		panic(fmt.Sprintf("html.renderIRStmt: unexpected ContextProvider: %#v", n))
	case *ir.Assign, *ir.LocalVar, *ir.Return, *ir.Emit, *ir.Toggle, *ir.CanvasRedrawStmt,
		*ir.Break, *ir.Continue:
	default:
		panic(fmt.Sprintf("html.renderIRStmt: unhandled ir.Stmt %T", n))
	}
}

// nodeFromIRCallStmt promotes an ir.CallStmt that targets a component into a
// synthetic ir.NodeInst. Returns nil for plain function calls.
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

// irCallName extracts the bare or namespace-qualified name from an IR Call.
// A namespace-resolved element call leaves Call.Func nil and Call.Receiver set
// to the namespace ident, so "ns.Foo" is reassembled from the AST back-ref.
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

// renderIRNode dispatches a NodeInst to the appropriate renderer: a user
// component inlines via its IR body, a stdlib component goes through its
// AST-backed helper, and a raw element is rendered IR-native.
func (g *htmlGen) renderIRNode(b *strings.Builder, n *ir.NodeInst, depth int) {
	if isUserIRComponent(n) {
		g.renderIRUserComponent(b, n, depth)
		return
	}
	switch n.Name {
	case "slot":
		for _, s := range g.irSlotChildren {
			g.renderIRStmt(b, s, depth)
		}
		return
	case "window":
		return
	case "timer":
		return
	}
	// A bodyless call promoted from ir.CallStmt carries no Component back-ref.
	if n.Component == nil {
		if comp := g.findIRComponent(n.Name); comp != nil && comp.AST != nil {
			n.Component = comp
			g.renderIRUserComponent(b, n, depth)
			return
		}
	}
	g.renderRawElementIR(b, n, depth)
}

// isUserIRComponent reports whether a NodeInst resolves to a user-defined
// component rather than a stdlib component or a raw element.
func isUserIRComponent(n *ir.NodeInst) bool {
	if n.Component == nil || n.Component.AST == nil {
		return false
	}
	// `element` answers to every tag name, so the node's name is a tag rather
	// than a reference to it; inlining its empty body renders nothing at all.
	if isElement(n.Component) {
		return false
	}
	if n.Component.Stdlib {
		return false
	}
	return true
}

// pts returns the funcvar points-to analysis results, or nil if not yet
// computed. Used with ir.BlockHasFuncvarAsyncCall.
func (g *htmlGen) pts() *ir.PointsToInfo {
	if g.pkg == nil {
		return nil
	}
	return g.pkg.PointsTo
}

// stateVars returns pkg.Vars merged with the main component's Vars.
// Synthesized vars are excluded; emitScript emits them as top-level `let`.
// stateVars is the state in scope for the document this generator emits: the
// package's, the root component's, and this window's. The other components'
// are not -- html emits one document per window and inlines the rest into it,
// so a child component's var reaches `state` through the inliner's rename, not
// through this list.
//
// Synthesized vars are excluded; emitScript emits them as top-level `let`.
func (g *htmlGen) stateVars() []*ir.Var {
	var out []*ir.Var
	for _, o := range ir.Owners(g.pkg) {
		if o.Comp != nil && o.Comp != g.rootComp {
			continue
		}
		if o.Win != nil && o.Win != g.irWindow {
			continue
		}
		for _, v := range o.Vars {
			if !v.Synthesized {
				out = append(out, v)
			}
		}
	}
	return out
}

// synthesizedVars returns the Synthesized vars of the package and main
// component, deduplicated by name: the context lowering pass injects a var
// like __ctx_locale into both pkg.Vars and component.Vars.
func (g *htmlGen) synthesizedVars() []*ir.Var {
	var out []*ir.Var
	seen := make(map[string]bool)
	if g.pkg != nil {
		for _, v := range g.pkg.Vars {
			if v.Synthesized && !seen[v.Name] {
				seen[v.Name] = true
				out = append(out, v)
			}
		}
		if main := g.rootComp; main != nil {
			for _, v := range main.Vars {
				if v.Synthesized && !seen[v.Name] {
					seen[v.Name] = true
					out = append(out, v)
				}
			}
		}
	}
	return out
}

// synthesizedFuncs returns the Synthesized funcs of the package, main
// component and current window.
func (g *htmlGen) synthesizedFuncs() []*ir.Func {
	var out []*ir.Func
	if g.pkg != nil {
		for _, f := range g.pkg.Funcs {
			if f.Synthesized {
				out = append(out, f)
			}
		}
		// Every component the build renders, not only the root: one that
		// survived inlining is emitted from its own declaration, and its
		// synthesized funcs -- a canvas draw function among them -- have to
		// come with it.
		for _, comp := range g.pkg.Components {
			for _, f := range comp.Funcs {
				if f.Synthesized {
					out = append(out, f)
				}
			}
		}
	}
	for _, f := range g.irWindowFuncs {
		if f.Synthesized {
			out = append(out, f)
		}
	}
	return out
}

// rootFlexes reports whether a top-level node of the page asked for a share of
// its parent. Only the roots: a flex inside the tree is answered by the box
// around it, and only the outermost one needs the viewport handed to it.
func (g *htmlGen) rootFlexes() bool {
	for _, st := range g.irBodyStmts {
		n, ok := st.(*ir.NodeInst)
		if !ok {
			continue
		}
		for _, sf := range codegen.NodeStyleFields(n) {
			if sf.Name != "flex" {
				continue
			}
			if v, ok := codegen.IRLiteralString(sf.Value); ok && (v == "" || v == "0") {
				continue
			}
			return true
		}
	}
	return false
}

func (g *htmlGen) pkgStructs() []*ir.StructDef {
	if g.pkg == nil {
		return nil
	}
	return g.pkg.Structs
}

// pkgFuncs returns user-defined top-level funcs plus main component funcs.
// The dedupe matters: after passNoInlineComponents + registerNestedMethods a
// component method lands in both pkg.Funcs and comp.Funcs. Synthesized funcs
// are excluded; emitScript routes those through WalkLowered separately.
func (g *htmlGen) pkgFuncs() []*ir.Func {
	if g.pkg == nil {
		return nil
	}
	seen := make(map[*ir.Func]struct{})
	var out []*ir.Func
	add := func(f *ir.Func) {
		if f.Synthesized {
			return
		}
		if _, dup := seen[f]; dup {
			return
		}
		seen[f] = struct{}{}
		out = append(out, f)
	}
	for _, f := range g.pkg.Funcs {
		add(f)
	}
	if main := g.rootComp; main != nil {
		for _, f := range main.Funcs {
			add(f)
		}
	}
	return out
}

func (g *htmlGen) pkgConsts() []*ir.Var {
	if g.pkg == nil {
		return nil
	}
	out := append([]*ir.Var{}, g.pkg.Consts...)
	if main := g.rootComp; main != nil {
		for _, v := range main.Vars {
			if v.IsConst {
				out = append(out, v)
			}
		}
	}
	return out
}

func mainIRComponent(pkg *ir.Package) *ir.Component {
	for _, c := range pkg.Components {
		if c.Name == "main" {
			return c
		}
	}
	return nil
}

// preservesWhitespace reports whether a raw HTML tag renders whitespace in its
// content literally, so the pretty-printer must not indent inside it.
func preservesWhitespace(tag string) bool {
	switch tag {
	case "pre", "textarea":
		return true
	}
	return false
}

// stripInterTagWhitespace collapses whitespace between adjacent tags, so a
// <pre>'s children do not carry the pretty-printer's indentation into the
// output. Whitespace inside text nodes is preserved.
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

const maxComponentDepth = 10

// renderRawElementIR renders an IR NodeInst as a raw HTML element, tagged by
// the local part of the component name. A synthesized AST node is built for
// the helpers that still accept ast.VisualNode.
func (g *htmlGen) renderRawElementIR(b *strings.Builder, n *ir.NodeInst, depth int) {
	indent := strings.Repeat("  ", depth)
	decl := g.elementDecl(n)
	tag := n.Name
	if _, local, ok := strings.Cut(tag, "."); ok {
		tag = local
	}
	// The tag the element resolved to: `element` binds the matched name to
	// its `tag` prop, and a call site that wrote one of its own — the way a
	// hyphenated custom element is reached — replaced it there. n.Name is the
	// fallback for a node that resolved to no component at all.
	if t, ok := rawElementTag(n); ok {
		tag = t
	}

	id := ""
	if g.nodeIsReactive(n) || g.preview || g.testMode {
		id = g.nodeID(n)
	}
	if n.CanvasDraw != nil && id == "" {
		id = g.nodeID(n)
		if g.idToNode != nil {
			g.idToNode[id] = n
		}
	}
	if n.CanvasDraw != nil {
		cw, ch := canvasIntProp(n, "width"), canvasIntProp(n, "height")
		cs := canvasSetup{id: id, drawFunc: n.CanvasDraw, w: cw, h: ch, scaling: canvasScalingMode(n)}
		g.canvasSetups = append(g.canvasSetups, cs)
		// Init-only: reactive redraws come from the CanvasRedrawStmt
		// passCanvasReactivity injects into handler/timer bodies. A scaled
		// canvas also redraws when its box changes, because the box is what
		// its backing store is sized from.
		uname := fmt.Sprintf("$u_%s_canvas", id[1:])
		body := cs.drawCall() + ";"
		if cs.scaling != "" {
			body += fmt.Sprintf("_snglCanvasWatch(%s,function(){%s});", id, cs.drawCall())
		}
		g.initWrites = append(g.initWrites, updateFunc{
			funcName: uname,
			body:     body,
			initOnly: true,
		})
	}
	style := g.buildCSSStyle(n)
	if css := canvasScalingCSS(n); css != "" {
		if style != "" {
			style += ";"
		}
		style += css
	}

	// innerText and innerHTML render as element content, not attributes.
	props := nodeProps(n)
	// The tag names the element rather than being one of its attributes.
	delete(props, tagProp)
	var attrs strings.Builder
	staticInnerText := ""
	staticInnerHTML := ""
	for _, name := range slices.Sorted(maps.Keys(props)) {
		expr := props[name]
		if name == "style" || name == "class" {
			continue
		}
		// Read into the element's CSS by canvasScalingCSS, and not an
		// attribute any element has.
		if name == "scalingMode" {
			continue
		}
		if codegen.IRIsReactive(expr) {
			if val := g.evalInitialString(expr); val != "" {
				switch name {
				case "innerHTML":
					staticInnerHTML = val
				case "innerText", "textContent":
					staticInnerText = val
				default:
					fmt.Fprintf(&attrs, " %s=\"%s\"", html.EscapeString(name), html.EscapeString(val))
				}
				continue
			}
			continue
		}
		val := g.evalStaticString(props, name)
		switch name {
		case "innerText", "textContent":
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

	switch {
	case voidElements[tag]:
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
			// A whitespace-sensitive tag renders its children inline: the
			// pretty-printer's newlines would be visible in the output.
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

	// For a NoReactivity-assigned __nN id, lowering already injected the
	// per-mutation Assign into the handler bodies, so the updater is initOnly
	// and findAffectedUpdaters skips it rather than double-firing.
	lowered := loweredID(id)
	for _, name := range slices.Sorted(maps.Keys(props)) {
		expr := props[name]
		if name == "style" {
			continue
		}
		if codegen.IRIsReactive(expr) {
			jsVal, requires := g.exprToJSReactiveCollect(expr)
			deps := g.exprDeps(expr)
			uname := fmt.Sprintf("$u_%s_%s", id[1:], name)
			var body string
			if field, ok := domPropForProp(decl, name); ok {
				body = fmt.Sprintf(`%s.%s = %s;`, id, field, jsVal)
			} else {
				body = fmt.Sprintf(`%s.setAttribute(%q, %s);`, id, name, jsVal)
			}
			// An i18n.tr-rooted prop with no state deps still varies by
			// locale; initOnly keeps it past OptimizeMutation's empty-deps
			// prune so it runs on initial render.
			initOnly := lowered || (len(deps) == 0 && exprUsesI18n(expr))
			g.initWrites = append(g.initWrites, updateFunc{
				funcName: uname,
				body:     body,
				deps:     deps,
				initOnly: initOnly,
				requires: requires,
			})
		}
	}

	// A raw element's event name is the DOM event name, and every event
	// registers the same way: whether the listener takes an argument is the
	// handler's parameter list to answer, not the event name's.
	for i := range n.Handlers {
		h := &n.Handlers[i]
		if h.Func == nil {
			continue
		}
		event := domEventName(decl, h.Name)
		if event == "" {
			continue
		}
		g.addEventHandler(decl, id, event, h.Func)
	}
}

// findIRComponent resolves a bare or namespace-qualified component name off
// the IR package's symbol table, so imported components are reachable.
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

// renderIRUserComponent inlines a user component at its call site, sourcing
// params/vars/computed from ir.Component, which covers imported packages the
// AST doc never sees.
func (g *htmlGen) renderIRUserComponent(b *strings.Builder, n *ir.NodeInst, depth int) {
	comp := n.Component
	if comp == nil {
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
	maps.Copy(savedLocals, g.ctx.Locals)
	savedRenames := g.ctx.Renames
	renames := make(map[string]string)
	if savedRenames != nil {
		maps.Copy(renames, savedRenames)
	}

	for _, p := range comp.Props {
		uniqueName := p.Name + suffix
		g.ctx.Locals[p.Name] = true
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

	for _, fn := range comp.Funcs {
		if len(fn.Params) == 0 && len(fn.Block) == 1 {
			if _, isRet := fn.Block[0].(*ir.Return); isRet {
				uniqueName := fn.Name + suffix
				g.ctx.Locals[fn.Name] = true
				renames[fn.Name] = uniqueName
			}
		}
	}
	g.ctx.Renames = renames
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

	// A synthesized context var may be overridden at the call site by a hidden
	// __ctx_<name> arg in n.Props, which supersedes the var's default Init.
	dataRenames := make(map[string]string)
	for _, dv := range comp.Vars {
		uniqueName := dv.Name + suffix
		// A promoted name is backed by no *ir.Var, so it stays out of the
		// pointer-keyed DepTracker; dataRenames handles it at the boundary.
		g.ctx.Locals[dv.Name] = true
		renames[dv.Name] = "state." + uniqueName
		dataRenames[dv.Name] = uniqueName
		var initJS string
		if dv.Synthesized {
			for _, pa := range n.Props {
				if pa.Name == dv.Name {
					initJS = g.exprToJS(pa.Value)
					break
				}
			}
		}
		if initJS == "" {
			initJS = g.literalToJS(dv.Init)
		}
		g.inlinedStateInits = append(g.inlinedStateInits, componentParam{
			name:  uniqueName,
			value: initJS,
		})
	}
	g.ctx.Renames = renames

	savedDataRenames := g.dataRenames
	g.dataRenames = dataRenames

	savedSlot := g.irSlotChildren
	g.irSlotChildren = n.Children

	for _, t := range comp.Timers {
		g.addIRTimer(t)
	}

	for _, s := range comp.Body {
		g.renderIRStmt(b, s, depth)
	}

	g.irSlotChildren = savedSlot
	g.dataRenames = savedDataRenames

	g.ctx.Locals = savedLocals
	g.ctx.Renames = savedRenames
}

func (g *htmlGen) emitScript(b *strings.Builder) {
	// Inlined component timers were already appended during rendering.
	if g.pkg != nil {
		for _, t := range g.pkg.Timers {
			g.addIRTimer(t)
		}
		if main := g.rootComp; main != nil {
			for _, t := range main.Timers {
				g.addIRTimer(t)
			}
		}
	}
	for i := range g.timers {
		g.timers[i].index = i
	}

	g.optimizeIR()

	// Impure native funcs are surfaced through the __sngl_externs object that
	// WASM instantiation populates at load time.
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

	// A var whose initializer reads other state is emitted as a separate
	// `state.X = ...;` after the object literal, so the sibling read does not
	// hit a temporal-dead-zone reference.
	b.WriteString("// State\nlet state = {")
	var stateFields []string
	var deferredInits []struct{ name, value string }
	stateVars := g.stateVars()
	for _, dv := range stateVars {
		val := g.literalToJS(dv.Init)
		if codegen.IRIsLiteral(dv.Init) {
			stateFields = append(stateFields, dv.Name+": "+val)
		} else {
			// Seeded so the object shape is correct for code that walks the
			// keys before init completes.
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
	if g.testMode {
		// Bridged onto the state object as zero-arg methods so tests can read
		// `c.<computed>()`. Function declarations hoist, so referencing them
		// before their definition is fine.
		for _, fn := range g.pkgFuncs() {
			if !codegen.IsComputed(fn) {
				continue
			}
			if fn.Receiver != "" {
				fmt.Fprintf(b, "state.%s = () => %s_%s(state);\n", fn.Name, fn.Receiver, fn.Name)
			} else {
				fmt.Fprintf(b, "state.%s = () => $%s();\n", fn.Name, fn.Name)
			}
		}
		g.emitEventInvokers(b)
		// Hoisted onto window so the testagent's newTestComponent() returns a
		// live reference. Tests share one state across the session.
		b.WriteString("if (typeof window !== 'undefined') { window.__sngl_state = state; }\n")
	}
	b.WriteString("\n")

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

	funcs := g.pkgFuncs()
	hasComputed := false
	for _, fn := range funcs {
		if !codegen.IsComputed(fn) || len(fn.Block) != 1 {
			continue
		}
		ret, ok := fn.Block[0].(*ir.Return)
		if !ok || ret.Value == nil {
			continue
		}
		body := g.exprToJS(ret.Value)
		if fn.Receiver != "" {
			fmt.Fprintf(b, "function %s_%s(state) { return %s; }\n", fn.Receiver, fn.Name, body)
		} else {
			fmt.Fprintf(b, "function $%s() { return %s; }\n", fn.Name, body)
		}
		hasComputed = true
	}
	if hasComputed {
		b.WriteString("\n")
	}

	if mf := javascript.EmitMergeFuncs(g.pkg.MergeStructs); mf != "" {
		b.WriteString(mf)
	}

	emittedFuncs := false
	for _, fn := range funcs {
		g.emitJSFunc(b, fn)
		emittedFuncs = true
	}
	if emittedFuncs {
		b.WriteString("\n")
	}
	if g.testMode && emittedFuncs {
		// The test module is bundled beside the page, not inside its IIFE, so
		// a call it makes to one of these -- `Calc_digit(…)`, the same name
		// the emitter used -- has to find it. Hoisted like the state object
		// above. The shorthand keys survive minification even when the
		// bindings are renamed.
		var names []string
		for _, fn := range funcs {
			if len(fn.Block) == 0 {
				continue
			}
			names = append(names, jsFuncName(fn))
		}
		if len(names) > 0 {
			fmt.Fprintf(b, "if (typeof window !== 'undefined') { Object.assign(window, { %s }); }\n\n", strings.Join(names, ", "))
		}
	}

	if g.pkg != nil && len(g.pkg.AsyncKickers) > 0 {
		for _, k := range g.pkg.AsyncKickers {
			g.emitJSFunc(b, k.Func)
		}
		b.WriteString("\n")
	}

	g.emitSynthesizedSlots(b)
	g.emitCanvasSetups(b)

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

	consts := g.pkgConsts()
	for _, c := range consts {
		val := g.literalToJS(c.Init)
		fmt.Fprintf(b, "const %s = %s;\n", c.Name, val)
	}
	if len(consts) > 0 {
		b.WriteString("\n")
	}

	// g.ctx.Helpers is the unified map, so it also carries flags written by
	// the JsIRContext path during emitScript.
	if g.ctx.Helpers["String"] {
		b.WriteString("function String(v) { return \"\" + v; }\n\n")
	}

	for _, cp := range g.componentParams {
		if cp.staticOnly {
			continue
		}
		fmt.Fprintf(b, "const %s = %s;\n", cp.name, cp.value)
	}
	if len(g.componentParams) > 0 {
		b.WriteString("\n")
	}

	// An id can appear in both refs and loweredRefs; prefer the querySelector
	// form, since emitting both is a duplicate `const __nN` that throws at
	// parse time.
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

	g.emitHandlers(b)

	g.emitTimers(b)

	// Every html updater is init-only: NoReactivity inlines the per-mutation
	// DOM writes into handler bodies, so no callable $u_*() is emitted.
	if len(g.initWrites) > 0 || len(g.timers) > 0 {
		b.WriteString("\nfunction __sngl_init() {\n")
		for _, u := range g.initWrites {
			fmt.Fprintf(b, "  %s\n", u.body)
		}
		for _, t := range g.timers {
			fmt.Fprintf(b, "  $timer_%d_sync();\n", t.index)
		}
		b.WriteString("}\n__sngl_init();\n")
	}

	// After the DOM updaters are wired, so a kicker body can call setters.
	if g.pkg != nil && len(g.pkg.AsyncKickers) > 0 {
		b.WriteString("\n// Async kicker startup\n")
		for _, k := range g.pkg.AsyncKickers {
			fmt.Fprintf(b, "%s();\n", k.Func.Name)
		}
	}

	if g.preview {
		b.WriteString("\nfunction __sngl_sync_state() { window.parent.postMessage({type: 'sngl-state', state: JSON.parse(JSON.stringify(state))}, '*'); }\n")
		b.WriteString("__sngl_sync_state();\n")
	}

}

// emitSynthesizedSlots writes the slot accumulators, render funcs, anchor
// bindings and initial render call for every slot.
//
// Each reactive if/for renders into a `display:contents` anchor at its source
// position, and the render func's `parent` param is bound to it (see
// rewriteSlotCallsToAnchors), so children appear in-flow. The old
// `__root = null` sentinel is therefore never referenced and is not emitted.
func (g *htmlGen) emitSynthesizedSlots(b *strings.Builder) {
	if g.pkg == nil {
		return
	}
	synthVars := g.synthesizedVars()
	synthFuncs := g.synthesizedFuncs()
	if len(synthVars) == 0 && len(synthFuncs) == 0 {
		return
	}
	jc := javascript.NewIRContext(g.ctx)

	emittedVar := false
	for _, v := range synthVars {
		// __root is a dead sentinel: slots render into their own anchors now.
		if v.Name == "__root" {
			continue
		}
		b.WriteString("let " + v.Name + " = ")
		if v.Init != nil {
			b.WriteString(jc.EvalExpr(v.Init))
		} else {
			b.WriteString("null")
		}
		b.WriteString(";\n")
		emittedVar = true
	}
	if emittedVar {
		b.WriteString("\n")
	}

	for _, fn := range synthFuncs {
		tr := g.newHTMLTranslator(jc)
		body := codegen.WalkLowered(context.Background(), fn.Block, tr)
		synthesized := &ir.Func{
			Name:   fn.Name,
			Params: fn.Params,
			Block:  body,
		}
		for _, line := range jc.EmitFuncDef(synthesized) {
			b.WriteString(line)
			b.WriteByte('\n')
		}
		b.WriteByte('\n')
	}

	// Driven off the synthesized-func list rather than a main.Body scan, so
	// slots nested inside windows/components are initialized too.
	for _, fn := range synthFuncs {
		idx := slotIndexFromRenderFunc(fn.Name)
		if idx == "" {
			continue
		}
		anchor := slotAnchorVar(idx)
		fmt.Fprintf(b, "var %s = document.querySelector('[data-sngl-slot=\"%s\"]');\n", anchor, idx)
		fmt.Fprintf(b, "%s(%s);\n", fn.Name, anchor)
	}
}

// canvasScalingCSS is the display size and fit of a canvas whose `scalingMode`
// asks for one.
//
// `width`/`height` on a canvas are its backing store -- the coordinate space
// the shapes were placed in -- and the CSS box is how big it is shown. A
// browser scales the one to the other, and object-fit says how, which is the
// same four choices SNGL spells.
//
// `center` is the default and adds nothing: the element stays the size of its
// drawing, which is what every canvas did before there was a choice.
func canvasScalingCSS(n *ir.NodeInst) string {
	if n == nil || n.CanvasDraw == nil {
		return ""
	}
	// Emitted after the width and height the canvas declared, so it wins:
	// those two size the backing store, and this is the box the shapes are
	// drawn into. No object-fit -- the helper sizes the backing store to this
	// box and scales the geometry, so there is no picture left to resample.
	switch canvasScalingMode(n) {
	case canvasutil.ScaleFit:
		// aspect-ratio rather than height:auto alone: the backing store's own
		// dimensions move at runtime, and the box must not follow them.
		w, h := canvasIntProp(n, "width"), canvasIntProp(n, "height")
		if w <= 0 || h <= 0 {
			return "width:100%"
		}
		return fmt.Sprintf("width:100%%;height:auto;aspect-ratio:%d/%d", w, h)
	case canvasutil.ScaleFill, canvasutil.ScaleStretch:
		return "width:100%;height:100%"
	}
	return ""
}

// canvasScalingMode reads a canvas node's `scalingMode` prop.
func canvasScalingMode(n *ir.NodeInst) string {
	if v, ok := codegen.NodeProp(n, "scalingMode").(*ir.Ident); ok {
		return v.Member
	}
	return ""
}

// emitCanvasSetups emits the _snglColor helper once when any canvas element is
// present. The per-canvas draw call comes from the updaters registered in
// initWrites, so no per-canvas IIFE is emitted here.
func (g *htmlGen) emitCanvasSetups(b *strings.Builder) {
	if len(g.canvasSetups) == 0 {
		return
	}
	b.WriteString(snglColorHelper)
	b.WriteString(snglCanvasHelper)
}

// timerSyncCalls returns a $timer_N_sync() call for every timer whose enabled
// var is in mutated, so the timer starts or stops to match the new state.
// Handler bodies write state directly rather than through $set_<var>, which
// carries its own sync, so the call is appended here.
func (g *htmlGen) timerSyncCalls(mutated map[string]bool) []string {
	if len(mutated) == 0 {
		return nil
	}
	var out []string
	for _, t := range g.timers {
		if t.activeVar != "" && mutated[t.activeVar] {
			out = append(out, fmt.Sprintf("$timer_%d_sync();", t.index))
		}
	}
	return out
}

// reactiveUpdaterCalls returns the bodies of non-initOnly updaters whose deps
// overlap the mutated var set — the ones NoReactivity cannot inline, which
// must be appended to every handler that touches their deps.
func (g *htmlGen) reactiveUpdaterCalls(mutated map[string]bool) []string {
	if len(mutated) == 0 {
		return nil
	}
	var out []string
	for _, u := range g.initWrites {
		if u.initOnly {
			continue
		}
		for dep := range u.deps {
			if mutated[dep] {
				out = append(out, u.body)
				break
			}
		}
	}
	return out
}

func (g *htmlGen) emitHandlers(b *strings.Builder) {
	for _, h := range g.handlers {
		var lines []string
		lines = append(lines, h.body)
		lines = append(lines, g.timerSyncCalls(h.mutated)...)
		lines = append(lines, g.reactiveUpdaterCalls(h.mutated)...)
		if g.preview {
			lines = append(lines, "__sngl_sync_state();")
		}
		body := strings.Join(lines, "\n  ")
		keyword := "function"
		if h.isAsync {
			keyword = "async function"
		}
		param := ""
		if h.hasParam {
			param = "e"
		}
		fmt.Fprintf(b, "%s.addEventListener(\"%s\", %s(%s) {\n  %s\n});\n", h.elemID, h.event, keyword, param, body)
	}
}

func (g *htmlGen) emitTimers(b *strings.Builder) {
	for _, t := range g.timers {
		var tickLines []string
		tickLines = append(tickLines, t.body)
		tickLines = append(tickLines, g.timerSyncCalls(t.mutated)...)
		tickLines = append(tickLines, g.reactiveUpdaterCalls(t.mutated)...)
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
		// No controlling Active var means always-on.
		activeExpr := "true"
		if t.activeVar != "" {
			activeExpr = "state." + t.activeVar
		}
		fmt.Fprintf(b, "  if (%s && !$timer_%d) {\n", activeExpr, t.index)
		fmt.Fprintf(b, "    $timer_%d = setInterval($timer_%d_tick, %d);\n", t.index, t.index, t.intervalMs)
		fmt.Fprintf(b, "  } else if (!%s && $timer_%d) {\n", activeExpr, t.index)
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
	for _, h := range dv.Handlers {
		if h.Name == "change" && h.Func != nil {
			for _, line := range g.translateBlockJC(h.Func.Block) {
				fmt.Fprintf(b, "  %s\n", line)
			}
		}
	}
	for _, t := range g.timers {
		if t.activeVar == dv.Name {
			fmt.Fprintf(b, "  $timer_%d_sync();\n", t.index)
		}
	}
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

	for _, u := range g.initWrites {
		// A lowered id ($u__nN_xxx) references a data-sngl-id'd element; mark
		// it so the loweredRefs emitter declares the const for it.
		parts := strings.SplitN(u.funcName, "_", 3)
		if len(parts) >= 2 && parts[1] != "" && allDigits(parts[1]) {
			addID("$" + parts[1])
		} else if len(parts) >= 3 && parts[1] == "" && strings.HasPrefix(parts[2], "n") {
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
		// Some updaters (for-else, if-else) manipulate a different element
		// than the one encoded in their name.
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

// extractElemIDs returns each unique `$N` element-handle identifier in a JS
// snippet.
func extractElemIDs(js string) []string {
	var out []string
	seen := make(map[string]bool)
	for i := 0; i < len(js); i++ {
		if js[i] != '$' {
			continue
		}
		j := i + 1
		if j >= len(js) || js[j] < '0' || js[j] > '9' {
			continue
		}
		// $ must not be preceded by an identifier character, or this matches
		// inside a longer name like $u_0_list.
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

func (g *htmlGen) optimizeIR() {
	// The pointer-keyed dep sets are synthesized from html's name-keyed maps;
	// iropt only reads *ir.Var.Name, so synthetic placeholders are safe.
	varReg := newVarRegistry(g.dt)
	updaters := make([]codegen.Updater, len(g.initWrites))
	for i, u := range g.initWrites {
		updaters[i] = codegen.Updater{
			Name:     u.funcName,
			Body:     u.body,
			Deps:     varReg.namesToVarSet(u.deps),
			InitOnly: u.initOnly,
			Requires: u.requires,
		}
	}
	// IR keys on ast.Node, but html has already translated to JS strings.
	handlerBodyMap := make(map[string]string)
	handlerAsyncMap := make(map[string]bool)
	handlerParamMap := make(map[string]bool)
	handlers := make([]codegen.Handler, len(g.handlers))
	for i, h := range g.handlers {
		key := h.elemID + ":" + h.event
		handlerBodyMap[key] = h.body
		handlerAsyncMap[key] = h.isAsync
		handlerParamMap[key] = h.hasParam
		handlers[i] = codegen.Handler{
			NodeID:  h.elemID,
			Event:   h.event,
			Mutated: varReg.namesToVarSet(h.mutated),
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
			Mutated: varReg.namesToVarSet(t.mutated),
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

	// Only updaters are droppable, so only they carry their requirements
	// through OptimizeMutation rather than flagging g.ctx during translation;
	// union the survivors' in here.
	for _, u := range m.Updaters {
		u.Requires.MergeInto(g.ctx.Helpers, g.ctx.NativeImports)
	}

	g.initWrites = make([]updateFunc, len(m.Updaters))
	for i, u := range m.Updaters {
		g.initWrites[i] = updateFunc{
			funcName: u.Name,
			body:     u.Body,
			deps:     varSetToNames(u.Deps),
			initOnly: u.InitOnly,
		}
	}

	g.handlers = make([]eventHandler, len(m.Handlers))
	for i, h := range m.Handlers {
		key := h.NodeID + ":" + h.Event
		g.handlers[i] = eventHandler{
			elemID:   h.NodeID,
			event:    h.Event,
			body:     handlerBodyMap[key],
			hasParam: handlerParamMap[key],
			mutated:  varSetToNames(h.Mutated),
			isAsync:  handlerAsyncMap[key],
		}
	}

	g.deduplicateComponentParams()
}

// deduplicateComponentParams merges component params with identical JS
// expressions, rewriting updater-body references to the surviving name.
func (g *htmlGen) deduplicateComponentParams() {
	if len(g.componentParams) <= 1 {
		return
	}

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

	for i, u := range g.initWrites {
		body := u.body
		for old, canonical := range renames {
			body = strings.ReplaceAll(body, old, canonical)
		}
		g.initWrites[i].body = body
	}
}

// loweredID reports whether id was assigned by NoReactivity lowering, which
// already injected the inline mutation updates, so its updaters are init-only.
func loweredID(id string) bool { return strings.HasPrefix(id, "__n") }

// exprUsesI18n keeps an init-time text updater that has no state-var deps but
// still depends on the active locale.
func exprUsesI18n(expr ir.Expr) bool {
	found := false
	var walk func(e ir.Expr)
	walk = func(e ir.Expr) {
		if found || e == nil {
			return
		}
		if c, ok := e.(*ir.Call); ok {
			if c.Func != nil && snglI18n.IsCall(c) {
				found = true
				return
			}
			for _, a := range c.Args {
				walk(a.Value)
			}
			if c.Receiver != nil {
				walk(c.Receiver)
			}
			if c.Callee != nil {
				walk(c.Callee)
			}
			return
		}
		switch x := e.(type) {
		case *ir.Binary:
			walk(x.Left)
			walk(x.Right)
		case *ir.Unary:
			walk(x.Operand)
		case *ir.Ternary:
			walk(x.Cond)
			walk(x.Then)
			walk(x.Else)
		case *ir.Conversion:
			walk(x.Operand)
		case *ir.Select:
			walk(x.Operand)
		case *ir.Index:
			walk(x.Operand)
			walk(x.Idx)
		case *ir.ListLit:
			for _, el := range x.Elems {
				walk(el)
			}
		case *ir.MapLitIR:
			for _, en := range x.Entries {
				walk(en.Key)
				walk(en.Value)
			}
		case *ir.StructLit:
			for _, f := range x.Fields {
				walk(f.Value)
			}
		case *ir.Spread:
			walk(x.Operand)
		case *ir.Lambda:
			if x.Func != nil {
				for _, s := range x.Func.Block {
					walkStmtExprs(s, walk)
				}
			}
		case *ir.Closure:
			if x.Func != nil {
				for _, s := range x.Func.Block {
					walkStmtExprs(s, walk)
				}
			}
		case *ir.Literal, *ir.Ident, *ir.ContextRead:
		default:
			panic(fmt.Sprintf("html.exprUsesI18n: unhandled ir.Expr %T", x))
		}
	}
	walk(expr)
	return found
}

func walkStmtExprs(s ir.Stmt, walk func(ir.Expr)) {
	switch n := s.(type) {
	case *ir.Assign:
		walk(n.Target)
		walk(n.Value)
	case *ir.CallStmt:
		if n.Call != nil {
			walk(n.Call)
		}
	case *ir.LocalVar:
		walk(n.Init)
	case *ir.Return:
		walk(n.Value)
	case *ir.If:
		walk(n.Cond)
		for _, c := range n.Body {
			walkStmtExprs(c, walk)
		}
		for _, c := range n.Else {
			walkStmtExprs(c, walk)
		}
	case *ir.For:
		walk(n.Iter)
		for _, c := range n.Body {
			walkStmtExprs(c, walk)
		}
		for _, c := range n.Else {
			walkStmtExprs(c, walk)
		}
	case *ir.Emit:
		for _, a := range n.Args {
			walk(a.Value)
		}
	case *ir.Toggle:
		walk(n.Target)
	}
}

// exprDeps remaps through dataRenames inside a component scope, so deps use
// the promoted field names.
func (g *htmlGen) exprDeps(expr ir.Expr) map[string]bool {
	varDeps := g.dt.ExprDeps(g.currentComp, expr)
	names := varSetToNames(varDeps)
	return g.remapMutated(names, g.dataRenames)
}

func varSetToNames(vs map[*ir.Var]struct{}) map[string]bool {
	out := make(map[string]bool, len(vs))
	for v := range vs {
		out[v.Name] = true
	}
	return out
}

// varRegistry maps names back to *ir.Var pointers at the codegen boundary.
// A promoted or suffixed name is synthesized on demand; iropt only reads
// .Name, so a synthetic placeholder is safe.
type varRegistry struct {
	byName map[string]*ir.Var
}

func newVarRegistry(dt *codegen.DepTracker) *varRegistry {
	r := &varRegistry{byName: make(map[string]*ir.Var)}
	if dt != nil {
		for v := range dt.ModelVars {
			r.byName[v.Name] = v
		}
	}
	return r
}

func (r *varRegistry) lookup(name string) *ir.Var {
	if v, ok := r.byName[name]; ok {
		return v
	}
	v := &ir.Var{Name: name}
	r.byName[name] = v
	return v
}

func (r *varRegistry) namesToVarSet(names map[string]bool) map[*ir.Var]struct{} {
	if len(names) == 0 {
		return nil
	}
	out := make(map[*ir.Var]struct{}, len(names))
	for n, ok := range names {
		if !ok {
			continue
		}
		out[r.lookup(n)] = struct{}{}
	}
	return out
}

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

// scopedJC clones g.ctx with the live locals, renames and EventVar, so
// identifiers resolve as they do elsewhere in html emission.
func (g *htmlGen) scopedJC() *javascript.JsIRContext {
	c := g.ctx.Clone()
	jc := javascript.NewIRContext(c)
	jc.EventVar = c.EventVar
	jc.EventParam = c.EventParam
	return jc
}

// scopedJCFresh is scopedJC with its own empty Helpers/NativeImports maps, for
// a body whose needs must be captured in isolation — an updater
// OptimizeMutation may drop must not pollute the shared set.
func (g *htmlGen) scopedJCFresh() *javascript.JsIRContext {
	c := g.ctx.Clone()
	c.Helpers = map[string]bool{}
	c.NativeImports = map[string]map[string]bool{}
	jc := javascript.NewIRContext(c)
	jc.EventVar = c.EventVar
	jc.EventParam = c.EventParam
	return jc
}

// translateBlockJC routes an IR block through WalkLowered + htmlTranslator +
// JsIRContext, returning JS statements with trailing semicolons.
func (g *htmlGen) translateBlockJC(body []ir.Stmt) []string {
	jc := g.scopedJC()
	tr := g.newHTMLTranslatorWithNodes(jc, g.idToNode)
	// A CanvasRedrawStmt needs a NodeInst→ID lookup only htmlGen has, so it is
	// handled here rather than in the translator.
	var regular []ir.Stmt
	var redraws []*ir.CanvasRedrawStmt
	for _, s := range body {
		if rs, ok := s.(*ir.CanvasRedrawStmt); ok {
			redraws = append(redraws, rs)
		} else {
			regular = append(regular, s)
		}
	}
	fragments := codegen.WalkLowered(context.Background(), regular, tr)
	var lines []string
	for _, s := range fragments {
		g.collectLoweredRefs(s)
		for _, ln := range jc.EvalStmt(s) {
			lines = append(lines, ln+";")
		}
	}
	for _, rs := range redraws {
		lines = append(lines, g.canvasRedrawLine(rs)+";")
	}
	return lines
}

func (g *htmlGen) canvasRedrawLine(rs *ir.CanvasRedrawStmt) string {
	for _, cs := range g.canvasSetups {
		if cs.drawFunc == rs.DrawFunc {
			return cs.drawCall()
		}
	}
	return "" // draw func not found (shouldn't happen)
}

// collectLoweredRefs registers every IsElementRef Ident in g.loweredRefs so the
// prelude emits a querySelector for it. Only `__n*` ids go through the
// data-sngl-id path; `$N` ids use the getElementById emission.
func (g *htmlGen) collectLoweredRefs(s ir.Stmt) {
	var walkExpr func(e ir.Expr)
	walkExpr = func(e ir.Expr) {
		if e == nil {
			return
		}
		switch x := e.(type) {
		case *ir.Ident:
			if x.IsElementRef && loweredID(x.Name) {
				g.loweredRefs[x.Name] = true
			}
		case *ir.Binary:
			walkExpr(x.Left)
			walkExpr(x.Right)
		case *ir.Unary:
			walkExpr(x.Operand)
		case *ir.Ternary:
			walkExpr(x.Cond)
			walkExpr(x.Then)
			walkExpr(x.Else)
		case *ir.Select:
			walkExpr(x.Operand)
		case *ir.Index:
			walkExpr(x.Operand)
			walkExpr(x.Idx)
		case *ir.Call:
			walkExpr(x.Receiver)
			walkExpr(x.Callee)
			for _, a := range x.Args {
				walkExpr(a.Value)
			}
		case *ir.Conversion:
			walkExpr(x.Operand)
		case *ir.ListLit:
			for _, el := range x.Elems {
				walkExpr(el)
			}
		case *ir.StructLit:
			for _, f := range x.Fields {
				walkExpr(f.Value)
			}
		case *ir.MapLitIR:
			for _, kv := range x.Entries {
				walkExpr(kv.Key)
				walkExpr(kv.Value)
			}
		case *ir.Spread:
			walkExpr(x.Operand)
		case *ir.Lambda, *ir.Closure:
		case *ir.Literal, *ir.ContextRead:
		default:
			panic(fmt.Sprintf("html.collectLoweredRefs.walkExpr: unhandled ir.Expr %T", x))
		}
	}
	switch n := s.(type) {
	case *ir.Assign:
		walkExpr(n.Target)
		walkExpr(n.Value)
	case *ir.CallStmt:
		walkExpr(n.Call)
	case *ir.Return:
		walkExpr(n.Value)
	case *ir.If:
		walkExpr(n.Cond)
		for _, b := range n.Body {
			g.collectLoweredRefs(b)
		}
		for _, b := range n.Else {
			g.collectLoweredRefs(b)
		}
	case *ir.For:
		walkExpr(n.Iter)
		for _, b := range n.Body {
			g.collectLoweredRefs(b)
		}
	case *ir.LocalVar:
		walkExpr(n.Init)
	case *ir.Emit:
		for _, a := range n.Args {
			walkExpr(a.Value)
		}
	case *ir.Toggle:
		walkExpr(n.Target)
	case *ir.NodeInst:
		for _, p := range n.Props {
			walkExpr(p.Value)
		}
		for _, c := range n.Children {
			g.collectLoweredRefs(c)
		}
	case *ir.SlotInst:
		for _, c := range n.Children {
			g.collectLoweredRefs(c)
		}
	case *ir.ErrorBoundary:
		for _, c := range n.Children {
			g.collectLoweredRefs(c)
		}
	case *ir.Window:
		for _, c := range n.Body {
			g.collectLoweredRefs(c)
		}
	case *ir.ContextProvider:
		for _, c := range n.Children {
			g.collectLoweredRefs(c)
		}
	case *ir.CanvasRedrawStmt:
	case *ir.Break, *ir.Continue:
		// A loop escape names no ref.
	default:
		panic(fmt.Sprintf("html.collectLoweredRefs: unhandled ir.Stmt %T", n))
	}
}

// addEventHandler registers a handler for one DOM event. What its event-arg
// param stands for is the event's declared payload to say (see
// eventPayloadBase); whether it has one decides the listener's signature.
func (g *htmlGen) addEventHandler(decl *ir.Component, elemID, event string, fn *ir.Func) {
	if fn == nil || len(fn.Block) == 0 {
		return
	}
	savedEvent := g.ctx.EventVar
	savedEventParam := g.ctx.EventParam
	g.ctx.EventVar = eventPayloadBase(decl, event)
	if len(fn.Params) > 0 {
		g.ctx.EventParam = fn.Params[0]
	}
	// The parameter is a local; what it stands for is EventParam's job.
	// Renaming by name as well would replace a lambda parameter spelled the
	// same, emitting `e2.target` where the lambda's own value was meant.
	var savedLocal []string
	for _, p := range fn.Params {
		if p == nil || p.Name == "" {
			continue
		}
		if !g.ctx.Locals[p.Name] {
			savedLocal = append(savedLocal, p.Name)
			g.ctx.Locals[p.Name] = true
		}
	}
	lines := g.translateBlockJC(fn.Block)
	mutated := make(map[string]bool)
	for _, s := range fn.Block {
		for v := range codegen.MutatedFields(g.currentComp, g.dt, s) {
			mutated[v.Name] = true
		}
	}
	g.ctx.EventVar = savedEvent
	g.ctx.EventParam = savedEventParam
	for _, n := range savedLocal {
		delete(g.ctx.Locals, n)
	}
	mutated = g.remapMutated(mutated, g.dataRenames)
	g.handlers = append(g.handlers, eventHandler{
		elemID: elemID,
		event:  event,
		body:   strings.Join(lines, "\n  "),
		// With no declared parameter nothing in the body can resolve to the
		// event, so the listener takes none either.
		hasParam: len(fn.Params) > 0,
		mutated:  mutated,
		isAsync:  ir.BlockHasFuncvarAsyncCall(fn.Block, g.pts()),
	})
}

// addIRTimer translates the handler body through the current scope, so state
// references resolve to the unique renamed field names.
func (g *htmlGen) addIRTimer(t *ir.Timer) {
	if t == nil || t.Handler == nil {
		return
	}
	lines := g.translateBlockJC(t.Handler.Block)
	mutated := make(map[string]bool)
	for _, s := range t.Handler.Block {
		for v := range codegen.MutatedFields(g.currentComp, g.dt, s) {
			mutated[v.Name] = true
		}
	}
	mutated = g.remapMutated(mutated, g.dataRenames)
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

func (g *htmlGen) buildCSSStyle(n *ir.NodeInst) string {
	if n == nil {
		return ""
	}
	return htmlutil.BuildCSSStyleIR(n.Props)
}

// receiverParamName is what the method's receiver is called in its own body.
// A nested method's is the implicit `this`; a top-level `func Op.symbol(o Op)`
// names its own. Both are emitted as `state`, so both have to be renamed —
// renaming only `this` left `func Op_symbol(state) { return o === "add" … }`,
// which reads a name the function does not have.
func receiverParamName(fn *ir.Func) string {
	if len(fn.Params) > 0 && fn.Params[0].Receiver {
		return fn.Params[0].Name
	}
	return ir.ReceiverParam
}

// jsFuncName is the JavaScript name a user func is emitted under. A desugared
// method's dotted form lives in fn.Receiver.
func jsFuncName(fn *ir.Func) string {
	if fn.Receiver != "" {
		return fn.Receiver + "_" + fn.Name
	}
	return strings.ReplaceAll(fn.Name, ".", "_")
}

func (g *htmlGen) emitJSFunc(b *strings.Builder, fn *ir.Func) {
	if codegen.IsComputed(fn) && len(fn.Block) == 1 {
		if ret, ok := fn.Block[0].(*ir.Return); ok && ret.Value != nil {
			_ = ret
			return
		}
	}
	params := make([]string, len(fn.Params))
	for i, p := range fn.Params {
		// Emitted as `state` so the param name matches the body's
		// component-self translation -- and, for a struct or enum receiver,
		// so that it is a name JavaScript accepts at all. SNGL spells the
		// receiver `this`, which is a parameter name JS rejects; the body's
		// uses are renamed to match below.
		if i == 0 && fn.Receiver != "" && p.Receiver {
			params[i] = "state"
		} else {
			params[i] = p.Name
		}
	}
	paramStr := strings.Join(params, ", ")

	jsName := jsFuncName(fn)

	keyword := "function"
	if fn.IsAsync {
		keyword = "async function"
	}

	if len(fn.Block) == 1 {
		if ret, ok := fn.Block[0].(*ir.Return); ok && ret.Value != nil {
			jc := g.scopedJC()
			if fn.Receiver != "" {
				jc = jc.WithRenamedLocal(receiverParamName(fn), "state")
			}
			for _, p := range fn.Params {
				jc = jc.WithLocal(p.Name)
			}
			body := jc.EvalExpr(ret.Value)
			fmt.Fprintf(b, "%s %s(%s) { return %s; }\n", keyword, jsName, paramStr, body)
			return
		}
	}
	if len(fn.Block) == 0 {
		return
	}
	fmt.Fprintf(b, "%s %s(%s) {\n", keyword, jsName, paramStr)
	// Routed through the same pipeline as a handler body, so element-ref
	// writes and intrinsic statements lower identically.
	jc := g.scopedJC()
	if fn.Receiver != "" {
		jc = jc.WithRenamedLocal(receiverParamName(fn), "state")
	}
	for _, p := range fn.Params {
		jc = jc.WithLocal(p.Name)
	}
	tr := g.newHTMLTranslatorWithNodes(jc, g.idToNode)
	lowered := codegen.WalkLowered(context.Background(), fn.Block, tr)
	for _, s := range lowered {
		g.collectLoweredRefs(s)
		for _, line := range jc.EvalStmt(s) {
			fmt.Fprintf(b, "  %s;\n", line)
		}
	}
	b.WriteString("}\n")
}

func (g *htmlGen) exprToJS(expr ir.Expr) string {
	if codegen.IRIsReactive(expr) {
		return g.scopedJC().EvalExpr(expr)
	}
	if codegen.IRIsLiteral(expr) {
		return g.lang.TranslateIRLiteral(expr)
	}
	return `""`
}

// exprToJSReactiveCollect translates a reactive expression in an isolated
// context, returning the JS plus the requirements it must carry: an updater
// OptimizeMutation drops must contribute nothing to the shared g.ctx.
// Callers guarantee expr is reactive.
func (g *htmlGen) exprToJSReactiveCollect(expr ir.Expr) (string, codegen.Requirement) {
	jc := g.scopedJCFresh()
	js := jc.EvalExpr(expr)
	return js, codegen.Requirement{
		Helpers:       jc.Ctx.Helpers,
		NativeImports: jc.Ctx.NativeImports,
	}
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
			case ir.TypeInt, ir.TypeFloat:
				// Shared with the JS lang translator so sized-width rendering
				// (BigInt for 64-bit, Math.fround for float32) can't drift.
				if s, ok := javascript.NumericLiteral(lit); ok {
					return s
				}
			case ir.TypeBool:
				return lit.Value
			case ir.TypeNull:
				return "null"
			case ir.TypeUnit:
				return fmt.Sprintf("%q", lit.Value)
			case ir.TypeStruct:
				if ir.StringReprStruct(lit.Type) {
					return fmt.Sprintf("%q", lit.Value)
				}
			}
		}
		return lit.Value
	}
	return g.exprToJS(expr)
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
	if codegen.IRIsReactive(v) {
		return g.evalInitialString(v)
	}
	return ""
}

func (g *htmlGen) evalInitialString(expr ir.Expr) string {
	if !codegen.IRIsReactive(expr) {
		return ""
	}
	js := g.exprToJS(expr)
	return g.resolveJSToString(js)
}

// resolveJSToString evaluates a JS expression to a static string, resolving a
// component param reference through its assigned value.
func (g *htmlGen) resolveJSToString(js string) string {
	if len(js) >= 2 && js[0] == '"' && js[len(js)-1] == '"' {
		if s, err := strconv.Unquote(js); err == nil {
			return s
		}
	}
	for i := range g.componentParams {
		if g.componentParams[i].name == js {
			if resolved := g.resolveJSToString(g.componentParams[i].value); resolved != "" {
				g.componentParams[i].staticOnly = true
				return resolved
			}
			return ""
		}
	}
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

// splitJSConcat splits on top-level " + ", respecting quoting and parens.
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

// noteSnglID records the id a program wrote against the JS variable this
// element is bound to. They are different names -- `#inc` is what a test can
// say, `$1` is what the emitted script holds -- and this is the one place both
// are in hand.
func (g *htmlGen) noteSnglID(elemVar string, n *ir.NodeInst) {
	if elemVar == "" || n == nil || n.ID == "" || strings.HasPrefix(n.ID, "__n") {
		return
	}
	if g.snglIDByElem == nil {
		g.snglIDByElem = map[string]string{}
	}
	g.snglIDByElem[elemVar] = n.ID
}

// emitEventInvokers attaches one function per (#id, @event) pair to the state
// object a test holds, so `c.inc.click()` reaches the element.
//
// The event is dispatched on the element itself -- `.click()` for a click, a
// real Event for the rest -- so it runs through the same addEventListener the
// page wired, rather than calling the handler behind its back. Nothing about
// layout or pointer position is involved; what it skips is whether the element
// is visible and hittable.
//
// They hang off `state` because that is the object newTestComponent() returns,
// which is what makes `c.<id><Event>()` resolve. The computed accessors above
// are attached the same way.
func (g *htmlGen) emitEventInvokers(b *strings.Builder) {
	seen := map[string]bool{}
	for _, h := range g.handlers {
		snglID := g.snglIDByElem[h.elemID]
		if snglID == "" {
			continue // a node no test can name
		}
		name := snglID + capitalizeFirst(h.event)
		if seen[name] {
			continue // duplicate id+event -- keep the first
		}
		seen[name] = true
		if h.event == "click" {
			fmt.Fprintf(b, "state.%s = () => %s.click();\n", name, h.elemID)
			continue
		}
		fmt.Fprintf(b, "state.%s = () => %s.dispatchEvent(new Event(%q));\n", name, h.elemID, h.event)
	}
}

// capitalizeFirst upper-cases the first letter, matching golang.ExportName on
// the names an event can have -- the two halves of a trigger are named on
// opposite sides of the compiler and have to agree.
func capitalizeFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
