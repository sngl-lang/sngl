package optimize

import (
	"log/slog"
	"slices"
	"strings"
	"time"

	"git.duckfam.us/jonathan/sngl/ir"
)

// schemeRunnableAtRuntime reports whether a target written in language `lang`
// can emit a working runtime call to a function imported via the given scheme.
// When false, a scheme-import value MUST be resolved at build time (const
// fold); a fold failure cannot be salvaged and must abort the build rather
// than emit broken output. Today only Go-language targets call go://, c://
// functions natively, and only JS targets call js:// natively. (The html
// platform additionally bridges go:// to wasm for explicitly runtime-used
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

	// NoCacheBust disables content-hash filename mangling for file:// assets
	// resolved during folding. Default false (cache-busting enabled).
	NoCacheBust bool

	// FileAssets is populated by Optimize with file:// assets that need
	// copying to the output directory.
	FileAssets []FileAsset
}

// FileAsset records a file that must be copied to the output directory.
type FileAsset struct {
	SrcPath string // absolute path on disk
	OutPath string // relative path in output (e.g. "assets/sngl.svg")
	Data    []byte // file contents read at fold time; pass-through to codegen
}

// evalCtx carries state needed during optimization.
type evalCtx struct {
	platform      string
	language      string
	dir           string
	noCacheBust   bool
	pkg           *ir.Package
	nativeImports map[string]*ir.NativeImport // lazily built from pkg.Imports
	nativeSchemes map[string]string           // import alias → scheme ("go", "js", ...)
	fileAssets    []FileAsset
	values        map[ir.Symbol]any     // const vars, params, and loop vars → evaluated values
	inlining      map[*ir.Component]int // recursion guard for component call inlining
	interpDepth   int                   // recursion guard for interpretFunc dispatch
	// err holds the first fatal evaluation error (e.g. a go:// import that
	// failed to evaluate at build time on a platform that requires the value
	// at compile time). Recorded during folding and surfaced by Optimize.
	err error
}

// optimizerRun threads cross-package state across a single Optimize call so
// that imports are folded once even when reached via diamond import paths.
type optimizerRun struct {
	cfg        *Config
	done       map[*ir.Package]bool
	fileAssets []FileAsset
	err        error // first fatal eval error across root + imports
}

// Optimize mutates pkg in place: evaluates constant expressions, inlines pure
// functions, eliminates dead branches and platform mismatches, and removes
// unreferenced declarations. Imported packages are folded recursively
// (Phases 1+2 only) so that for-loops inside imported components can unroll
// against their own package consts. Phases 3+4 run only on the root package.
func Optimize(pkg *ir.Package, cfg *Config) error {
	run := &optimizerRun{
		cfg:  cfg,
		done: map[*ir.Package]bool{},
	}

	// Phases 1+2 on root and all imports (depth-first, memoized).
	rootCtx := run.foldPkg(pkg)
	if run.err != nil {
		return run.err
	}
	if rootCtx == nil {
		return nil
	}

	// Phase 3: Expand for-loop windows in main component (root only).
	start := time.Now()
	expandForWindows(pkg, rootCtx)
	slog.Debug("optimize: expand", "duration", time.Since(start))

	// Phase 4: Dead code elimination (root only).
	start = time.Now()
	shakeUnused(pkg)
	slog.Debug("optimize: shake", "duration", time.Since(start))

	// Accumulate file assets across multiple Optimize calls on the same
	// Config. The lowering pipeline runs Optimize twice (pre/post lower);
	// the second pass sees file:// consts already folded to string
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
	return nil
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
		platform:    r.cfg.Platform,
		language:    r.cfg.Language,
		dir:         r.cfg.Dir,
		noCacheBust: r.cfg.NoCacheBust,
		pkg:         pkg,
		values:      make(map[ir.Symbol]any),
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

func foldWindow(w *ir.Window, ctx *evalCtx) {
	for _, v := range w.Vars {
		foldVar(v, ctx)
	}
	for _, f := range w.Funcs {
		f.Block = foldStmts(f.Block, ctx)
	}
	w.Body = foldStmts(w.Body, ctx)
}

func foldTimer(t *ir.Timer, ctx *evalCtx) {
	if t.Interval != nil {
		t.Interval = foldExpr(t.Interval, ctx)
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
		if scheme, _, ok := strings.Cut(imp.Path, "://"); ok {
			ctx.nativeSchemes[imp.Alias] = scheme
		}
	}
	return ctx.nativeImports
}
