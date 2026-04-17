package optimize

import (
	"log/slog"
	"strings"
	"time"

	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/ir"
)

// Config holds compile-time constants for the optimization pass.
type Config struct {
	Platform string // "html", "bubbletea"
	Language string // "js", "go"
	Dir      string // project directory (for compile-time go run execution)

	// FileAssets is populated by Optimize with file:// assets that need
	// copying to the output directory.
	FileAssets []FileAsset
}

// FileAsset records a file that must be copied to the output directory.
type FileAsset struct {
	SrcPath string // absolute path on disk
	OutPath string // relative path in output (e.g. "assets/sngl.svg")
}

// evalCtx carries state needed during optimization.
type evalCtx struct {
	platform      string
	language      string
	dir           string
	pkg           *ir.Package
	nativeImports map[string]*codegen.NativeDecls // lazily built from pkg.Imports
	fileAssets    []FileAsset
	values        map[ir.Symbol]any // const vars and loop vars → evaluated values
}

// Optimize mutates pkg in place: evaluates constant expressions, inlines pure
// functions, eliminates dead branches and platform mismatches, and removes
// unreferenced declarations.
func Optimize(pkg *ir.Package, cfg *Config) error {
	ctx := &evalCtx{
		platform: cfg.Platform,
		language: cfg.Language,
		dir:      cfg.Dir,
		pkg:      pkg,
		values:   make(map[ir.Symbol]any),
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

	// Phase 3: Expand for-loop windows in main component.
	start = time.Now()
	expandForWindows(pkg, ctx)
	slog.Debug("optimize: expand", "duration", time.Since(start))

	// Phase 4: Dead code elimination.
	start = time.Now()
	shakeUnused(pkg)
	slog.Debug("optimize: shake", "duration", time.Since(start))

	cfg.FileAssets = ctx.fileAssets
	return nil
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
func (ctx *evalCtx) getNativeImports() map[string]*codegen.NativeDecls {
	if ctx.nativeImports != nil {
		return ctx.nativeImports
	}
	ctx.nativeImports = make(map[string]*codegen.NativeDecls)
	if ctx.pkg == nil {
		return ctx.nativeImports
	}
	for _, imp := range ctx.pkg.Imports {
		if imp.Native == nil {
			continue
		}
		scheme, uri := parseImportScheme(imp.AST.Path)
		if scheme == "" {
			continue
		}
		si := codegen.LookupScheme(scheme)
		if si == nil {
			continue
		}
		decls, err := si.Resolve(uri, "")
		if err != nil {
			continue
		}
		ctx.nativeImports[imp.Alias] = decls
	}
	return ctx.nativeImports
}

func parseImportScheme(path string) (scheme, uri string) {
	if before, after, ok := strings.Cut(path, "://"); ok {
		return before, after
	}
	return "", path
}
