package html

import (
	"context"
	"fmt"
	"html"
	"io/fs"
	"iter"
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
	"git.duckfam.us/jonathan/sngl/internal/optimize"
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

// SupportedLangs returns "none" (static-site default) plus any registered
// language whose translator implements codegen.HTTPCompiler. The first entry
// is the default when --lang is omitted.
func (g *Generator) SupportedLangs() []string {
	out := []string{"none"}
	var httpLangs []string
	for _, name := range codegen.Langs() {
		if codegen.ServesRoutes(name) {
			httpLangs = append(httpLangs, name)
		}
	}
	sort.Strings(httpLangs)
	return append(out, httpLangs...)
}

// GeneratesUntranslated says html answers `--lang none` with a static site,
// which the browser runs: there is no interpreter in the way.
func (g *Generator) GeneratesUntranslated() bool { return true }

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
		if err := firstUnrenderedNode(req.Pkg); err != nil {
			return err
		}
	}

	placement := scanPlacement(req.Pkg)
	if placement.err != nil {
		return placement.err
	}

	if req.Lang.LanguageIdentifier() == "none" {
		c := &compilation{ctx: codegen.NewCodegenCtx(req, "html"), frontendNatives: placement.frontend}
		// Static mode has no server to host the route's POST handler.
		if win, ok := backendHandlerWindow(req.Pkg); ok {
			return fmt.Errorf("html: window %q has a server-side handler (calls a non-js: import) but the build target %q has no server — compile with a server language (e.g. --lang go) or wrap the call in html.frontend(...)", win, req.Lang.LanguageIdentifier())
		}
		m, err := c.BuildMutationModel(req, c.ctx.Analysis)
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
			if err := emitTestagentFiles(sink, req.Pkg); err != nil {
				return err
			}
		}
		return nil
	}
	if _, ok := req.Lang.(codegen.HTTPCompiler); ok {
		return g.generateRoutes(req, sink, placement.frontend)
	}
	return fmt.Errorf("html: unsupported lang %q", req.Lang.LanguageIdentifier())
}

// testBootOpen and testBootClose make a test page's program re-runnable, so
// each test starts from a freshly mounted page.
const testBootOpen = `window.__sngl_markup = Array.from(document.body.childNodes)
  .filter((n) => n.nodeName !== "SCRIPT").map((n) => n.cloneNode(true));
window.__sngl_reset = () => {
  for (const n of Array.from(document.body.childNodes)) {
    if (n.nodeName !== "SCRIPT") n.remove();
  }
  const first = document.body.firstChild;
  for (const n of window.__sngl_markup) document.body.insertBefore(n.cloneNode(true), first);
  window.__sngl_boot();
};
window.__sngl_boot = () => {
`

const testBootClose = `};
window.__sngl_boot();
`

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

type compilation struct {
	assetFiles []htmlAssetFile
	windows    []htmlWindowOutput

	// ctx is the compilation's, built once by the caller. Constructing one
	// analyzes the whole package twice over (AnalyzeCommon and the dep
	// tracker), and the static path asked four separate times for the same
	// answer. Nil for a caller that builds a compilation directly, which
	// codegenCtx then serves.
	ctx *codegen.CodegenCtx

	// frontendNatives is the frontend set from Generate's placement scan.
	frontendNatives map[nativeFuncKey]bool

	// routeWindows are the documents route mode rendered, in the order of
	// c.windows, for generateRoutes to take its routes from, and routePages
	// the page of a nav.stack each was written for, nil for a window that
	// holds none.
	routeWindows []*codegen.ViewCtx
	routePages   []*ir.NodeInst
}

// documents is the build's documents, or this package's for a caller that
// ran no build.
func (c *compilation) documents(req *codegen.Request) iter.Seq2[*codegen.Document, error] {
	var fold codegen.Fold
	if req.Fold != nil {
		fold = req.Fold()
	} else {
		fold = optimize.NewFold(req.Pkg, &optimize.Config{
			Platform: "html",
			Language: req.Lang.LanguageIdentifier(),
			Dir:      codegen.OptionString(req.Options, "projectDir"),
		})
	}
	return documentsOf(req.Pkg, fold, !codegen.ServesRoutes(req.Lang.LanguageIdentifier()))
}

// codegenCtx is c.ctx, or a fresh one for a caller that supplied none.
func (c *compilation) codegenCtx(req *codegen.Request) *codegen.CodegenCtx {
	if c.ctx == nil {
		c.ctx = codegen.NewCodegenCtx(req, "html")
	}
	return c.ctx
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
	// constAssets are the consts written once for every page (sharedConst),
	// and constFiles the scripts that carry them.
	constAssets map[*ir.Var]*sharedConstAsset
	constFiles  []htmlAssetFile

	// The rest are the package-derived analysis each window used to rebuild
	// for itself. Each is a function of the package alone and each walks the
	// whole of it -- and a window's body is part of that package, so a site of
	// N pages walked N tree-heavy packages per page.
	//
	// Filled on the first window, like usedComponents above: the slot retarget
	// that runs after it rewrites call arguments and declares nothing, so it
	// changes none of these answers.
	derived      bool
	common       *codegen.CommonAnalysis
	dt           *codegen.DepTracker
	owners       []ir.Owner
	canvasByID   map[string]*canvasutil.Meta
	canvasByNode map[*ir.NodeInst]*canvasutil.Meta
	// canvasDraws is the one set of drawings this package's generation uses.
	// One instance, because the draw funcs are keyed by pointer: a second
	// NewCanvasDraws would build equal funcs that match nothing.
	canvasDraws *codegen.CanvasDraws
	// modelVars is derived from dt on first use rather than beside it: only
	// the mutation model asks for it.
	modelVars map[string]*ir.Var
}

// derivePackage fills all four on first use. One gate rather than a nil check
// per field, because two of them answer nil legitimately -- ir.Owners and
// canvasutil.Collect both do for a nil package -- and a nil check would then
// re-walk on every window for the one case where the walk means nothing.
func (s *windowShared) derivePackage(pkg *ir.Package) {
	if s.derived {
		return
	}
	s.derived = true

	var ao codegen.AnalyzeOpts
	ao.UsedComponents = s.usedComponents
	s.common = codegen.AnalyzeCommonFor(pkg, ao)
	if s.usedComponents == nil {
		s.usedComponents = maps.Clone(s.common.UsedComponents)
	}
	s.dt = codegen.NewDepTrackerFromPkg(pkg)
	s.owners = ir.Owners(pkg)
	s.canvasDraws = codegen.NewCanvasDraws(pkg)
	s.canvasByID, s.canvasByNode = canvasutil.Collect(s.canvasDraws)
}

// analysis hands each window a copy.
//
// Nothing in this platform reads a CommonAnalysis field: codegen.OptimizeMutation
// writes to one -- PruneUnusedComputeds deletes from three of its maps, per
// window -- and html reads the updaters it kept rather than the analysis it
// pruned. So sharing one would produce identical output today, and no fixture
// can be written that says otherwise. The copy is what keeps that from being a
// fact a later emitter has to know before it reads ModelFields and gets a map
// three windows have already pruned.
func (s *windowShared) analysis(pkg *ir.Package) *codegen.CommonAnalysis {
	s.derivePackage(pkg)
	return s.common.Clone()
}

// depTracker is shared rather than copied: html asks it four things --
// ExprDeps, codegen.MutatedFields, a range over ModelVars in newVarRegistry,
// and OptimizeMutation through MutationModel.DepTracker -- and all four read.
func (s *windowShared) depTracker(pkg *ir.Package) *codegen.DepTracker {
	s.derivePackage(pkg)
	return s.dt
}

// ownerList is ir.Owners memoized. A caller filters it per window; the walk
// that produces it is per package.
func (s *windowShared) ownerList(pkg *ir.Package) []ir.Owner {
	s.derivePackage(pkg)
	return s.owners
}

// canvases is canvasutil.Collect memoized. Shared, not copied, for the reason
// depTracker is: the two maps and the Meta behind them are only read.
func (s *windowShared) canvases(pkg *ir.Package) (map[string]*canvasutil.Meta, map[*ir.NodeInst]*canvasutil.Meta) {
	s.derivePackage(pkg)
	return s.canvasByID, s.canvasByNode
}

// drawings is the package's canvases, for the paths that meet a canvas as a
// node rather than as a flattened local.
func (s *windowShared) drawings(pkg *ir.Package) *codegen.CanvasDraws {
	s.derivePackage(pkg)
	return s.canvasDraws
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
	wasmPkgs := collectWASMPackages(req.Pkg, c.frontendNatives, projectFS, projectDir)
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

	ctx := c.codegenCtx(req)

	staticMode := req.Lang.LanguageIdentifier() == "none"
	// One document ahead, so that whether there is a second is known before the
	// first is written.
	next, stop := iter.Pull2(c.documents(req))
	defer stop()
	doc, docErr, ok := next()
	// A package that writes no document -- no surface and no harness root --
	// still emits an empty index.html, so callers can verify codegen
	// succeeded.
	if !ok {
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
	following, followingErr, more := next()
	// One page gains nothing from a second file, and a preview is shown as one
	// document with nowhere to fetch a second from.
	shareConsts := staticMode && !opts.Preview && more
	var mainStmts []ir.Stmt
	seenPaths := map[string]string{}
	seenAssets := map[string]bool{}
	for _, fa := range c.assetFiles {
		seenAssets[fa.name] = true
	}
	for i := 0; ok; i++ {
		if docErr != nil {
			return nil, docErr
		}
		win := documentView(doc, ctx.Harness())
		// What the lowering wrote to a Window's props, spelled the way the page
		// holds them: in the package's funcs, which every document shares and
		// a second pass finds already spelled, and in this document's body,
		// which is a clone of its own.
		respellWindowWrites(req.Pkg, doc.Surface, doc.Body)
		for _, fa := range doc.FileAssets {
			if !seenAssets[fa.OutPath] {
				seenAssets[fa.OutPath] = true
				c.assetFiles = append(c.assetFiles, htmlAssetFile{name: fa.OutPath, bytes: fa.Data})
			}
		}
		// A document is served at the href of the page it is written for; a
		// window holding no stack is the one document a site has, at its root.
		var name string
		var href ir.Expr
		if doc.Page != nil {
			href = pageHref(doc.Page)
		}
		switch {
		case !staticMode:
			// In route mode the language compiler indexes by WindowIdx and
			// ignores file paths, and dynamic /{param} routes are expected.
			name = fmt.Sprintf("window_%d", i)
			c.routeWindows = append(c.routeWindows, win)
			c.routePages = append(c.routePages, doc.Page)
		case href == nil:
			name = "index.html"
		default:
			// A static site serves no pattern: whether this language serves
			// routes is the platform's to know, and a page under a `for` has
			// its href only once the copy is known.
			h, ok := codegen.IRLiteralString(href)
			if !ok || len(extractRouteParams(h)) > 0 {
				at := "a pattern"
				if ok {
					at = h + ", a pattern"
				}
				return nil, fmt.Errorf("%s: page %q is served at %s: a static site writes one document per page and cannot serve one; compile with a server language (e.g. --lang go)", ir.NodePos(doc.Page), doc.Page.ID, at)
			}
			name = pathFromHref(h)
		}
		if staticMode {
			label := "window " + ir.StmtPos(win.Surface).String()
			if doc.Page != nil {
				label = fmt.Sprintf("page %q", doc.Page.ID)
			}
			if prev, dup := seenPaths[name]; dup {
				return nil, fmt.Errorf("html: %s is written to %s, as %s is", label, name, prev)
			}
			seenPaths[name] = label
		}
		gen := newHTMLGenFromCtx(ctx, jsLang, opts, shared)
		gen.wasmLoader = wasmLoaderHTML
		gen.wasmPkgs = wasmPkgs
		gen.stylesheet = stylesheetURL
		gen.shareConsts = shareConsts
		gen.irBodyStmts = win.Body
		// Per document: a drawing is keyed by its node, and the document's nodes
		// are its own clones rather than anything the package holds.
		gen.canvasDraws = codegen.NewCanvasDrawsIn(req.Pkg, win.Body)
		gen.canvasByID, gen.canvasByNode = canvasutil.Collect(gen.canvasDraws)
		gen.irWindow = win.Surface
		gen.page = doc.Page
		if !staticMode && href != nil {
			gen.routePath, _ = codegen.IRLiteralString(href)
		}
		gen.ctx = gen.ctx.ForSurface(win.Surface)
		if s, ok := codegen.IRLiteralString(win.Surface.Prop(windowTitle)); ok {
			gen.title = s
		}
		if s, ok := codegen.IRLiteralString(win.Surface.Prop(windowFavicon)); ok {
			gen.favicon = s
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
		doc, docErr, ok = following, followingErr, more
		if ok {
			following, followingErr, more = next()
		}
	}
	c.assetFiles = append(c.assetFiles, shared.constFiles...)
	return ctx.BuildMutation(mainStmts), nil
}

// The props of html's Window a document reads: what its <title> and its icon
// are. They are the primitive's own, declared in html.sngl.
const (
	windowTitle   = "title"
	windowFavicon = "favicon"
)

// documentView is what a page is generated from: the Window it is written
// from. A harness rendering its root component has none, and takes the vars
// and funcs of the view CodegenCtx.Harness makes of it.
func documentView(doc *codegen.Document, harness *codegen.ViewCtx) *codegen.ViewCtx {
	if doc.Surface != nil || harness == nil {
		return &codegen.ViewCtx{Surface: doc.Surface, Body: doc.Body, Name: surfaceName(doc.Surface)}
	}
	w := *harness
	w.Body = doc.Body
	return &w
}

// surfaceName is what a document's Window is called: its `#id`, which a route
// is named after.
// surfaceName is the document's name: the #id the program wrote on its
// window, and nothing for the one a lowering pass gave it to patch a prop.
func surfaceName(n *ir.NodeInst) string {
	if n == nil || strings.HasPrefix(n.ID, "__n") {
		return ""
	}
	return n.ID
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

	// shareConsts is a static site of more than one page, where each is its
	// own document and a const they read is worth a file of its own
	// (sharesConst).
	shareConsts bool
	noCacheBust bool

	// outDir is what the inline source map's `sources` resolve against. It
	// only makes them a good label: a page with `href="/about"` lands a
	// directory lower, and that name is decided after the map is rendered.
	// The map's sourcesContent is what actually carries the source.
	outDir string

	// currentComp resolves implicit `this` for exprDeps / MutatedFields.
	currentComp *ir.Component

	// rootComp is the component a harness isolated as this document's body
	// (CodegenCtx.RootDecl), and nil for every ordinary build.
	rootComp *ir.Component

	usesI18n bool

	irBodyStmts []ir.Stmt

	// pageSlots are the render slots whose anchor this page wrote. The slot
	// funcs are collected per owner, and a root component spliced into the
	// package body owns every window's, so a page boots only its own.
	pageSlots map[string]bool

	// irWindow is the Window this generator emits a document from, or nil
	// when it is emitting a harness root's body. It carries the route's params
	// cell, which is per document.
	irWindow *ir.NodeInst
	// windowNames is windowIDs' answer, made once.
	windowNames map[string]bool
	// page is the page of a nav.stack this document is written for, nil for a
	// window that holds none.
	page *ir.NodeInst
	// routePath is the pattern route mode serves this document at, which the
	// window's params cell is read from on the client; empty on a static site.
	routePath string

	// snglIDByElem maps a JS element variable ($1) to the id a program wrote
	// on that node (#inc). Only ids a test could name are in it.
	snglIDByElem map[string]string

	// enclosingSnglID is the nearest program-written id around the element
	// being rendered. An override's root takes the call site's id while the
	// handler sits on an element inside it -- a checkbox's `<input>` in its
	// `<label>` -- so that is the id a test names the handler by.
	enclosingSnglID string

	// refToVar maps the name a lowered node op uses for its node to the JS
	// variable the element was emitted as. They coincide for a synthesized
	// `__nN` and differ for every `#id` a program wrote: the lowering leaves
	// that id on the node and names it in the updaters it builds, while the
	// element itself is emitted as `$N`. See htmlGen.nodeID.
	refToVar map[string]string
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
	// loweredLocals are the names a lowered statement *declares* on its way
	// past -- a `let __n1__re0__el = __n1__re0.__root` a rebuild binds, say.
	// They look exactly like a page ref where they are used, so without this
	// the prelude also emitted a querySelector for one and the local was
	// renamed by the collision, leaving the rebuild inserting a node the page
	// had never found.
	loweredLocals map[string]bool

	canvasSetups []canvasSetup
	// usesLoweredCanvas records that some emitted scope drew a canvas, so the
	// page carries the draw helpers even when its own markup holds none.
	usesLoweredCanvas bool
	// canvasByID/canvasByFunc are the flattened canvases of every lowered
	// body, threaded into every translator by newHTMLTranslator.
	canvasByID   map[string]*canvasutil.Meta
	canvasByNode map[*ir.NodeInst]*canvasutil.Meta
	canvasDraws  *codegen.CanvasDraws

	// staticInsts are the factory instances the page builds once, in the order
	// the static renderer met them.
	staticInsts []staticInstance
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
	// snglID is the id a test invokes this handler by: the element's own, or
	// the nearest enclosing one.
	snglID string
	// payloadOnTarget says the payload's fields are the element's own state
	// (eventPayloadBase answered e.target), so an invoker writes them there.
	payloadOnTarget bool
	body            string // JS statements
	// hasParam follows the handler's declared parameter, not the event name,
	// because that is what translation binds `e` from. A declared parameter
	// the body never reads still sets it.
	hasParam bool
	mutated  map[string]bool
	isAsync  bool
}

// newHTMLGen builds the generator for one window. A caller with no compilation
// to share passes a nil shared and gets one of its own, so there is only ever
// one code path below.
func newHTMLGen(pkg *ir.Package, lang codegen.LangTranslator, opts htmlConfig, shared *windowShared) *htmlGen {
	if shared == nil {
		shared = newWindowShared("", nil)
	}
	common := shared.analysis(pkg)

	g := &htmlGen{
		pkg:            pkg,
		lang:           lang,
		CommonAnalysis: common,
		Emission:       codegen.NewEmission(),
		preview:        opts.Preview,
		testMode:       opts.Test,
		minify:         opts.Minify,
		noCacheBust:    opts.NoCacheBust,
		idToNode:       make(map[string]*ir.NodeInst),
		refToVar:       make(map[string]string),
		loweredRefs:    make(map[string]bool),
		loweredLocals:  make(map[string]bool),
		pageSlots:      make(map[string]bool),
		usesI18n:       hasI18nCalls(pkg),
		shared:         shared,
	}

	g.dt = shared.depTracker(pkg)
	// The canvases passDeclarative flattened into a lowered body. A canvas the page
	// renders as markup is an ir.NodeInst and is not among these; what is, is
	// every canvas in a scope emitted as code -- a component factory, a slot
	// renderer -- which is what the translator needs to draw one at all.
	g.canvasByID, g.canvasByNode = shared.canvases(pkg)
	g.canvasDraws = shared.drawings(pkg)
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
	g.rootComp = ctx.RootDecl()
	g.currentComp = g.rootComp
	// Adopt the caller's ExprCtx either way. It carries what the *build* said
	// -- Maps above all -- and newHTMLGen's own is built from the package
	// alone, so with no root component to scope to the generator kept an
	// ExprCtx that had never heard of `--opt maps=true` and the JS translator
	// emitted no position markers at all. Re-cloning replaces newHTMLGen's
	// wiring, so the two maps it seeded are carried across.
	helpers := g.ctx.Helpers
	native := g.ctx.NativeImports
	if main := ctx.RootDecl(); main != nil {
		g.irBodyStmts = main.Body
		g.ctx = ctx.ExprCtx.ForComponent(main)
	} else {
		g.ctx = ctx.ExprCtx.Clone()
	}
	g.ctx.Helpers = helpers
	g.ctx.NativeImports = native
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
			// A stack or a page is no element: a document stands the page's content
			// where the stack was, so neither is an element of any page.
			if n.Record != nil || (n.Component != nil && n.Component.Intrinsic == "html:stack") {
				visitStmts(n.Children)
				return
			}
			if strings.HasPrefix(n.ID, "__n") {
				g.idToNode[n.ID] = n
			} else if n.ID != "" {
				// Allocated here rather than when the element is emitted:
				// a handler is translated as its own node is reached, which
				// may be before the node its updater writes to. The element
				// var has to be known by then or the updater renders against
				// the name the op used, which nothing declares.
				g.nodeID(n)
			}
			// A node the lowering creates later — a `for` body's — gets its
			// id then, so it never reaches idToNode. Every raw element of a
			// package shares one declaration, so keeping the one seen here
			// answers for those too.
			if g.elemDecl == nil && isElement(n.Component) {
				g.elemDecl = n.Component
			}
			for _, rule := range classRules(n) {
				g.AddStyle(rule)
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
		case *ir.SlotInst:
			visitStmts(n.Children)
		case *ir.ErrorBoundary:
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
	// The document's own body, which is a clone of the package's and so
	// reached from nothing above.
	visitStmts(g.irBodyStmts)
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
	visit := retargetSlotCalls
	visitHandlers := func(vars []*ir.Var) {
		for _, v := range vars {
			for _, h := range v.Handlers {
				if h.Func != nil {
					visit(h.Func.Block)
				}
			}
		}
	}
	// Not into a factory: a slot there renders into the instance's own root,
	// which the lowering already named, and a page anchor would point every
	// instance at one node.
	for _, c := range g.pageComponents() {
		visit(c.Body)
		for _, fn := range c.Funcs {
			if fn != nil {
				visit(fn.Block)
			}
		}
		visitHandlers(c.Vars)
	}
	for _, fn := range g.pkg.Funcs {
		if fn != nil {
			visit(fn.Block)
		}
	}
	visitHandlers(g.pkg.Vars)
}

// rewriteDocumentSlotCalls retargets the calls in the page's own document,
// which is cloned from its Window and so is none of what the package-wide
// rewrite reached.
func (g *htmlGen) rewriteDocumentSlotCalls() {
	retargetSlotCalls(g.irBodyStmts)
}

// retargetSlotCalls points every `__renderSlotN(parentRef)` under root at
// the slot's anchor.
//
// ir.Walk rather than a descent of the statements that can hold one,
// because "which statement holds the re-fire" is a question this has been
// wrong about twice. The descent knew a CallStmt and the lambda arguments
// of a CallStmt, so a timer handler was covered while `h = setInterval(func
// () { ... })` -- an ir.Assign -- was not, and the re-fire inside it kept
// the parentRef it was threaded with and removeChild'd from the wrong node.
func retargetSlotCalls(root any) {
	_ = walkThroughCatch(root, func(node ir.Node) error {
		call, ok := node.(*ir.Call)
		if !ok || call.Func == nil {
			return nil
		}
		n := slotIndexFromRenderFunc(call.Func.Name)
		if n == "" || len(call.Args) != 1 {
			return nil
		}
		call.Args[0].Value = &ir.Ident{
			Name:         slotAnchorVar(n),
			Type:         ir.TypDyn,
			IsElementRef: true,
			Synthesized:  true,
		}
		return nil
	})
}

// walkThroughCatch is ir.Walk that also walks the handler of every catch
// block it reaches. ir.Walk leaves that handler alone, being an alias of one
// declared elsewhere; but passBoundaryPassthrough has spliced away the
// boundary that declared it, so the catch block is the only route to it left, and an
// edit the page needs made in every handler would miss this one.
func walkThroughCatch(root any, visit func(ir.Node) error) error {
	return ir.Walk(root, func(node ir.Node) error {
		if ifs, ok := node.(*ir.If); ok && ifs.Catch != nil && ifs.Catch.Func != nil {
			if err := walkThroughCatch(ifs.Catch.Func.Block, visit); err != nil {
				return err
			}
		}
		return visit(node)
	})
}

// nodeID returns n.ID when NoReactivity pre-assigned one, otherwise allocates
// a fresh `$N`, recording it in g.idToNode. A nil n allocates without
// recording, so that id falls through to the JS-default write.
//
// A program-written `#id` is not reused as the variable name: it would have to
// survive JS scoping and could name a reserved word or a global the page
// already emits. It is recorded in refToVar instead, because the lowering left
// that id on the node and every updater it built names it -- an op arriving
// here under that name has to reach the element emitted under this one.
func (g *htmlGen) nodeID(n *ir.NodeInst) string {
	var id string
	switch {
	case n != nil && strings.HasPrefix(n.ID, "__n"):
		id = n.ID
	case n != nil && n.ID != "" && g.refToVar[n.ID] != "":
		// The prewalk already allocated for this ref so that a handler
		// translated before the element is emitted can still resolve it.
		id = g.refToVar[n.ID]
	default:
		id = g.allocID()
	}
	if n != nil {
		g.idToNode[id] = n
		if n.ID != "" && n.ID != id {
			g.refToVar[n.ID] = id
			// Under the op's own name too: OnPropAssign recovers the
			// declaration by the name the op used, before resolving the
			// variable.
			g.idToNode[n.ID] = n
		}
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
	g.rewriteDocumentSlotCalls()

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
		// A window shown inside the document is a `<dialog>` with the
		// browser's own look, which the reset above takes the margin that
		// centres it and the padding of: given them back.
		if g.showsDialog() {
			defaultCSS += "dialog { margin: auto; padding: 1em; }\n"
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

	// Ahead of the stylesheet: an element the script builds registers its
	// class's rule as it is translated.
	var scriptBuf strings.Builder
	g.emitScript(&scriptBuf)
	if script := scriptBuf.String(); strings.Contains(script, navPageMarker) {
		decl := ""
		if strings.Contains(script, "globalThis."+navPageGlobal) {
			decl = fmt.Sprintf("globalThis.%s = %s;\n", navPageGlobal, g.literalToJS(g.page.Record))
		}
		scriptBuf.Reset()
		scriptBuf.WriteString(strings.Replace(script, navPageMarker, decl, 1))
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

	sharedTags, script := g.linkSharedConsts(pruneDecls(scriptBuf.String()))

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
		b.WriteString("\n")
		b.WriteString(sharedTags)
		b.WriteString("<script>\n")
		if g.testMode {
			b.WriteString(testBootOpen)
			b.WriteString(script)
			b.WriteString(testBootClose)
		} else {
			b.WriteString(script)
		}
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
		panic(fmt.Sprintf("html.renderIRStmt: slot %q reached the emitter unsubstituted", n.Name))
	case *ir.CallStmt:
		// A __renderSlotN call marks a reactive if/for slot position; its
		// content is appended into this anchor at runtime.
		if n.Call != nil && n.Call.Func != nil {
			if idx := slotIndexFromRenderFunc(n.Call.Func.Name); idx != "" {
				g.pageSlots[idx] = true
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
	// A call to something with a body is a call, not an element. An element
	// resolves to a declaration carrying none, so without this test a lowering
	// that puts a call in a body -- an effect's setup does -- was rendered as
	// markup: `<__effect0_mount></__effect0_mount>` in the page, and the call
	// itself nowhere.
	if fn := n.Call.Func; fn != nil && len(fn.Block) > 0 {
		return nil
	}
	// A node operation is bodyless too, and is an instruction rather than a
	// tag: `<AppendChild>` in the page is what promoting one looks like.
	if ir.IsNodeOpCall(n.Call) {
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
// component reaching here is a runtime instance, every other one having been
// inlined by the lowering, and anything else is rendered as an element.
func (g *htmlGen) renderIRNode(b *strings.Builder, n *ir.NodeInst, depth int) {
	if isUserIRComponent(n) {
		// A factory component is never inlined here, at a static position any
		// more than inside a slot: its body has been flattened for the
		// factory and is no longer a tree to render.
		if g.isInstanceComponent(n.Component) {
			g.renderStaticInstance(b, n, depth)
			return
		}
		panic(fmt.Sprintf("html.renderIRNode: component %s reached the emitter neither inlined nor an instance", n.Component.Name))
	}
	if isWindowPrimitive(n) {
		g.renderDialog(b, n, depth)
		return
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

// stateVars returns pkg.Vars merged with a harness root's Vars.
// Synthesized vars are excluded; emitScript emits them as top-level `let`.
// stateVars is the state in scope for the document this generator emits: the
// package's, the root component's, and this window's. The other components'
// are not -- html emits one document per window and inlines the rest into it,
// so a child component's var reaches `state` through the inliner's rename, not
// through this list.
//
// Synthesized vars are excluded; emitScript emits them as top-level `let`.
func (g *htmlGen) stateVars() []codegen.OwnedVar {
	var out []codegen.OwnedVar
	// The route's per-request input, which is this document's and no owner's:
	// the page reads it as state because that is what it is to a document --
	// a cell filled in before anything renders, from the path the browser
	// asked for (routeParamsJS). It is the slot population's own *ir.Param,
	// or a page's, which is why this list is symbols: nothing in the program
	// declares it, so there is no Var to be had.
	if g.irWindow != nil && g.irWindow.Params != nil {
		out = append(out, codegen.OwnedVar{Sym: g.irWindow.Params})
	}
	for _, o := range g.shared.ownerList(g.pkg) {
		if o.Comp != nil && o.Comp != g.rootComp {
			continue
		}
		for _, v := range o.Vars {
			if !v.Synthesized {
				out = append(out, codegen.OwnedVar{Sym: v, Comp: o.Comp})
			}
		}
	}
	return out
}

// synthesizedVars returns the Synthesized vars of the package, the root
// component, deduplicated by name: the context lowering pass
// injects a var like __ctx_locale into both pkg.Vars and component.Vars.
func (g *htmlGen) synthesizedVars() []*ir.Var {
	var out []*ir.Var
	seen := make(map[string]bool)
	add := func(vars []*ir.Var) {
		for _, v := range vars {
			if v != nil && v.Synthesized && !seen[v.Name] {
				seen[v.Name] = true
				out = append(out, v)
			}
		}
	}
	if g.pkg == nil {
		return nil
	}
	add(g.pkg.Vars)
	if main := g.rootComp; main != nil {
		add(main.Vars)
	}
	return out
}

// synthesizedFuncs returns the Synthesized funcs of the package and of every
// component the page renders.
//
// Deduped by pointer, as pkgFuncs is and for the same reason: a component's
// nested method is on pkg.Funcs and on the component's Funcs both. Both
// consumers of this list matter -- one writes the definition, the other the
// anchor lookup and the bootstrap call -- so a duplicate was a __renderSlotN
// defined twice and run twice at startup, the second run removing the nodes
// the first had just built.
func (g *htmlGen) synthesizedFuncs() []*ir.Func {
	var out []*ir.Func
	seen := map[*ir.Func]bool{}
	add := func(f *ir.Func) {
		if f == nil || !f.Synthesized || seen[f] {
			return
		}
		seen[f] = true
		out = append(out, f)
	}
	if g.pkg != nil {
		for _, f := range g.pkg.Funcs {
			add(f)
		}
		// Every component the build renders, not only the root: one that
		// survived inlining is emitted from its own declaration, and its
		// synthesized funcs -- a canvas draw function among them -- have to
		// come with it.
		for _, comp := range g.pageComponents() {
			for _, f := range comp.Funcs {
				add(f)
			}
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
			if sf.Value == nil {
				continue
			}
			// `flex` is a float, so the question is numeric. An expression
			// nobody can read at build time may ask for a share at runtime,
			// and handing over the viewport costs a page that does not.
			if v, ok := codegen.IRLiteralNumber(sf.Value); ok && v == 0 {
				continue
			}
			return true
		}
	}
	return false
}

// showsDialog reports whether the page holds a `<dialog>`, written in its
// markup or built by its script.
func (g *htmlGen) showsDialog() bool {
	found := false
	// A Window in the document's body is a dialog; one in the package is
	// either that dialog's original or the document's own, so it is the
	// body's alone that counts.
	inBody := true
	visit := func(root any) {
		_ = walkThroughCatch(root, func(n ir.Node) error {
			switch x := n.(type) {
			case *ir.NodeInst:
				if isElement(x.Component) && (x.Name == "dialog" || strings.HasSuffix(x.Name, ".dialog")) || inBody && isWindowPrimitive(x) {
					found = true
				}
			case *ir.Call:
				if x.Func != nil && x.Func.Name == ir.NodeOpCreateNode && len(x.Args) > 0 {
					if tag, ok := codegen.IRLiteralString(x.Args[0].Value); ok && (tag == "dialog" || tag == windowElement) {
						found = true
					}
				}
			}
			if found {
				return ir.SkipAll
			}
			return nil
		})
	}
	visit(g.irBodyStmts)
	if !found && g.pkg != nil {
		inBody = false
		visit(g.pkg)
	}
	return found
}

func (g *htmlGen) pkgStructs() []*ir.StructDef {
	if g.pkg == nil {
		return nil
	}
	return g.pkg.Structs
}

// pkgFuncs returns user-defined top-level funcs plus the harness root's funcs.
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
		if f.Synthesized || !g.pageOwnsFunc(f) {
			return
		}
		// A native declaration is not emitted: the identifier already exists,
		// and a call becomes a call to it. Emitted anyway, the bodyless
		// signature came out as `function setInterval(f, ms) { return 0; }`,
		// which shadows the global it names -- a stub that compiles, runs, and
		// schedules nothing.
		if f.Foreign.Name != "" && !f.Foreign.Marked {
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

// preservesWhitespace reports whether a raw HTML tag renders whitespace in its
// content literally, so the pretty-printer must not indent inside it.
func preservesWhitespace(tag string) bool {
	switch tag {
	case "pre", "textarea":
		return true
	}
	return false
}

// escapeTextContent writes a text node. One that is all whitespace spells its
// line breaks as references, or stripInterTagWhitespace cannot tell it from
// the pretty-printer's run between two tags.
func escapeTextContent(s string) string {
	out := html.EscapeString(s)
	if strings.TrimSpace(s) == "" {
		out = strings.NewReplacer("\n", "&#10;", "\r", "&#13;").Replace(out)
	}
	return out
}

// preservesWhitespaceNode is the same question asked of an element that says
// so in its style rather than by being one of the two tags that always do.
//
// A flow of rich text is the case: its text is literal on every target, which
// on the web means `white-space: pre-wrap`, and the pretty-printer's own
// newline and indentation are inside the element and so are part of it. A
// `<p>` is not whitespace-sensitive in general, so the tag cannot answer -- the
// style has to.
func (g *htmlGen) preservesWhitespaceNode(n *ir.NodeInst, tag string) bool {
	return preservesWhitespace(tag) || strings.Contains(g.buildCSSStyle(n), "white-space:pre")
}

// stripInterTagWhitespace collapses whitespace between adjacent tags, so a
// <pre>'s children do not carry the pretty-printer's indentation into the
// output. Whitespace inside text nodes is preserved.
//
// Only a run holding a newline is the pretty-printer's: that is what it always
// writes between two elements. Stripping every run instead ate the space
// between two words of a code sample -- `<span> </span>` came out
// `<span></span>` -- which is precisely the character a flow of rich text
// promises to keep. A text node that is whitespace *with* a newline, a code
// sample's line break and indentation, is kept by escapeTextContent.
func stripInterTagWhitespace(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	n := len(s)
	i := 0
	for i < n {
		c := s[i]
		if c == '>' {
			b.WriteByte('>')
			j, nl := i+1, false
			for j < n && (s[j] == ' ' || s[j] == '\t' || s[j] == '\n' || s[j] == '\r') {
				nl = nl || s[j] == '\n' || s[j] == '\r'
				j++
			}
			if nl && j < n && s[j] == '<' {
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
	return strings.TrimRight(out, "\t\n\r")
}

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
	if n.ID != "" && !strings.HasPrefix(n.ID, "__n") {
		saved := g.enclosingSnglID
		g.enclosingSnglID = n.ID
		defer func() { g.enclosingSnglID = saved }()
	}
	drawing := g.canvasDraws.ForNode(n)
	if drawing != nil && id == "" {
		id = g.nodeID(n)
		if g.idToNode != nil {
			g.idToNode[id] = n
		}
	}
	if drawing != nil {
		cw, ch := canvasIntProp(n, "width"), canvasIntProp(n, "height")
		cs := canvasSetup{id: id, node: n, drawName: drawing.Name, draw: drawing.Draw, w: cw, h: ch, scaling: canvasScalingMode(n)}
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
	style := nodeInlineCSS(n)
	if css := canvasScalingCSS(n, drawing != nil); css != "" {
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
		// Read into the element's CSS by canvasScalingCSS and spanStyleCSS,
		// and not an attribute any element has.
		if name == "scalingMode" || name == spanStyleProp || name == classStyleProp || name == classStyleDarkProp {
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
	for _, rule := range classRules(n) {
		g.AddStyle(rule)
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
			b.WriteString(escapeTextContent(staticInnerText))
			fmt.Fprintf(b, "</%s>\n", tag)
		} else if g.preservesWhitespaceNode(n, tag) {
			// A whitespace-sensitive tag renders its children inline: the
			// pretty-printer's newlines would be visible in the output.
			var sub strings.Builder
			for _, s := range ir.WidgetChildren(n) {
				g.renderIRStmt(&sub, s, 0)
			}
			b.WriteString(stripInterTagWhitespace(sub.String()))
			fmt.Fprintf(b, "</%s>\n", tag)
		} else {
			b.WriteString("\n")
			for _, s := range ir.WidgetChildren(n) {
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
		if name == "style" || name == classStyleProp || name == classStyleDarkProp {
			continue
		}
		if name == spanStyleProp {
			g.spanColorWrite(id, expr, lowered)
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
			// prune so it runs on initial render. So does one calling a func the
			// build cannot evaluate: `deeper(0)` where deeper writes state reads
			// nothing, and pruned it was never written at all.
			initOnly := lowered || (len(deps) == 0 && (exprUsesI18n(expr) || callsUnfoldable(expr)))
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

func (g *htmlGen) emitScript(b *strings.Builder) {
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

	// The page this document is written for, which is what a stack shows
	// here: `pages.current` read where no document's fold reaches -- a
	// function's body, a test's -- is this (emitNavCurrent).
	// Written in place once the rest of the script is, and only if the rest
	// reads it.
	if g.page != nil && g.page.Record != nil {
		b.WriteString(navPageMarker)
	}

	// Ahead of the state, whose initializers may read one.
	consts := g.pkgConsts()
	for _, c := range consts {
		if g.sharesConst(c) {
			continue
		}
		fmt.Fprintf(b, "const %s = %s;\n", c.Name, g.literalToJS(c.Init))
	}
	if len(consts) > 0 {
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
		val := g.literalToJS(dv.Init())
		if g.irWindow != nil && dv.Sym == ir.Symbol(g.irWindow.Params) {
			if read := routeParamsJS(g.routePath, g.irWindow.Params.Type, val); read != "" {
				stateFields = append(stateFields, dv.Name()+": null")
				deferredInits = append(deferredInits, struct{ name, value string }{dv.Name(), read})
				continue
			}
		}
		if codegen.IRIsLiteral(dv.Init()) {
			stateFields = append(stateFields, dv.Name()+": "+val)
		} else {
			// Seeded so the object shape is correct for code that walks the
			// keys before init completes.
			stateFields = append(stateFields, dv.Name()+": null")
			deferredInits = append(deferredInits, struct{ name, value string }{dv.Name(), val})
		}
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
				fmt.Fprintf(b, "state.%s = () => %s();\n", fn.Name, fn.Name)
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
			param := javascript.SafeIdent(f.Name)
			params = append(params, param)
			if param == f.Name {
				body = append(body, f.Name)
			} else {
				body = append(body, f.Name+": "+param)
			}
		}
		// A payload struct is declared for the targets that keep a handler's
		// parameter, and a page reads a DOM event instead: its constructor is
		// one nothing calls.
		openDecl(b, sd.Name)
		fmt.Fprintf(b, "function %s(%s) { return {%s}; }\n",
			sd.Name, strings.Join(params, ", "), strings.Join(body, ", "))
		closeDecl(b)
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
			// Its own name, not a `$`-prefixed one: `$` is this platform's
			// namespace for a helper it synthesized ($set_, $timer_, $compute_)
			// and a computed is the program's own func. A block-bodied computed
			// has always come out under its plain name from emitJSFunc, and
			// every call site spells that -- so the prefix here was a
			// declaration nothing called beside a call to nothing declared.
			fmt.Fprintf(b, "function %s() { return %s; }\n", fn.Name, body)
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

	// Before the slots: a slot body is where a component instance is created,
	// so the factory it calls has to be in scope by then.
	g.emitComponentFactories(b)
	g.emitSynthesizedSlots(b)
	g.emitCanvasSetups(b)

	for _, dv := range stateVars {
		v := dv.Var()
		if v == nil {
			// A route's parameter has no setter: nothing in the page assigns
			// it, and a @change is something a declaration carries.
			continue
		}
		needsSetter := g.preview
		for _, h := range v.Handlers {
			if h.Name == "change" {
				needsSetter = true
				break
			}
		}
		if !needsSetter && g.pkg != nil {
			for _, k := range g.pkg.AsyncKickers {
				if slices.Contains(k.Deps, v.Name) {
					needsSetter = true
					break
				}
			}
		}
		if !needsSetter {
			continue
		}
		g.emitSetter(b, v)
	}
	if len(stateFields) > 0 {
		b.WriteString("\n")
	}

	b.WriteString("\n")

	// g.ctx.Helpers is the unified map, so it also carries flags written by
	// the JsIRContext path during emitScript.
	if g.ctx.Helpers["String"] {
		b.WriteString("function String(v) { return \"\" + v; }\n\n")
	}
	if g.ctx.Helpers[spanColorHelper] {
		b.WriteString(spanColorHelperJS + "\n")
	}
	if g.ctx.Helpers[dialogHelper] {
		b.WriteString(dialogHelperJS + "\n")
	}

	// Before the refs below, which is where a handler's binding for the
	// instance would otherwise be written as an element lookup.
	g.emitStaticInstances(b)

	// An id can appear in both refs and loweredRefs; prefer the querySelector
	// form, since emitting both is a duplicate `const __nN` that throws at
	// parse time.
	refs := g.collectReferencedIDs()
	for _, id := range refs {
		if _, lowered := g.loweredRefs[id]; lowered {
			continue
		}
		if g.isStaticInstanceID(id) {
			continue
		}
		fmt.Fprintf(b, "const %s = document.getElementById(\"%s\");\n", id, id)
	}
	loweredRefs := make([]string, 0, len(g.loweredRefs))
	for id := range g.loweredRefs {
		if g.isStaticInstanceID(id) || g.loweredLocals[id] {
			continue
		}
		loweredRefs = append(loweredRefs, id)
	}
	sort.Strings(loweredRefs)
	for _, id := range loweredRefs {
		fmt.Fprintf(b, "const %s = document.querySelector('[data-sngl-id=%q]');\n", id, id)
	}
	if len(refs) > 0 || len(loweredRefs) > 0 {
		b.WriteString("\n")
	}

	g.emitSlotBoots(b)

	g.emitHandlers(b)

	// Every html updater is init-only: NoReactivity inlines the per-mutation
	// DOM writes into handler bodies, so no callable $u_*() is emitted.
	if len(g.initWrites) > 0 {
		b.WriteString("\nfunction __sngl_init() {\n")
		for _, u := range g.initWrites {
			fmt.Fprintf(b, "  %s\n", u.body)
		}
		b.WriteString("}\n__sngl_init();\n")
	}

	// After the DOM updaters are wired, for the same reason a kicker is: a
	// mount body writes state, and the write patches whatever reads it.
	for _, fn := range g.bodyCalls() {
		fmt.Fprintf(b, "%s();\n", fn)
	}

	// After the DOM updaters are wired, so a kicker body can call setters.
	if g.pkg != nil && len(g.pkg.AsyncKickers) > 0 {
		b.WriteString("\n// Async kicker startup\n")
		for _, k := range g.pkg.AsyncKickers {
			fmt.Fprintf(b, "%s();\n", k.Func.Name)
		}
	}

	// Only the page's own teardown. An instance's brackets are released by the
	// __destroy the factory returns, called wherever that instance is torn
	// down; naming that function here would name a factory local.
	if g.pkg != nil && g.pkg.Teardown != nil && g.pageOwnsFunc(g.pkg.Teardown) {
		// pagehide, not beforeunload or unload: it is the one the browsers
		// still fire for a page entering the back/forward cache, and the two
		// older events are precisely the ones that do not run on a mobile tab
		// discard. Even so this is best effort -- a crashed tab runs nothing --
		// which is what an effect's teardown is written knowing.
		fmt.Fprintf(b, "\nwindow.addEventListener(\"pagehide\", %s);\n", g.pkg.Teardown.Name)
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
	// The drawings count: they are emitted below and are in no func list, so a
	// page whose only synthesized code is a canvas would return here and write
	// none of it.
	if len(synthVars) == 0 && len(synthFuncs) == 0 && len(g.canvasDraws.All()) == 0 {
		return
	}
	jc := javascript.NewIRContext(g.ctx)

	emittedVar := false
	for _, v := range synthVars {
		// __root is a dead sentinel: slots render into their own anchors now.
		if v.Name == "__root" {
			continue
		}
		openDecl(b, v.Name)
		b.WriteString("let " + v.Name + " = ")
		if v.Init != nil {
			b.WriteString(jc.EvalExpr(v.Init))
		} else {
			b.WriteString("null")
		}
		b.WriteString(";\n")
		closeDecl(b)
		emittedVar = true
	}
	if emittedVar {
		b.WriteString("\n")
	}

	// A drawing is wrapped once and called from the canvas setup and from
	// every repaint, so the statements the tree carries become a function
	// here. It is this platform's, built at emission: no func list holds one
	// and nothing in the IR names it.
	for _, cv := range g.canvasDraws.All() {
		// A drawing owned by a component the page builds as a factory is
		// declared inside that factory, where the elements it paints are in
		// scope. See emitFactory.
		if cv.Owner != nil && g.isInstanceComponent(cv.Owner) {
			continue
		}
		tr := g.newHTMLTranslator(jc)
		drawn := &ir.Func{
			Name:   cv.Name,
			Params: []*ir.Param{{Name: "ctx", Type: ir.TypDyn}},
			Block:  codegen.WalkLowered(context.Background(), cv.Draw, tr),
		}
		for _, line := range jc.EmitFuncDef(drawn) {
			b.WriteString(line)
			b.WriteByte('\n')
		}
		b.WriteByte('\n')
	}

	for _, fn := range synthFuncs {
		tr := g.newHTMLTranslator(jc)
		body := codegen.WalkLowered(context.Background(), fn.Block, tr)
		synthesized := &ir.Func{
			Name:   fn.Name,
			Params: fn.Params,
			Block:  body,
			// The rebuilt func stands in for fn at emission, so it has to carry
			// fn's colour: a call site writes `await` off the callee's flag,
			// and a definition that lost it is an await in a sync function.
			IsAsync: fn.IsAsync,
		}
		if fn.Receiver == "" {
			openDecl(b, fn.Name)
		}
		for _, line := range jc.EmitFuncDef(synthesized) {
			b.WriteString(line)
			b.WriteByte('\n')
		}
		b.WriteByte('\n')
		if fn.Receiver == "" {
			closeDecl(b)
		}
	}

}

// emitSlotBoots renders each of the page's slots into its anchor. After the
// page's element references, because a slot builds its component instances
// as it renders, and one whose effect mounts writes the page's own nodes:
// booted first, it wrote to a reference the page had not bound yet.
//
// Driven off the synthesized-func list rather than a main.Body scan, so
// slots nested inside windows/components are initialized too.
func (g *htmlGen) emitSlotBoots(b *strings.Builder) {
	for _, fn := range g.synthesizedFuncs() {
		idx := slotIndexFromRenderFunc(fn.Name)
		if idx == "" || !g.pageSlots[idx] {
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
func canvasScalingCSS(n *ir.NodeInst, isCanvas bool) string {
	if n == nil || !isCanvas {
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

// emitCanvasSetups emits the canvas helpers once when any canvas element is
// present. The per-canvas draw call comes from the updaters registered in
// initWrites, so no per-canvas IIFE is emitted here.
func (g *htmlGen) emitCanvasSetups(b *strings.Builder) {
	if len(g.canvasSetups) == 0 && !g.usesLoweredCanvas {
		return
	}
	if g.ctx.Helpers["native:_snglDrawImage"] {
		b.WriteString(snglDrawImageHelper)
	}
	b.WriteString(snglCanvasHelper)
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
			hjc := codegen.BindVarHandlerValue(g.scopedJC(), h, "v")
			for _, line := range g.translateBlockWithJC(hjc, h.Func.Block) {
				fmt.Fprintf(b, "  %s\n", line)
			}
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
	varReg := newVarRegistry(g.shared.modelVarsByName(g.pkg))
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
	byKey := make(map[string]eventHandler)
	handlers := make([]codegen.Handler, len(g.handlers))
	for i, h := range g.handlers {
		byKey[h.elemID+":"+h.event] = h
		handlers[i] = codegen.Handler{
			NodeID:  h.elemID,
			Event:   h.event,
			Mutated: varReg.namesToVarSet(h.mutated),
		}
	}
	m := &codegen.MutationModel{
		Analysis:   g.CommonAnalysis,
		DepTracker: g.dt,
		Updaters:   updaters,
		Handlers:   handlers,
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
		orig := byKey[h.NodeID+":"+h.Event]
		orig.elemID = h.NodeID
		orig.event = h.Event
		orig.mutated = varSetToNames(h.Mutated)
		g.handlers[i] = orig
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

func (g *htmlGen) exprDeps(expr ir.Expr) map[string]bool {
	return varSetToNames(g.dt.ExprDeps(g.currentComp, expr))
}

func varSetToNames(vs map[ir.Symbol]struct{}) map[string]bool {
	out := make(map[string]bool, len(vs))
	for v := range vs {
		out[v.SymName()] = true
	}
	return out
}

// varRegistry maps names back to *ir.Var pointers at the codegen boundary.
// A promoted or suffixed name is synthesized on demand; iropt only reads
// .Name, so a synthetic placeholder is safe.
//
// Two maps because the halves have two lifetimes. base is the package's model
// vars, which no window changes -- built per window, it was the package's var
// table rebuilt once per page. own is what this window synthesized, which is
// the half that must not reach another: a placeholder stands for a name no
// declaration owns, so two windows naming one get one each, as they did when
// the whole map was per window.
type varRegistry struct {
	base map[string]*ir.Var
	own  map[string]*ir.Var
}

func newVarRegistry(base map[string]*ir.Var) *varRegistry {
	return &varRegistry{base: base, own: map[string]*ir.Var{}}
}

// modelVarsByName is base, derived from the dep tracker once per compilation.
//
// A name two owners both declare resolves to whichever the map iteration
// reached last, which is what it did per window before -- shared, that choice
// is at least made once rather than redrawn per page.
func (s *windowShared) modelVarsByName(pkg *ir.Package) map[string]*ir.Var {
	s.derivePackage(pkg)
	if s.modelVars == nil {
		s.modelVars = make(map[string]*ir.Var, len(s.dt.ModelVars))
		for sym := range s.dt.ModelVars {
			if v, ok := sym.(*ir.Var); ok {
				s.modelVars[v.Name] = v
			}
		}
	}
	return s.modelVars
}

func (r *varRegistry) lookup(name string) *ir.Var {
	if v, ok := r.own[name]; ok {
		return v
	}
	if v, ok := r.base[name]; ok {
		return v
	}
	v := &ir.Var{Name: name}
	r.own[name] = v
	return v
}

func (r *varRegistry) namesToVarSet(names map[string]bool) map[ir.Symbol]struct{} {
	if len(names) == 0 {
		return nil
	}
	out := make(map[ir.Symbol]struct{}, len(names))
	for n, ok := range names {
		if !ok {
			continue
		}
		out[r.lookup(n)] = struct{}{}
	}
	return out
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
	return g.translateBlockWithJC(g.scopedJC(), body)
}

// translateBlockWithJC is translateBlockJC over a caller-supplied scope, for a
// body emitted inside a binding the block itself does not declare.
func (g *htmlGen) translateBlockWithJC(jc *javascript.JsIRContext, body []ir.Stmt) []string {
	tr := g.newHTMLTranslator(jc)
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

// pageCanvas is the canvas the page rendered for n, as the draw call names
// it, or nil.
func (g *htmlGen) pageCanvas(n *ir.NodeInst) *canvasutil.Meta {
	for _, cs := range g.canvasSetups {
		if cs.node == n {
			return &canvasutil.Meta{ID: cs.id, Node: cs.node, DrawName: cs.drawName, Width: cs.w, Height: cs.h, Scaling: cs.scaling}
		}
	}
	return nil
}

func (g *htmlGen) canvasRedrawLine(rs *ir.CanvasRedrawStmt) string {
	for _, cs := range g.canvasSetups {
		if cs.node == rs.Canvas {
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
		g.loweredLocals[n.Name] = true
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
	base := eventPayloadBase(decl, event)
	g.ctx.EventVar = base
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
			mutated[v.SymName()] = true
		}
	}
	g.ctx.EventVar = savedEvent
	g.ctx.EventParam = savedEventParam
	for _, n := range savedLocal {
		delete(g.ctx.Locals, n)
	}
	g.handlers = append(g.handlers, eventHandler{
		elemID:          elemID,
		event:           event,
		snglID:          g.enclosingSnglID,
		payloadOnTarget: base == "e.target",
		body:            strings.Join(lines, "\n  "),
		// With no declared parameter nothing in the body can resolve to the
		// event, so the listener takes none either.
		hasParam: len(fn.Params) > 0,
		mutated:  mutated,
		isAsync:  ir.BlockHasFuncvarAsyncCall(fn.Block, g.pts()),
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
	tr := g.newHTMLTranslator(jc)
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
				// Shared with the JS lang translator: a unit value is its
				// magnitude per base. The CSS spelling is applied where the
				// value reaches a style property, not here.
				return javascript.UnitLiteral(lit)
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
//
// A payload a test passes is written where the handler reads it: onto the
// element when its fields are the element's state (`checked`, `value`), which
// is what a user's input would have changed before the event fired, and onto
// the event otherwise.
func (g *htmlGen) emitEventInvokers(b *strings.Builder) {
	seen := map[string]bool{}
	emit := func(h eventHandler, snglID string) {
		if snglID == "" {
			return
		}
		name := snglID + capitalizeFirst(h.event)
		if seen[name] {
			return
		}
		seen[name] = true
		switch {
		case h.event == "click":
			fmt.Fprintf(b, "state.%s = () => %s.click();\n", name, h.elemID)
		case h.payloadOnTarget:
			fmt.Fprintf(b, "state.%s = (p) => { Object.assign(%s, p); %s.dispatchEvent(new Event(%q)); };\n", name, h.elemID, h.elemID, h.event)
		default:
			fmt.Fprintf(b, "state.%s = (p) => %s.dispatchEvent(Object.assign(new Event(%q), p));\n", name, h.elemID, h.event)
		}
	}
	// An element's own id outranks one it inherits, whichever was rendered
	// first.
	for _, h := range g.handlers {
		emit(h, g.snglIDByElem[h.elemID])
	}
	for _, h := range g.handlers {
		emit(h, h.snglID)
	}
	// An element built at run time -- inside a component instance or a
	// render slot -- does not exist when the page starts, so its invoker
	// finds it when it is called, by the mark OnCreateNode gives it.
	for _, h := range g.runtimeHandlers() {
		name := h.id + capitalizeFirst(h.event)
		if seen[name] {
			continue
		}
		seen[name] = true
		lookup := fmt.Sprintf("document.querySelector('[data-sngl-id=%q]')", h.id)
		if h.event == "click" {
			fmt.Fprintf(b, "state.%s = () => %s.click();\n", name, lookup)
			continue
		}
		fmt.Fprintf(b, "state.%s = (p) => { const el = %s; Object.assign(el, p); el.dispatchEvent(Object.assign(new Event(%q), p)); };\n", name, lookup, h.event)
	}
}

// runtimeHandler is one listener the lowered IR attaches to an element a
// program named, where the element is built by code rather than markup.
type runtimeHandler struct{ id, event string }

// runtimeHandlers is every such listener, in the order the package holds
// them.
func (g *htmlGen) runtimeHandlers() []runtimeHandler {
	if g.pkg == nil {
		return nil
	}
	var out []runtimeHandler
	_ = walkThroughCatch(g.pkg, func(n ir.Node) error {
		cs, ok := n.(*ir.CallStmt)
		if !ok || cs.Call == nil || cs.Call.Func == nil || cs.Call.Func.Intrinsic != ir.NodeOpAttachHandler || len(cs.Call.Args) < 2 {
			return nil
		}
		id, ok := cs.Call.Args[0].Value.(*ir.Ident)
		if !ok || !isProgramID(id.Name) {
			return nil
		}
		lit, ok := cs.Call.Args[1].Value.(*ir.Literal)
		if !ok {
			return nil
		}
		var decl *ir.Component
		if node := g.idToNode[id.Name]; node != nil {
			decl = node.Component
		}
		if ev := domEventName(decl, lit.Value); ev != "" {
			out = append(out, runtimeHandler{id: id.Name, event: ev})
		}
		return nil
	})
	return out
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

// bodyCalls names the functions a page body calls at the position they were
// written, in order.
//
// html renders a body as markup, so an imperative statement in one has no
// place there and is emitted into the startup script instead. Today these are
// an effect's setup calls; anything else a lowering leaves in a body arrives
// the same way.
//
// A factory's body is not a page body: its calls are written inside the
// factory, where the names they reach are in scope and where there is an
// instance for them to act on.
func (g *htmlGen) bodyCalls() []string {
	if g.pkg == nil {
		return nil
	}
	pkg := g.pkg
	var out []string
	collect := func(stmts []ir.Stmt) {
		for _, s := range stmts {
			call, isCall := s.(*ir.CallStmt)
			if !isCall || call.Call == nil || call.Call.Func == nil {
				continue
			}
			fn := call.Call.Func
			if len(fn.Block) == 0 || slotIndexFromRenderFunc(fn.Name) != "" {
				continue
			}
			out = append(out, fn.Name)
		}
	}
	for _, c := range g.pageComponents() {
		collect(c.Body)
	}
	collect(pkg.Body)
	return out
}

// callsUnfoldable reports whether expr calls a func that is not pure, whose
// value the build therefore could not have written into the markup.
func callsUnfoldable(expr ir.Expr) bool {
	found := false
	_ = ir.WalkExprs(expr, func(e ir.Expr) error {
		if c, ok := e.(*ir.Call); ok && c.Func != nil && c.Func.Purity != ir.PurityPure {
			found = true
			return ir.SkipAll
		}
		return nil
	})
	return found
}

// isWindowPrimitive reports whether n is html's Window: a document, or a
// `<dialog>` in one.
func isWindowPrimitive(n *ir.NodeInst) bool {
	return n != nil && n.Component != nil && n.Component.Intrinsic == windowIntrinsic
}

// windowIntrinsic is the id html.sngl gives its Window primitive.
const windowIntrinsic = "html:window"

// renderDialog writes a Window that is not the document: a `<dialog>` in it,
// shown without a backdrop as a desktop window is. `visible` is its `open`,
// the attribute `show()` sets, written by the init and by the patches the
// lowering put in every handler that writes it (respellWindowWrites). Its
// header carries the title and a close the user makes, which is a window
// manager's close: the window reports itself hidden through its `visible`
// write-back, and then `@closed` runs. A close the program makes runs
// nothing.
func (g *htmlGen) renderDialog(b *strings.Builder, n *ir.NodeInst, depth int) {
	indent := strings.Repeat("  ", depth)
	id := g.nodeID(n)
	title, _ := codegen.IRLiteralString(n.Prop(windowTitle))
	fmt.Fprintf(b, "%s<dialog id=%q", indent, id)
	if strings.HasPrefix(id, "__n") {
		fmt.Fprintf(b, " data-sngl-id=%q", id)
	}
	fmt.Fprintf(b, " aria-label=\"%s\">\n", html.EscapeString(title))
	close := g.nodeID(&ir.NodeInst{Name: "html.button"})
	fmt.Fprintf(b, "%s  <header>\n%s    <span>%s</span>\n", indent, indent, escapeTextContent(title))
	fmt.Fprintf(b, "%s    <button id=%q aria-label=\"Close\">\n%s      <span>×</span>\n%s    </button>\n%s  </header>\n", indent, close, indent, indent, indent)
	for _, s := range ir.WidgetChildren(n) {
		g.renderIRStmt(b, s, depth+1)
	}
	fmt.Fprintf(b, "%s</dialog>\n", indent)

	if v := n.Prop("visible"); v != nil {
		jsVal, requires := g.exprToJSReactiveCollect(v)
		g.initWrites = append(g.initWrites, updateFunc{
			funcName: fmt.Sprintf("$u_%s_open", strings.TrimPrefix(id, "$")),
			body:     fmt.Sprintf("%s.open = %s;", id, jsVal),
			deps:     g.exprDeps(v),
			initOnly: true,
			requires: requires,
		})
	}
	var visible, closed *ir.EventHandler
	for i := range n.Handlers {
		h := &n.Handlers[i]
		switch {
		case h.Name == "visible" && visible == nil:
			visible = h
		case h.Name == "closed" && closed == nil:
			closed = h
		}
	}
	var block []ir.Stmt
	if visible != nil && visible.Func != nil {
		hidden := ir.CloneStmtsSharingDecls(visible.Func.Block)
		if len(visible.Func.Params) > 0 {
			p := visible.Func.Params[0]
			_ = ir.RewriteExprs(hidden, func(e ir.Expr) (ir.Expr, error) {
				if id, ok := e.(*ir.Ident); ok && id.Sym == ir.Symbol(p) {
					return &ir.Literal{Type: ir.TypBool, Value: "false"}, nil
				}
				return e, nil
			})
		}
		block = append(block, hidden...)
	}
	if closed != nil && closed.Func != nil {
		block = append(block, closed.Func.Block...)
	}
	g.addEventHandler(nil, close, "click", &ir.Func{Block: block})
}

// respellWindowWrites drops what the lowering wrote to the document's props
// through its handle: the document cannot leave the screen, and its title is
// the page's, written once. A dialog's are written as its element's, a
// `visible` as its `open` (domPropForProp).
func respellWindowWrites(pkg *ir.Package, doc *ir.NodeInst, body []ir.Stmt) {
	if doc == nil || doc.ID == "" {
		return
	}
	writesDoc := func(a *ir.Assign) bool {
		sel, ok := a.Target.(*ir.Select)
		if !ok {
			return false
		}
		id, ok := sel.Operand.(*ir.Ident)
		return ok && id.Name == doc.ID
	}
	var filter func(stmts []ir.Stmt) []ir.Stmt
	filter = func(stmts []ir.Stmt) []ir.Stmt {
		out := stmts[:0:0]
		for _, s := range stmts {
			switch x := s.(type) {
			case *ir.Assign:
				if writesDoc(x) {
					continue
				}
			case *ir.If:
				x.Body, x.Else = filter(x.Body), filter(x.Else)
			case *ir.For:
				x.Body, x.Else = filter(x.Body), filter(x.Else)
			}
			out = append(out, s)
		}
		return out
	}
	funcs := map[*ir.Func]bool{}
	add := func(f *ir.Func) {
		if f != nil {
			funcs[f] = true
		}
	}
	for _, f := range pkg.Funcs {
		add(f)
	}
	for _, c := range pkg.Components {
		for _, f := range c.Funcs {
			add(f)
		}
	}
	for _, root := range []any{pkg, body} {
		_ = walkThroughCatch(root, func(n ir.Node) error {
			switch x := n.(type) {
			case *ir.NodeInst:
				for i := range x.Handlers {
					add(x.Handlers[i].Func)
				}
			case *ir.Lambda:
				add(x.Func)
			case *ir.If:
				if x.Catch != nil {
					add(x.Catch.Func)
				}
			}
			return nil
		})
	}
	for _, v := range pkg.Vars {
		for _, h := range v.Handlers {
			add(h.Func)
		}
	}
	for f := range funcs {
		f.Block = filter(f.Block)
	}
}

// windowIDs is the names a lowering creates a Window under in a script --
// a slot renderer, a component factory -- so the ops naming one are written
// for the `<dialog>` it is, whichever of them is translated first.
func (g *htmlGen) windowIDs() map[string]bool {
	if g.windowNames != nil {
		return g.windowNames
	}
	g.windowNames = map[string]bool{}
	_ = walkThroughCatch(g.pkg, func(n ir.Node) error {
		lv, ok := n.(*ir.LocalVar)
		if !ok {
			return nil
		}
		if call, ok := lv.Init.(*ir.Call); ok && call.Func != nil && call.Func.Name == ir.NodeOpCreateNode && len(call.Args) > 0 {
			if tag, ok := codegen.IRLiteralString(call.Args[0].Value); ok && tag == windowElement {
				g.windowNames[lv.Name] = true
			}
		}
		return nil
	})
	return g.windowNames
}
