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

// Generate writes html platform output directly into sink. This is the
// sink-based path platforms migrate to during the codegen unification.
func (g *Generator) Generate(req *codegen.Request, sink codegen.Sink) error {
	// Agent-mode test build: suppress the user's main() entry and let
	// the testagent's main() drive the page. Mirrors bubbletea/android.
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

	// Validate placement directives (html.frontend/html.backend) before any
	// mode-specific work; these guards are language- and mode-agnostic.
	if err := checkPlacementDirectives(req.Pkg); err != nil {
		return err
	}

	if req.Lang.LanguageIdentifier() == "none" {
		if err := rejectDynamicHrefs(req); err != nil {
			return err
		}
		// A backend (server-side) handler cannot run in a serverless static
		// build. The target lang ("none") is not an HTTPCompiler, so there is
		// no server to host the route's POST handler.
		if win, ok := backendHandlerWindow(req.Pkg, codegen.NewCodegenCtx(req, "html").Windows()); ok {
			return fmt.Errorf("html: window %q has a server-side handler (calls a non-js:// import) but the build target %q has no server — compile with a server language (e.g. --lang go) or wrap the call in html.frontend(...)", win, req.Lang.LanguageIdentifier())
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

// injectTestagentBootstrap rewrites every assembled window in c so its
// closing </body> is preceded by a <script type="module"> tag that
// imports the testagent runtime + emitted test files and calls
// TestAgent.main() on DOMContentLoaded. Called only under agent mode.
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

// compilation holds per-request build state flowing between
// BuildMutationModel and EmitFromMutation.
type compilation struct {
	assetFiles []htmlAssetFile
	windows    []htmlWindowOutput
}

// windowShared holds the results a window's codegen recomputes but that do not
// vary by window. A static site emits one window per page, and everything here
// was being redone that many times.
//
// projectDir and projectFS live here rather than on the htmlGen because
// everything cached below is a function of them: with one place for them to
// come from, no cache key has to carry them.
type windowShared struct {
	projectDir string
	// projectFS is the FS the js:// virtual root is mounted on; it is the OS
	// filesystem for the CLI and an in-memory one for the playground.
	projectFS fs.FS

	bundles      map[bundleKey]bundleResult
	i18nManifest string
	i18nLoaded   bool
	// usedComponents is the visual-tree scan AnalyzeCommon would otherwise
	// repeat; prewalked is the __n* id map prewalkNodes would. Both describe
	// the package, which no window changes.
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
// already fix: the entry text and the two flags read off the htmlGen.
type bundleKey struct {
	entry  string
	minify bool
	maps   bool
}

type bundleResult struct {
	out string
	err error
}

// htmlAssetFile is a resolved asset (name + bytes) accumulated during
// BuildMutationModel and flushed to the Sink in EmitFromMutation.
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
		c.assetFiles = append(c.assetFiles, htmlAssetFile{name: fa.OutPath, bytes: data})
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
			c.assetFiles = append(c.assetFiles, htmlAssetFile{name: outName, bytes: data})
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
	// One cache for every window this compilation emits.
	shared := newWindowShared(projectDir, projectFS)

	ctx := codegen.NewCodegenCtx(req, "html")

	// IR-driven rendering: one file per window. Packages with no main
	// component and no explicit windows still emit an empty index.html so
	// callers can verify codegen at least succeeded.
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
		gen := newHTMLGenFromCtx(ctx, jsLang, opts, shared)
		gen.wasmLoader = wasmLoaderHTML
		gen.wasmPkgs = wasmPkgs
		gen.stylesheet = stylesheetURL
		gen.irBodyStmts = win.Body
		gen.irWindowFuncs = win.Funcs
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

// writeSinkFile writes content to sink under name and closes the writer.
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

	// Type-checked package and expression context (IR-driven).
	pkg *ir.Package
	ctx *codegen.ExprCtx

	// Analysis (shared)
	*codegen.CommonAnalysis
	dt *codegen.DepTracker

	// Element ID counter
	nextID int

	// Init-time DOM writes inlined into __sngl_init()
	initWrites []updateFunc

	// Collected event handlers
	handlers []eventHandler

	// Collected timers
	timers []timerDef

	// Component param constants (name → JS expression)
	componentParams []componentParam

	// Inlined component state fields (unique name → initial JS value)
	inlinedStateInits []componentParam

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

	// shared is this compilation's cache, and the one place the project root
	// and its filesystem come from. Never nil: newHTMLGen allocates one when
	// the caller has no compilation to share.
	shared *windowShared

	// Preview mode: add data-sngl-line/col attributes, ensure all elements have IDs
	preview bool

	// Test mode: emit data-key, id, class attributes for test element lookup
	testMode bool

	// Minify inline JS via esbuild (and HTML in BuildMutationModel).
	minify bool

	// Maps enables source-map generation for the inline JS: extracts inline
	// SNGL position markers, builds a SNGL→JS source map, and routes that
	// map through esbuild so the final output carries an inline SNGL→bundled
	// source map.
	maps bool

	// Component invocation counter for unique param names
	componentInvocations int

	// dataRenames maps original component var names to promoted unique names
	// during component inlining, so handler MutatedFields can be remapped.
	dataRenames map[string]string

	// currentComp is the component owning the currently-rendered tracked
	// context. Used by exprDeps / MutatedFields to resolve implicit `this`.
	currentComp *ir.Component

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

	// irWindowFuncs holds synthesized funcs (e.g. _canvasDrawN) that
	// lowerCanvas placed on the window IR node rather than on the package
	// or main component. Set alongside irBodyStmts when rendering a window.
	irWindowFuncs []*ir.Func

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

	// canvasSetups collects canvas elements needing JS draw wiring.
	canvasSetups []canvasSetup

	// rawElem is the `element` declaration every HTML tag resolves to (see
	// element_decl.go). Resolved once per generator; rawElemDone separates
	// "not looked up yet" from "looked up and there is none".
	rawElem     *ir.Component
	rawElemDone bool
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
	// requires holds the runtime helpers / native imports this updater's body
	// needs, collected structurally during translation. Carried through
	// OptimizeMutation so a dropped (dead) updater contributes nothing to the
	// final helper/import set.
	requires codegen.Requirement
}

type eventHandler struct {
	elemID string
	event  string // the DOM event name passed to addEventListener
	body   string // JS statements
	// hasParam is whether the handler declared a parameter, which is what
	// translation binds `e` from (see eventPayloadBase) and so what the emitted
	// listener's signature follows — rather than the event's name, which says
	// nothing about the handler. A declared parameter the body never reads
	// still sets it.
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

// newHTMLGen builds the generator for one window. shared is the compilation's
// cache; a caller with no compilation to share (a direct unit test) passes nil
// and gets one of its own, so there is only ever one code path below.
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
		preview:        opts.Preview,
		testMode:       opts.Test,
		minify:         opts.Minify,
		idToNode:       make(map[string]*ir.NodeInst),
		loweredRefs:    make(map[string]bool),
		usesI18n:       hasI18nCalls(pkg),
		shared:         shared,
	}

	g.dt = common.DepTracker()
	g.currentComp = mainIRComponent(pkg)
	g.ctx = codegen.NewExprCtx(pkg)
	// Seed package consts as locals so identifier resolution treats them as
	// in-scope names.
	if pkg != nil {
		for _, c := range pkg.Consts {
			g.ctx.Locals[c.Name] = true
		}
	}
	// Allocate the NativeImports accumulator; native calls emitted during
	// codegen append to this shared map.
	if g.ctx.NativeImports == nil {
		g.ctx.NativeImports = map[string]map[string]bool{}
	}
	// Use the single helper-flag map from the analysis: the JsIRContext path
	// writes jc.Ctx.Helpers and the emit check reads g.ctx.Helpers, so wiring
	// them to the same map makes a flag from either path reach the check.
	g.ctx.Helpers = common.Helpers

	return g
}

// newHTMLGenFromCtx creates an htmlGen from CodegenCtx (IR-first path).
func newHTMLGenFromCtx(ctx *codegen.CodegenCtx, lang codegen.LangTranslator, opts htmlConfig, shared *windowShared) *htmlGen {
	g := newHTMLGen(ctx.Pkg, lang, opts, shared)
	g.maps = ctx.ExprCtx.Maps
	if main := ctx.MainComponent(); main != nil {
		g.irBodyStmts = main.Body
		// Capture the native-imports accumulator and helper-flag map wired up
		// by newHTMLGen before re-binding to the component (ForComponent
		// re-clones, replacing the wiring with ctx.ExprCtx's own maps).
		helpers := g.ctx.Helpers
		native := g.ctx.NativeImports
		g.ctx = ctx.ExprCtx.ForComponent(main)
		g.ctx.Helpers = helpers
		g.ctx.NativeImports = native
	}
	return g
}

// prewalkShared seeds idToNode from the cached package walk when there is one.
// The walk finds every __n* id in the package, which is the same set for every
// window; the $N ids allocated while rendering are not, so each window still
// gets its own map to add them to.
func (g *htmlGen) prewalkShared() {
	if g.shared.prewalked == nil {
		g.prewalkNodes()
		g.shared.prewalked = maps.Clone(g.idToNode)
		return
	}
	maps.Copy(g.idToNode, g.shared.prewalked)
}

// rewriteSlotCallsOnce runs the slot retarget for the first window only. The
// rewrite is a package-wide IR edit, so once done it holds for every window
// that follows.
func (g *htmlGen) rewriteSlotCallsOnce() {
	if !g.shared.slotsRewritten {
		g.rewriteSlotCallsToAnchors()
		g.shared.slotsRewritten = true
	}
}

// bundleScript runs the window's script through esbuild, memoized on the
// script text. Every page of a static site shares the same runtime, so a whole
// site collapses to a handful of distinct entries.
func (g *htmlGen) bundleScript(entry string) (string, error) {
	key := bundleKey{entry: entry, minify: g.minify, maps: g.maps}
	if got, ok := g.shared.bundles[key]; ok {
		return got.out, got.err
	}
	out, err := bundleNativeScript(entry, g.shared.projectFS, g.minify, g.maps)
	g.shared.bundles[key] = bundleResult{out: out, err: err}
	return out, err
}

// i18nManifest returns the inlined manifest, loaded from disk once per
// compilation rather than once per window.
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
		case *ir.PlatformFilter:
			visitStmts(n.Body)
		case *ir.ContextProvider:
			visitStmts(n.Children)
		case *ir.Assign, *ir.CallStmt, *ir.LocalVar, *ir.Return, *ir.Emit, *ir.Toggle, *ir.CanvasRedrawStmt:
			// Imperative stmts contain no nested NodeInst to record.
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

// slotIndexFromRenderFunc extracts the N from "__renderSlotN", or "" if the
// name is not a slot render func.
func slotIndexFromRenderFunc(name string) string {
	if !strings.HasPrefix(name, "__renderSlot") {
		return ""
	}
	return strings.TrimPrefix(name, "__renderSlot")
}

// slotAnchorVar returns the JS variable name that holds the DOM anchor for
// slot N: "__slotAnchor_N".
func slotAnchorVar(n string) string {
	return "__slotAnchor_" + n
}

// rewriteSlotCallsToAnchors retargets every `__renderSlotN(parentRef)` CallStmt
// so its single argument becomes the per-slot DOM anchor ident
// `__slotAnchor_N` instead of the threaded parentRef (a real element id or the
// unbound `__root` sentinel). passReactivity threads `parent` uniformly into
// both the slot func's AppendChild (create) and RemoveChild (teardown) calls,
// so binding `parent` to the slot's own `display:contents` anchor renders and
// tears down children in the correct DOM position for both top-level and
// element-nested slots — and removes the dependence on the unbound `__root`.
//
// Runs before the static body walk (which collects handler bodies) so the
// rewrite is observed by init calls, handler/timer re-fires, and the
// placeholder-position emission alike.
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
			// An inline-closure handler (e.g. attachHandler(el, "change",
			// () => { … __renderSlotN(__root) … })) carries slot re-fire calls
			// in its body; descend so they're retargeted to the slot anchor too.
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
		case *ir.PlatformFilter:
			visitStmts(n.Body)
		case *ir.ContextProvider:
			visitStmts(n.Children)
		default:
			// Assign/LocalVar/Return/Emit/Toggle carry no nested slot calls.
		}
	}
	// Visit every block where the reactivity pass may have spliced a
	// __renderSlotN re-fire (after a mutation of a slot's dep var) — mirrors
	// lowerReactivity's pass-2 coverage. Missing timer handlers here left
	// timer-driven re-fires (the carousel) pointing at the threaded parentRef
	// instead of the slot anchor, so they'd removeChild from the wrong node.
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
	g.prewalkShared()

	// Retarget every __renderSlotN(parentRef) call to render into the slot's
	// own display:contents anchor (__slotAnchor_N). Must precede the body
	// walk so handler/timer re-fire calls collected there are rewritten too.
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
		if manifestJS := g.i18nManifest(); manifestJS != "" {
			preamble.WriteString(manifestJS)
		}
		preamble.WriteString(snippet)
		preamble.WriteString("\n")
		script = preamble.String() + script
	}

	if strings.TrimSpace(script) != "" {
		// When source maps are enabled, the JS translator has emitted
		// `/*@SNGL:file:line@*/` markers. Convert them to an inline
		// sourceMappingURL data URL now so esbuild reads it and chains
		// through to a SNGL→bundled output map.
		if g.maps {
			script = inlineSourceMapFromMarkers(script)
		}
		// Always run esbuild: serves as JS formatter/normalizer in
		// addition to bundling native imports and minifying. Output is
		// then consistent regardless of whether the SNGL source uses
		// native imports. Esbuild's IIFE wrapping deterministically
		// renames shadowed parameters (e.g. `state` → `state2` inside a
		// fn whose enclosing IIFE also declares `state`); harmless because
		// param names are scope-local.
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

	// Strip unreferenced IDs from HTML to reduce noise.
	result := b.String()
	refSet := map[string]bool{}
	for _, id := range g.collectReferencedIDs() {
		refSet[id] = true
	}
	return stripUnreferencedIDs(result, refSet, g.nextID), nil
}

// stripUnreferencedIDs removes every ` id="$N"` attribute whose id allocID
// handed out and no handler or updater refers to. One scan of the document:
// the loop over the allocated ids it replaces does a whole-document
// strings.Replace per id, which is quadratic in the page.
//
// nextID bounds what allocID has issued, and only an id it issued is a
// candidate — so `$4` on a page that allocated three is left alone, as is any
// spelling allocID cannot produce (`$00`). Anything else in an ` id="` would
// belong to whatever emitted it.
//
// Only the first occurrence of a given id is dropped, matching the
// strings.Replace(…, 1) it replaces — ids are unique in practice, so this only
// matters if that ever stops being true.
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
		// Copy up to and including the marker, then decide about the value.
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

// isAllocatedID reports whether digits is the decimal allocID would have
// written for some id below nextID.
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

// renderIRStmt is the IR-driven top-level dispatch. Structural statements
// (PlatformFilter/Slot/user-component inlining) are walked over IR; leaf
// NodeInst rendering currently delegates to the AST helpers via the
// original n.AST back-reference. *ir.If and *ir.For never reach this
// switch in production: const branches/iterables are eliminated by
// internal/optimize, and runtime ones are rewired into __renderSlot by
// passReactivity.
func (g *htmlGen) renderIRStmt(b *strings.Builder, s ir.Stmt, depth int) {
	switch n := s.(type) {
	case *ir.NodeInst:
		g.renderIRNode(b, n, depth)
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
		// A __renderSlotN call marks a reactive if/for slot position. Emit a
		// transparent display:contents anchor here; the slot's content is
		// appended into this anchor at runtime (see emitSynthesizedSlots).
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
		// Const branches are folded by optimize; runtime ones are rewired
		// into __renderSlot by passReactivity. Recursive components can
		// still surface a literal-cond If here — render both arms.
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
		// passNoContext eliminates these before codegen. Reaching this case
		// indicates a regression in NoContext.
		panic(fmt.Sprintf("html.renderIRStmt: unexpected ContextProvider: %#v", n))
	case *ir.Assign, *ir.LocalVar, *ir.Return, *ir.Emit, *ir.Toggle, *ir.CanvasRedrawStmt:
		// Imperative stmts have no visual rendering at top-level slot expansion.
	default:
		panic(fmt.Sprintf("html.renderIRStmt: unhandled ir.Stmt %T", n))
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
// expression. Namespace-resolved element calls (ns.Foo) leave Call.Func nil and
// Call.Receiver set to the namespace ident — reassemble "ns.Foo" from Receiver
// plus the AST back-reference.
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
	// Non-element stdlib nodes that are not raw HTML tags.
	switch n.Name {
	case "slot":
		// Project caller's children into this position.
		for _, s := range g.irSlotChildren {
			g.renderIRStmt(b, s, depth)
		}
		return
	case "window":
		// Windows are handled at Generate level — skip inline rendering.
		return
	case "timer":
		// Timers carry no visual output; the runtime is wired elsewhere.
		return
	}
	// Bodyless calls promoted from ir.CallStmt carry no Component back-ref;
	// resolve a user component by name. Everything else is a native element.
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
// component (local or imported) as opposed to a stdlib component or a
// platform-resolved raw element (html.div, html.img, ...).
func isUserIRComponent(n *ir.NodeInst) bool {
	if n.Component == nil || n.Component.AST == nil {
		return false
	}
	// A wildcard component answers to every tag name, so the node's name is a
	// tag rather than a reference to this declaration: inlining its (empty)
	// body would render nothing at all.
	if n.Component.Wildcard != "" {
		return false
	}
	if n.Component.Stdlib {
		return false
	}
	return true
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
// Synthesized vars (e.g. __slotN accumulators from passReactivity) are
// excluded; emitScript handles them separately as top-level `let` bindings.
func (g *htmlGen) stateVars() []*ir.Var {
	var out []*ir.Var
	if g.pkg != nil {
		for _, v := range g.pkg.Vars {
			if !v.Synthesized {
				out = append(out, v)
			}
		}
		if main := mainIRComponent(g.pkg); main != nil {
			for _, v := range main.Vars {
				if !v.Synthesized {
					out = append(out, v)
				}
			}
		}
	}
	return out
}

// synthesizedVars returns Synthesized=true vars (e.g. __slotN accumulators
// from passReactivity) from the package and main component. These are
// emitted as top-level `let` bindings in the bootstrap script.
// Deduplicates by name so context vars like __ctx_locale (injected into both
// pkg.Vars and component.Vars by the context lowering pass) are emitted once.
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
		if main := mainIRComponent(g.pkg); main != nil {
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

// synthesizedFuncs returns Synthesized=true funcs (e.g. __renderSlotN funcs
// from passReactivity, _canvasDrawN funcs from passCanvas) from the package,
// main component, and the current window (irWindowFuncs).
func (g *htmlGen) synthesizedFuncs() []*ir.Func {
	var out []*ir.Func
	if g.pkg != nil {
		for _, f := range g.pkg.Funcs {
			if f.Synthesized {
				out = append(out, f)
			}
		}
		if main := mainIRComponent(g.pkg); main != nil {
			for _, f := range main.Funcs {
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

// pkgStructs returns all struct definitions reachable from the main package.
func (g *htmlGen) pkgStructs() []*ir.StructDef {
	if g.pkg == nil {
		return nil
	}
	return g.pkg.Structs
}

// pkgFuncs returns user-defined top-level funcs plus main component funcs.
// pkgFuncs returns user-defined top-level funcs plus main component funcs,
// deduplicated. After passNoInlineComponents + registerNestedMethods,
// component methods land in both pkg.Funcs AND comp.Funcs; without dedupe
// the emitter would double-emit them.
//
// Synthesized funcs (e.g. __renderSlotN from passReactivity) are excluded;
// emitScript routes them through htmlTranslator + WalkLowered separately.
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
	if main := mainIRComponent(g.pkg); main != nil {
		for _, f := range main.Funcs {
			add(f)
		}
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

const maxComponentDepth = 10

// renderRawElementIR renders an IR NodeInst as a raw HTML element. The
// component name (or its local part for qualified names like html.div) is
// used as the tag. Props and children come from IR; a synthesized AST
// node is built locally for helpers that still accept ast.VisualNode
// (writeUserAttrs / exprDeps).
func (g *htmlGen) renderRawElementIR(b *strings.Builder, n *ir.NodeInst, depth int) {
	indent := strings.Repeat("  ", depth)
	// The declaration this element resolved to answers which prop holds the
	// tag, which props are boolean, and which events it has.
	decl := g.elementDecl(n)
	// Extract tag name: "html.div" → "div", "button" → "button"
	tag := n.Name
	if _, local, ok := strings.Cut(tag, "."); ok {
		tag = local
	}
	// The tag the element resolved to: `element` binds the matched name to
	// its `tag` prop, and a call site that wrote one of its own — the way a
	// hyphenated custom element is reached — replaced it there. n.Name is the
	// fallback for a node that resolved to no component at all.
	if t, ok := rawElementTag(decl, n); ok {
		tag = t
	}

	id := ""
	if g.nodeIsReactive(n) || g.preview || g.testMode {
		id = g.nodeID(n)
	}
	// Canvas elements always need an ID to wire up the draw call.
	if n.CanvasDraw != nil && id == "" {
		id = g.nodeID(n)
		if g.idToNode != nil {
			g.idToNode[id] = n
		}
	}
	if n.CanvasDraw != nil {
		g.canvasSetups = append(g.canvasSetups, canvasSetup{id: id, drawFunc: n.CanvasDraw})
		// Register an init-only updater for the initial mount draw.
		// Reactive redraws are handled by CanvasRedrawStmt injected into
		// handler/timer bodies by passCanvasReactivity.
		uname := fmt.Sprintf("$u_%s_canvas", id[1:])
		body := fmt.Sprintf(
			"(function(){const _ctx=%s.getContext(\"2d\");_ctx.clearRect(0,0,%s.width,%s.height);%s(_ctx);})();",
			id, id, id, n.CanvasDraw.Name,
		)
		g.initWrites = append(g.initWrites, updateFunc{
			funcName: uname,
			body:     body,
			initOnly: true,
		})
	}
	style := g.buildCSSStyle(n)

	// Build inline attributes from static props.
	// innerText and innerHTML are rendered as element content, not attributes.
	props := nodeProps(n)
	// The prop the matched tag name binds to names the element; it is not one
	// of its attributes. Which prop that is comes from the element's
	// #[wildcard] mark, not from the name "tag".
	delete(props, tagPropName(decl))
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
				case "innerText", "textContent":
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
			jsVal, requires := g.exprToJSReactiveCollect(expr)
			deps := g.exprDeps(expr)
			uname := fmt.Sprintf("$u_%s_%s", id[1:], name)
			var body string
			// The element's declaration decides how a prop is written, in the
			// initial render and in a handler alike, so the two cannot
			// disagree about one prop.
			if field, ok := domPropForProp(decl, name); ok {
				body = fmt.Sprintf(`%s.%s = %s;`, id, field, jsVal)
			} else {
				body = fmt.Sprintf(`%s.setAttribute(%q, %s);`, id, name, jsVal)
			}
			// An i18n.tr-rooted prop with no state dependencies still
			// varies by locale and must run at least once on initial
			// render; mark such updaters initOnly so OptimizeMutation's
			// empty-deps prune keeps them.
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

	// Wire up events. A raw element's event name is the DOM event name, and
	// every event goes through the same registration: whether the listener
	// takes an event argument is the handler's parameter list to answer, not
	// the event name's, so there is no per-event case to get wrong.
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
	maps.Copy(savedLocals, g.ctx.Locals)
	savedRenames := g.ctx.Renames
	renames := make(map[string]string)
	if savedRenames != nil {
		maps.Copy(renames, savedRenames)
	}

	// Component params: bind call-site prop values (IR) or defaults to
	// uniquely-renamed JS constants so the inlined body references them.
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

	// Component computed (zero-param) funcs: register renames, then emit bodies.
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

	// State vars: promote to parent with unique names, and record their
	// initial values so emitScript can populate the state object.
	// Synthesized context vars (added by passNoContext) may be overridden
	// at the call site by a hidden __ctx_<name> arg in n.Props; if so,
	// the overriding expr supersedes the var's default Init.
	dataRenames := make(map[string]string)
	for _, dv := range comp.Vars {
		uniqueName := dv.Name + suffix
		// Note: promoted/renamed names aren't backed by *ir.Var, so we
		// don't register them in the pointer-keyed DepTracker. The
		// rename layer (dataRenames) handles them at the boundary.
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

	g.ctx.Locals = savedLocals
	g.ctx.Renames = savedRenames
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
	if g.testMode {
		// Bridge computeds onto the state object as zero-arg methods so
		// tests can read `c.<computed>()` (computeds are otherwise emitted
		// as free `<recv>_<name>(state)` functions). Function declarations
		// hoist, so referencing them here before their definition is fine.
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
		// Hoist the closure-private state object onto window so the
		// testagent's newTestComponent() can return a live reference.
		// Tests share one state across the session — no per-test
		// isolation (see codegen/platform/html/testagent_emit.go).
		b.WriteString("if (typeof window !== 'undefined') { window.__sngl_state = state; }\n")
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

	// Struct-merge helpers for opaque spreads (flatten_struct_spread lowering).
	if mf := javascript.EmitMergeFuncs(g.pkg.MergeStructs); mf != "" {
		b.WriteString(mf)
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

	// Synthesized slot accumulators + renderSlot funcs from passReactivity.
	// Emit __slotN as top-level `let` bindings, then route each __renderSlotN
	// body through htmlTranslator + WalkLowered. Initial-render call sites
	// appear as top-level CallStmts in the component body and are emitted
	// here as one JS statement each.
	g.emitSynthesizedSlots(b)
	g.emitCanvasSetups(b)

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

	// Helper functions — only emit if used. Read from g.ctx.Helpers, the
	// unified map (== common.Helpers) that also receives flags written by the
	// JsIRContext path (e.g. setter/slot bodies translated during emitScript).
	if g.ctx.Helpers["String"] {
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

	// Event handlers with static dispatch
	g.emitHandlers(b)

	// Timers
	g.emitTimers(b)

	// Initial sync: one __sngl_init() inlining every updater body and
	// timer-sync call. All updaters registered by html are init-only —
	// NoReactivity inlines per-mutation DOM writes into handler/timer/
	// setter bodies, so no callable $u_*() functions are emitted.
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

}

// emitSynthesizedSlots writes the slot accumulator vars (__slotN), slot
// render funcs (__renderSlotN), per-slot DOM anchor bindings (__slotAnchor_N),
// and the initial render call for every slot. Slot funcs are routed through
// htmlTranslator + WalkLowered so intrinsic statements become DOM mutations in
// JavaScript form.
//
// Each reactive if/for renders into a `<span data-sngl-slot="N"
// style="display:contents">` anchor emitted at its source position. The render
// func's `parent` param is bound to that anchor (see rewriteSlotCallsToAnchors),
// so append (create) and removeChild (teardown) operate on the anchor and
// children appear in-flow. The old `__root = null` sentinel is therefore never
// referenced and is not emitted.
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

	// Bind each slot's anchor and fire its initial render. Driven off the
	// synthesized-func list (not a main.Body scan) so slots nested inside
	// windows/components are initialized too.
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

// emitCanvasSetups emits the _snglColor helper once when any canvas element is
// present. The per-canvas draw call is now handled by reactive updaters
// registered in initWrites (see renderRawElementIR), so no per-canvas IIFE is
// emitted here.
func (g *htmlGen) emitCanvasSetups(b *strings.Builder) {
	if len(g.canvasSetups) == 0 {
		return
	}
	b.WriteString(snglColorHelper)
}

// timerSyncCalls returns a $timer_N_sync() call for every timer whose enabled
// (active) var is in mutated. A mutation of a timer's enabled var must re-sync
// the timer so it starts or stops to match the new state; without this the
// timer only reflects its initial enabled value. Handler bodies write state
// directly (they do not route through $set_<var>, which carries its own sync),
// so the sync is appended here. Dedupes if the same var drives several timers.
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
// overlap with the mutated var set. These are updaters that cannot be inlined
// by NoReactivity (e.g. i18n computed text with reactive deps) and must be
// appended to every handler/timer that touches their deps.
// Canvas redraws are handled by CanvasRedrawStmt injected during lowering, and
// their initWrites entries are always initOnly, so they never appear here.
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
		// When the timer has no controlling Active var, treat as
		// always-on: start unconditionally and never tear down.
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

	for _, u := range g.initWrites {
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
	// Convert platform types → IR types. Pointer-keyed dep sets are
	// synthesized from the html-internal name-keyed maps; iropt only
	// reads *ir.Var.Name, so synthetic placeholders are safe.
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
	// Keep handler body strings keyed by elemID+event (IR uses ast.Node,
	// but HTML has already translated to JS strings).
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

	// Union the helper/import requirements of the SURVIVING updaters into the
	// shared sets the prelude/helper emission reads. Handlers, timers, setters,
	// slots, funcs and init values are always emitted, so they flag g.ctx
	// directly during translation; only updaters are droppable, and their
	// requirements rode through OptimizeMutation on Updater.Requires — so dead
	// ones contribute nothing and merged ones are unioned. No text scanning.
	for _, u := range m.Updaters {
		u.Requires.MergeInto(g.ctx.Helpers, g.ctx.NativeImports)
	}

	// Write back optimized updaters.
	g.initWrites = make([]updateFunc, len(m.Updaters))
	for i, u := range m.Updaters {
		g.initWrites[i] = updateFunc{
			funcName: u.Name,
			body:     u.Body,
			deps:     varSetToNames(u.Deps),
			initOnly: u.InitOnly,
		}
	}

	// Write back optimized handlers, restoring body strings.
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
	for i, u := range g.initWrites {
		body := u.body
		for old, canonical := range renames {
			body = strings.ReplaceAll(body, old, canonical)
		}
		g.initWrites[i].body = body
	}
}

// loweredID reports whether id was assigned by NoReactivity lowering. Such
// ids already have inline mutation updates injected into handler/timer/setter
// bodies; updaters registered for them are init-only.
func loweredID(id string) bool { return strings.HasPrefix(id, "__n") }

// exprUsesI18n reports whether expr (or any sub-expression) is a call to
// an i18n.* intrinsic. Used to keep init-time text updaters that have no
// state-var dependencies but still depend on the active locale.
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
			// Leaf — no i18n call possible.
		default:
			panic(fmt.Sprintf("html.exprUsesI18n: unhandled ir.Expr %T", x))
		}
	}
	walk(expr)
	return found
}

// walkStmtExprs invokes walk on every Expr held by a single Stmt
// (used by exprUsesI18n to descend into lambda/closure bodies).
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
	case *ir.PlatformFilter:
		for _, c := range n.Body {
			walkStmtExprs(c, walk)
		}
	}
}

// addForStmtUpdater adds a list updater for an ir.For statement.

// exprDeps extracts model field dependencies, remapping through dataRenames
// when inside a component scope so deps use promoted field names.
func (g *htmlGen) exprDeps(expr ir.Expr) map[string]bool {
	// ExprDeps now walks IR with *ir.Var pointer identity, so no name-based
	// pre-registration is necessary. Convert pointer keys → names and apply
	// the rename layer for promoted component vars.
	varDeps := g.dt.ExprDeps(g.currentComp, expr)
	names := varSetToNames(varDeps)
	return g.remapMutated(names, g.dataRenames)
}

// varSetToNames converts a pointer-keyed *ir.Var set into a name-keyed set.
func varSetToNames(vs map[*ir.Var]struct{}) map[string]bool {
	out := make(map[string]bool, len(vs))
	for v := range vs {
		out[v.Name] = true
	}
	return out
}

// varRegistry maps stringly-keyed names back to *ir.Var pointers at the
// codegen boundary. Real model vars come from the DepTracker; promoted /
// suffixed names are synthesized on demand. iropt only reads .Name, so
// synthetic placeholders are safe.
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

// scopedJC returns a JsIRContext whose ExprCtx is a clone of g.ctx, carrying
// the live locals, renames, and EventVar so WalkLowered + jc.EvalStmt resolves
// identifiers consistently with the rest of html emission.
func (g *htmlGen) scopedJC() *javascript.JsIRContext {
	c := g.ctx.Clone()
	jc := javascript.NewIRContext(c)
	jc.EventVar = c.EventVar
	jc.EventParam = c.EventParam
	return jc
}

// scopedJCFresh is like scopedJC but gives the context its OWN empty
// Helpers/NativeImports maps instead of sharing g.ctx's. Use it to translate a
// body whose helper/import needs must be captured in isolation (e.g. an updater
// that OptimizeMutation may later drop), so they don't pollute the shared set.
func (g *htmlGen) scopedJCFresh() *javascript.JsIRContext {
	c := g.ctx.Clone()
	c.Helpers = map[string]bool{}
	c.NativeImports = map[string]map[string]bool{}
	jc := javascript.NewIRContext(c)
	jc.EventVar = c.EventVar
	jc.EventParam = c.EventParam
	return jc
}

// translateBlockJC routes an IR block through WalkLowered + htmlTranslator
// + JsIRContext, returning JS statement strings with trailing semicolons.
// Used by handler/timer/setter emission to converge on the new dispatch path.
func (g *htmlGen) translateBlockJC(body []ir.Stmt) []string {
	jc := g.scopedJC()
	tr := g.newHTMLTranslatorWithNodes(jc, g.idToNode)
	// Split off CanvasRedrawStmts: they carry a NodeInst→ID lookup that
	// only htmlGen has, so handle them here rather than in the translator.
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
		// Track __nN refs so the script emits getElementById/querySelector
		// for each one (parity with translateHandlerStmt's loweredRefs side
		// effect).
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

// canvasRedrawLine returns the JS inline redraw for a CanvasRedrawStmt:
// clear the canvas and call the draw function with a fresh context.
func (g *htmlGen) canvasRedrawLine(rs *ir.CanvasRedrawStmt) string {
	// Find the canvas element ID by matching the draw func pointer.
	for _, cs := range g.canvasSetups {
		if cs.drawFunc == rs.DrawFunc {
			id := cs.id
			return "(function(){const _ctx=" + id + ".getContext(\"2d\");" +
				"_ctx.clearRect(0,0," + id + ".width," + id + ".height);" +
				rs.DrawFunc.Name + "(_ctx);})()"
		}
	}
	return "" // draw func not found (shouldn't happen)
}

// collectLoweredRefs walks an IR stmt and registers any IsElementRef Ident
// in g.loweredRefs so the script-prelude emits a `const __nN =
// querySelector(...)` for it. Only `__n*` ids (NoReactivity-pre-assigned)
// flow through the data-sngl-id path; allocator-assigned `$N` ids use the
// existing getElementById emission.
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
			// Lambda/closure bodies walked separately when emitted.
		case *ir.Literal, *ir.ContextRead:
			// Leaf — no element ref.
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
	case *ir.PlatformFilter:
		for _, b := range n.Body {
			g.collectLoweredRefs(b)
		}
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
		// No element refs.
	default:
		panic(fmt.Sprintf("html.collectLoweredRefs: unhandled ir.Stmt %T", n))
	}
}

// addEventHandler registers a handler for one DOM event. The handler's
// function may carry a single event-arg param; what that param stands for is
// the event's declared payload to say (see eventPayloadBase), and whether it
// carries one at all decides the emitted listener's signature. Body emission
// goes through translateBlockJC.
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
	// The parameter is a local; what it stands for is EventParam's job. This
	// used to rename by name as well, which replaced any identifier spelled
	// the same — a lambda parameter sharing the handler's name emitted
	// `e2.target` where the lambda's own value was meant.
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
		// The listener takes `e` exactly when the handler declared the
		// parameter that translation bound to it: with no parameter nothing in
		// the body can resolve to the event, and with one the body reads it.
		hasParam: len(fn.Params) > 0,
		mutated:  mutated,
		isAsync:  ir.BlockHasFuncvarAsyncCall(fn.Block, g.pts()),
	})
}

// addIRTimer collects an IR timer into g.timers, translating the handler
// body through the current scope so state references resolve to the unique
// renamed field names.
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

// CSS building

func (g *htmlGen) buildCSSStyle(n *ir.NodeInst) string {
	if n == nil {
		return ""
	}
	return htmlutil.BuildCSSStyleIR(n.Props)
}

// Expression evaluation helpers

func (g *htmlGen) emitJSFunc(b *strings.Builder, fn *ir.Func) {
	// Skip funcs already emitted in the computed branch (see emitScript).
	if codegen.IsComputed(fn) && len(fn.Block) == 1 {
		if ret, ok := fn.Block[0].(*ir.Return); ok && ret.Value != nil {
			_ = ret
			return
		}
	}
	params := make([]string, len(fn.Params))
	for i, p := range fn.Params {
		// Synthetic `this` receiver on desugared methods → emit as `state`
		// so the param name matches the body's component-self translation.
		if i == 0 && fn.Receiver != "" && p.Receiver {
			params[i] = "state"
		} else {
			params[i] = p.Name
		}
	}
	paramStr := strings.Join(params, ", ")

	// Mangle dotted names for JS: int.sqrt → int_sqrt.
	// For desugared methods, the dotted form lives in fn.Receiver.
	jsName := strings.ReplaceAll(fn.Name, ".", "_")
	if fn.Receiver != "" {
		jsName = fn.Receiver + "_" + fn.Name
	}

	keyword := "function"
	if fn.IsAsync {
		keyword = "async function"
	}

	// Single-return expression body — preserve the one-line form for parity.
	if len(fn.Block) == 1 {
		if ret, ok := fn.Block[0].(*ir.Return); ok && ret.Value != nil {
			jc := g.scopedJC()
			if fn.Receiver != "" {
				jc = jc.WithLocal(ir.ReceiverParam)
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
	// Bind params as locals, then route the whole body through the unified
	// pipeline (WalkLowered + htmlTranslator + JsIRContext) so element-ref
	// writes and intrinsic statements lower identically to handler bodies.
	jc := g.scopedJC()
	if fn.Receiver != "" {
		jc = jc.WithLocal(ir.ReceiverParam)
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

// exprToJSReactiveCollect translates a reactive expression in an ISOLATED
// context, returning the JS plus the helpers/native-imports it requires. Used
// for updater bodies, whose requirements must travel with the updater (so a
// dead updater dropped by OptimizeMutation contributes nothing) rather than
// being flagged on the shared g.ctx. Callers guarantee expr is reactive.
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
				return lit.Raw
			case ir.TypeNull:
				return "null"
			case ir.TypeUnit:
				return fmt.Sprintf("%q", lit.Raw)
			case ir.TypeStruct:
				if ir.StringReprStruct(lit.Type) {
					return fmt.Sprintf("%q", lit.Raw)
				}
			}
		}
		return lit.Raw
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
