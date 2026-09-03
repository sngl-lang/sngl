package optimize

import (
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"strings"
	"time"

	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/internal/imports"
	"git.duckfam.us/jonathan/sngl/ir"
)

// schemeRunnableAtRuntime reports whether a target written in language `lang`
// can emit a working runtime call to a function imported via the given scheme.
// When false, a scheme-import value MUST be resolved at build time (const
// fold); a fold failure cannot be salvaged and must abort the build rather
// than emit broken output. Today only Go-language targets call go:, c:
// functions natively, and only JS targets call js: natively. (The html
// platform additionally bridges go: to wasm for explicitly runtime-used
// functions, but that path is selected in html codegen, not here — a const
// fold failure on html still has no runtime to fall back to.)
func schemeRunnableAtRuntime(scheme, lang string) bool {
	switch scheme {
	case "go", "c":
		return lang == "go"
	case "js":
		return lang == "js"
	}
	return false
}

// Config holds compile-time constants for the optimization pass.
type Config struct {
	Platform string // "html", "bubbletea"
	Language string // "js", "go"
	Dir      string // project directory (for compile-time go run execution)

	// NoCacheBust disables content-hash filename mangling for file: assets
	// resolved during folding. Default false (cache-busting enabled).
	NoCacheBust bool

	// FileAssets is populated by Optimize with file: assets that need
	// copying to the output directory.
	FileAssets []FileAsset

	// Cache memoizes compile-time evaluation. Set it to share one across the
	// targets of a build; left nil, Optimize allocates one for this Config,
	// which is what confines a folded value to the compilation that folded it.
	Cache *EvalCache

	// nativeErr records, per scheme, that this build's round loop failed as a
	// whole — a build error, a timeout — rather than for any one call. The
	// batches are independent programs, so a go: failure is no answer for a
	// js: call. It is not cached with the calls: the next target's Config
	// starts clean and retries.
	nativeErr map[string]error

	// nativeSettled records that compile-time evaluation of this build's
	// native calls has already run. Every caller optimizes twice with one
	// Config (before and after lowering), and by the second call every value
	// is cached — so the second must not clone a fully expanded IR just to
	// discover nothing. Should lowering somehow produce a new foldable call,
	// requestPureNativeFunc evaluates it on its own: the same approximation
	// hasUnresolvedNativeCall is allowed.
	nativeSettled bool
}

// FileAsset records a file that must be copied to the output directory.
type FileAsset struct {
	SrcPath string // absolute path on disk
	OutPath string // relative path in output (e.g. "assets/sngl.svg")
	Data    []byte // file contents read at fold time; pass-through to codegen
}

// evalCtx carries state needed during optimization.
// unrollsLoops reports whether a loop over a constant iterable is expanded
// into its iterations here, rather than left for the target to emit.
//
// Only for a target with no host language. `--lang none` is the static
// artifact: the platform writes markup, a loop has nowhere to run in it, and
// one left standing renders its body once -- so unrolling is the only way the
// nodes exist at all, at whatever size the program asks for.
//
// Every language target emits the loop itself (each language's ForHead), and
// its own compiler is in a better position to decide whether to unroll it: it
// can see the bounds are constant, and it does not have to write the copies
// into a source file that a person reads and a build compiles. Unrolled
// Compose was three copies of a RadioButton where the loop is one, and the
// same held for Go, Kotlin and the TUI.
//
// A window loop is not this. expandForWindows unrolls those on every target,
// because each iteration is a separate window -- a file, a top-level surface
// -- and not a repeated body.
//
// The language is not the whole question, though. A language emits a loop only
// where the platform hands it statements to emit, and a static-view platform
// (codegen.StaticViewRenderer -- html, in both its modes) hands it none: the
// tree is markup written once, so a loop still standing renders its body a
// single time with its variable bound to nothing. `sngl doc --http` lost its
// package list that way, seven links becoming one empty `<a>`, under --lang go.
//
// The question is asked of the platform rather than of the block being folded,
// because by the second Optimize call the distinction is gone: lowering moves
// a view subtree into a synthesized render function, so "markup" and "a
// function body" are the same shape by then. Unrolling a constant loop in a
// handler on such a platform costs a longer handler and nothing else, which is
// the price of asking a question that has an answer.
func (ctx *evalCtx) unrollsLoops() bool {
	return ctx == nil || ctx.language == "" || ctx.language == "none" || ctx.staticView
}

type evalCtx struct {
	platform string
	language string
	// staticView is the platform's answer to codegen.StaticViewRenderer,
	// asked once per package fold rather than per loop.
	staticView    bool
	dir           string
	noCacheBust   bool
	pkg           *ir.Package
	nativeImports map[string]*ir.NativeImport // lazily built from pkg.Imports
	nativeSchemes map[string]string           // import alias → scheme ("go", "js", ...)
	fileAssets    []FileAsset
	// native collects the pure native calls this pass could not answer from
	// cache. Non-nil only during a probe pass (see evalNativeRounds); a pass
	// over the real package runs with every needed value already cached.
	native *nativeEval
	// cache is Config.Cache: what this compilation has already evaluated.
	cache *EvalCache
	// nativeErr is Config.nativeErr: the batch failure this build already hit,
	// per scheme.
	nativeErr     map[string]error
	values        map[ir.Symbol]any     // const vars, params, and loop vars → evaluated values
	inlining      map[*ir.Component]int // recursion guard for component call inlining
	inliningFuncs map[*ir.Func]bool     // recursion guard for function inlining (detects mutual recursion)
	interpDepth   int                   // recursion guard for interpretFunc dispatch
	// err holds the first fatal evaluation error (e.g. a native import that
	// failed to evaluate at build time on a platform that requires the value
	// at compile time). Recorded during folding and surfaced by Optimize.
	err error
}

// child returns a context for folding a nested scope — a for-loop iteration,
// an inlined component body. It copies the parent whole and then replaces only
// the state a nested fold must not share: the value bindings it is about to add
// to, and the component recursion guard it increments.
//
// Copy the parent whole, deliberately: a field added to evalCtx is then carried
// into every nested fold with no edit here or at any call site. The one that
// was not — the compile-time request collector — cost the docs site every
// highlighted code block, with no error to show for it, because a nested fold
// that cannot record a request just leaves the value out.
func (ctx *evalCtx) child() *evalCtx {
	c := *ctx
	c.values = make(map[ir.Symbol]any, len(ctx.values)+2)
	maps.Copy(c.values, ctx.values)
	c.inlining = make(map[*ir.Component]int, len(ctx.inlining)+1)
	maps.Copy(c.inlining, ctx.inlining)
	return &c
}

// childInPkg returns a child that folds a body belonging to another package.
// Native-call resolution has to consult that package's imports, so the
// memoized lookup maps are dropped for it to rebuild.
func (ctx *evalCtx) childInPkg(pkg *ir.Package) *evalCtx {
	c := ctx.child()
	c.pkg = pkg
	c.nativeImports, c.nativeSchemes = nil, nil
	return c
}

// optimizerRun threads cross-package state across a single Optimize call so
// that imports are folded once even when reached via diamond import paths.
type optimizerRun struct {
	cfg        *Config
	done       map[*ir.Package]bool
	fileAssets []FileAsset
	err        error // first fatal eval error across root + imports
	native     *nativeEval
}

// maxEvalRounds bounds the round loop. Every round either caches a value for
// each pending call or caches the reason it has none, so the loop terminates
// on its own; the bound is here so a bug in that invariant fails with a
// message instead of spinning.
const maxEvalRounds = 10

// Optimize mutates pkg in place: evaluates constant expressions, inlines pure
// functions, eliminates dead branches and platform mismatches, and removes
// unreferenced declarations. Imported packages are folded recursively
// (Phases 1+2 only) so that for-loops inside imported components can unroll
// against their own package consts. Phases 3+4 run only on the root package.
func Optimize(pkg *ir.Package, cfg *Config) error {
	if cfg.Cache == nil {
		cfg.Cache = NewEvalCache()
	}
	// An override is the body a call runs on this target, so it has to be in
	// place before anything folds or inlines against it. Lowering swaps the
	// rest; this moves only what already has a body to replace.
	ir.SpecializeOverriddenBodies(pkg, cfg.Platform, cfg.Language)
	// A pure native call can only fold once a subprocess has computed it, and
	// building that subprocess is worth doing once for the whole batch. Learn
	// the batch from throwaway passes over a clone, then fold pkg itself with
	// every value already in hand.
	if !cfg.nativeSettled && hasPureNativeFuncs(pkg, cfg) && hasUnresolvedNativeCall(pkg, cfg) {
		if err := evalNativeRounds(pkg, cfg); err != nil {
			return err
		}
	}
	cfg.nativeSettled = true
	return optimizeIR(pkg, cfg, nil)
}

// evalNativeRounds discovers and evaluates every pure native call reachable
// from pkg. Each round folds a fresh clone — folding is destructive, and a
// pass that left a call unfolded cannot be resumed — and hands the calls it
// could not answer to one generated program.
func evalNativeRounds(pkg *ir.Package, cfg *Config) error {
	probe := *cfg
	probe.FileAssets = nil // a discarded pass must not report assets

	var pending []*nativeRequest
	for range maxEvalRounds {
		ne := &nativeEval{}
		cloneStart := time.Now()
		clone := ir.ClonePackage(pkg)
		slog.Debug("consteval probe clone", "duration", time.Since(cloneStart))
		if err := optimizeIR(clone, &probe, ne); err != nil {
			// Not final: the same fold runs again on the real package once the
			// values are in, and reports it then if it still fails.
			slog.Debug("consteval probe", "err", err)
		}
		if len(ne.order) == 0 {
			return nil
		}
		pending = ne.order
		slog.Info("consteval round", "calls", len(pending))
		if errs := runNativeRequests(cfg.Cache, cfg.Dir, ir.IndexNativeDecls(pkg), pending); len(errs) > 0 {
			// Nothing is cached for these calls, so retrying the identical
			// batch would only repeat the failure. The fold reports it per
			// call site, which is where the target's ability to call the
			// scheme at runtime decides whether it is fatal.
			cfg.nativeErr = errs
			return nil
		}
	}

	names := make([]string, 0, len(pending))
	for _, r := range pending {
		names = append(names, r.nativeType)
	}
	return fmt.Errorf("compile-time evaluation did not settle after %d rounds; still pending: %s",
		maxEvalRounds, strings.Join(names, ", "))
}

// hasPureNativeFuncs reports whether the package graph imports any pure
// function a subprocess could evaluate. It is the cheap half of the gate —
// import lists only, no IR walk — and hasUnresolvedNativeCall decides whether
// any such function is actually still called.
func hasPureNativeFuncs(pkg *ir.Package, cfg *Config) bool {
	if cfg.Dir == "" {
		return false
	}
	seen := map[*ir.Package]bool{}
	var walk func(*ir.Package) bool
	walk = func(p *ir.Package) bool {
		if p == nil || seen[p] {
			return false
		}
		seen[p] = true
		for _, imp := range p.Imports {
			if imp.Native != nil {
				for _, f := range imp.Native.Funcs {
					if f.Purity == ir.PurityPure && f.Foreign.Path != "file" && f.Foreign.Unusable == "" {
						return true
					}
				}
			}
			if walk(imp.Pkg) {
				return true
			}
		}
		return false
	}
	return walk(pkg)
}

// optimizeIR is Optimize's single pass. native is non-nil only for a probe
// pass, which records the native calls it could not fold instead of folding
// them.
func optimizeIR(pkg *ir.Package, cfg *Config, native *nativeEval) error {
	run := &optimizerRun{
		cfg:    cfg,
		done:   map[*ir.Package]bool{},
		native: native,
	}

	// Phases 1+2 on root and all imports (depth-first, memoized).
	rootCtx := run.foldPkg(pkg)
	if run.err != nil && native == nil {
		return run.err
	}
	// A probe carries on past a fold error: it is discarded either way, and
	// stopping here would hide the calls phase 3 goes on to discover.
	if rootCtx == nil {
		return nil
	}

	// Phase 3: Expand for-loop windows in main component (root only).
	start := time.Now()
	expandForWindows(pkg, rootCtx)
	slog.Debug("optimize: expand", "duration", time.Since(start))

	// Phase 4: Dead code elimination (root only). A probe pass is discarded,
	// and pruning unreferenced declarations discovers nothing, so it is skipped
	// there. Phase 3 is not: expanding a for-loop binds the loop variable, and
	// that is what makes a native call's argument const in the first place.
	if native == nil {
		start = time.Now()
		shakeUnused(pkg)
		slog.Debug("optimize: shake", "duration", time.Since(start))
	}

	// Accumulate file assets across multiple Optimize calls on the same
	// Config. The lowering pipeline runs Optimize twice (pre/post lower);
	// the second pass sees file: consts already folded to string
	// literals and produces no FileAssets, but the resolved assets from
	// the first pass must survive. Dedup by OutPath; new assets from this
	// run win on collision.
	newAssets := append(run.fileAssets, rootCtx.fileAssets...)
	seen := make(map[string]bool, len(newAssets))
	for _, fa := range newAssets {
		seen[fa.OutPath] = true
	}
	merged := slices.Clone(newAssets)
	for _, fa := range cfg.FileAssets {
		if !seen[fa.OutPath] {
			merged = append(merged, fa)
		}
	}
	cfg.FileAssets = merged

	// Phase 3 folds too, so it can be the first phase to hit a fatal
	// evaluation error. A probe's own errors are not reported: the same fold
	// runs again on the real package.
	if native != nil {
		return nil
	}
	return rootCtx.err
}

// foldPkg runs Phases 1+2 on pkg and recursively on its imports. Returns the
// pkg's evalCtx (so the caller can run downstream phases against it), or nil
// when pkg has already been folded by this run.
func (r *optimizerRun) foldPkg(pkg *ir.Package) *evalCtx {
	if pkg == nil || r.done[pkg] {
		return nil
	}
	r.done[pkg] = true

	// Recurse imports first so their consts/components are folded by the
	// time we fold this pkg's bodies (which may inline component calls
	// targeting imported components).
	for _, imp := range pkg.Imports {
		if imp.Pkg == nil {
			continue
		}
		// Skip native-scheme shells: they hold no SNGL bodies that the
		// optimizer can act on.
		if imp.Native != nil && len(imp.Pkg.Components) == 0 &&
			len(imp.Pkg.Windows) == 0 && len(imp.Pkg.Timers) == 0 {
			continue
		}
		if subCtx := r.foldPkg(imp.Pkg); subCtx != nil {
			r.fileAssets = append(r.fileAssets, subCtx.fileAssets...)
		}
	}

	ctx := &evalCtx{
		cache:         r.cfg.Cache,
		native:        r.native,
		nativeErr:     r.cfg.nativeErr,
		platform:      r.cfg.Platform,
		language:      r.cfg.Language,
		staticView:    codegen.PlatformRendersViewStatically(r.cfg.Platform),
		dir:           r.cfg.Dir,
		noCacheBust:   r.cfg.NoCacheBust,
		pkg:           pkg,
		values:        make(map[ir.Symbol]any),
		inliningFuncs: make(map[*ir.Func]bool),
	}

	// Phase 1: Evaluate all top-level consts.
	start := time.Now()
	for _, c := range pkg.Consts {
		if c.Init != nil {
			if val, ok := evalExpr(c.Init, ctx); ok {
				ctx.values[c] = val
			}
			c.Init = foldExpr(c.Init, ctx)
		}
	}
	slog.Debug("optimize: consts", "duration", time.Since(start))

	// Phase 2: Fold expressions in all declarations.
	start = time.Now()
	for _, v := range pkg.Vars {
		foldVar(v, ctx)
	}
	for _, f := range pkg.Funcs {
		f.Block = foldStmts(f.Block, ctx)
	}
	for _, s := range pkg.Structs {
		for _, f := range s.Fields {
			if f.Default != nil {
				f.Default = foldExpr(f.Default, ctx)
			}
		}
	}
	for _, e := range pkg.Enums {
		for _, m := range e.Members {
			if m.Value != nil {
				m.Value = foldExpr(m.Value, ctx)
			}
		}
	}
	// A context default is spliced into a synthesized Var by passContext, which
	// runs after this; folding it here is the only chance it gets.
	for _, c := range pkg.Contexts {
		if c.Default != nil {
			c.Default = foldExpr(c.Default, ctx)
		}
	}
	for _, o := range pkg.Outputs {
		if o.Options != nil {
			if st, ok := foldExpr(o.Options, ctx).(*ir.StructLit); ok {
				o.Options = st
			}
		}
	}
	pkg.Body = foldStmts(pkg.Body, ctx)
	for _, comp := range pkg.Components {
		foldComponent(comp, ctx)
	}
	for _, w := range pkg.Windows {
		foldWindow(w, ctx)
	}
	for _, t := range pkg.Timers {
		foldTimer(t, ctx)
	}
	slog.Debug("optimize: fold", "duration", time.Since(start))

	if ctx.err != nil && r.err == nil {
		r.err = ctx.err
	}
	return ctx
}

func foldVar(v *ir.Var, ctx *evalCtx) {
	if v.Init != nil {
		v.Init = foldExpr(v.Init, ctx)
	}
	for _, h := range v.Handlers {
		h.Func.Block = foldStmts(h.Func.Block, ctx)
	}
}

func foldComponent(comp *ir.Component, ctx *evalCtx) {
	for _, p := range comp.Props {
		if p.Default != nil {
			p.Default = foldExpr(p.Default, ctx)
		}
	}
	for _, v := range comp.Vars {
		foldVar(v, ctx)
	}
	for _, f := range comp.Funcs {
		f.Block = foldStmts(f.Block, ctx)
	}
	for _, t := range comp.Timers {
		foldTimer(t, ctx)
	}
	comp.Body = foldStmts(comp.Body, ctx)
}

// foldWindow folds everything a window owns. It must stay in step with the
// *ir.Window arm of foldStmt, which handles a window nested in a for-loop
// body: the two used to disagree about Href/Title/Favicon, so a top-level
// window's href was never folded and html's static mode rejected a
// compile-time-constant one as dynamic. ir.Rewrite has one window walk for
// both positions; this driver folds rather than rewrites, so it keeps its own.
func foldWindow(w *ir.Window, ctx *evalCtx) {
	if w.Href != nil {
		w.Href = foldExpr(w.Href, ctx)
	}
	if w.Title != nil {
		w.Title = foldExpr(w.Title, ctx)
	}
	if w.Favicon != nil {
		w.Favicon = foldExpr(w.Favicon, ctx)
	}
	for _, v := range w.Vars {
		foldVar(v, ctx)
	}
	for _, f := range w.Funcs {
		f.Block = foldStmts(f.Block, ctx)
	}
	if w.ErrorHandler != nil && w.ErrorHandler.Func != nil {
		w.ErrorHandler.Func.Block = foldStmts(w.ErrorHandler.Func.Block, ctx)
	}
	w.Body = foldStmts(w.Body, ctx)
}

func foldTimer(t *ir.Timer, ctx *evalCtx) {
	if t.Interval != nil {
		t.Interval = foldExpr(t.Interval, ctx)
	}
	if t.Enabled != nil {
		t.Enabled = foldExpr(t.Enabled, ctx)
	}
	if t.Handler != nil {
		t.Handler.Block = foldStmts(t.Handler.Block, ctx)
	}
}

// getNativeImports lazily builds the native imports map from the IR package.
// The *ir.NativeImport was already populated during checking, so we don't
// re-resolve via the scheme importer — we just index by alias.
func (ctx *evalCtx) getNativeImports() map[string]*ir.NativeImport {
	if ctx.nativeImports != nil {
		return ctx.nativeImports
	}
	ctx.nativeImports = make(map[string]*ir.NativeImport)
	ctx.nativeSchemes = make(map[string]string)
	if ctx.pkg == nil {
		return ctx.nativeImports
	}
	for _, imp := range ctx.pkg.Imports {
		if imp.Native == nil {
			continue
		}
		ctx.nativeImports[imp.Alias] = imp.Native
		if scheme, _ := imports.ParseScheme(imp.Path); scheme != "" {
			ctx.nativeSchemes[imp.Alias] = scheme
		}
	}
	return ctx.nativeImports
}
