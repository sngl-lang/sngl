package optimize

import (
	"fmt"
	"maps"
	"strings"

	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/internal/interp"
	"git.duckfam.us/jonathan/sngl/ir"
)

// Fold is the build-time evaluation a target that writes its view out as
// markup writes its documents with (codegen.Fold). It runs after lowering, on
// the package the second Optimize left, and is where a loop is unrolled at
// all. Which documents a program has -- a surface, a page of a stack -- is
// the target's question; this answers what each holds once it is decided.
//
// A target writes one document at a time because a site is a loop over its
// pages, and unrolling that loop in the IR held every page's expanded tree
// live at once: 3.9 GB of heap for the docs site, most of it pages already
// written. Each document is a clone folded in a child of the package's fold,
// so nothing yielded is reachable from the package.
type Fold struct {
	ctx *evalCtx
}

var _ codegen.Fold = (*Fold)(nil)

// NewFold is the fold of pkg: its consts and its build consts evaluated, and
// every constant loop it folds unrolled.
func NewFold(pkg *ir.Package, cfg *Config) *Fold {
	if cfg.Cache == nil {
		cfg.Cache = NewEvalCache()
	}
	run := &optimizerRun{
		cfg:        cfg,
		done:       map[*ir.Package]bool{},
		root:       pkg,
		writes:     newWritesAnalysis(cfg.Platform, cfg.Language),
		interpEnvs: map[*ir.Package]*interp.Env{},
	}
	ctx := run.newCtx(pkg)
	ctx.unroll, ctx.spliced = true, true
	// Built here so every child shares the one map rather than each building
	// its own.
	ctx.getNativeImports()
	for _, c := range pkg.Consts {
		if c.Init != nil {
			if val, ok := evalExpr(c.Init, ctx); ok {
				ctx.values[c] = val
			}
		}
	}
	for _, c := range pkg.BuildConsts {
		if c.Init != nil {
			if val, ok := evalExpr(c.Init, ctx); ok {
				ctx.values[c] = val
			}
		}
	}
	return &Fold{ctx: ctx}
}

func (f *Fold) Child() codegen.Fold { return &Fold{ctx: f.ctx.child()} }

func (f *Fold) Eval(e ir.Expr) (any, bool) { return evalExpr(e, f.ctx) }

func (f *Fold) Expr(e ir.Expr) ir.Expr { return foldExpr(e, f.ctx) }

func (f *Fold) Stmts(stmts []ir.Stmt) []ir.Stmt { return foldStmts(stmts, f.ctx) }

func (f *Fold) Clone(stmts []ir.Stmt) []ir.Stmt { return cloneStmts(stmts) }

func (f *Fold) Decide(cond ir.Expr) (value, ok bool) {
	lit, isLit := foldExpr(cloneExpr(cond), f.ctx).(*ir.Literal)
	if !isLit || lit.Type == nil || lit.Type.Kind != ir.TypeBool {
		return false, false
	}
	return lit.Value == "true", true
}

func (f *Fold) LoopItems(fs *ir.For) ([]any, bool) { return loopItems(fs, f.ctx) }

func (f *Fold) BindLoop(fs *ir.For, i int, item any) {
	keyVar, valueVar := loopVars(fs)
	maps.Copy(f.ctx.values, bindLoopVars(fs, f.ctx, keyVar, valueVar, i, item).values)
}

func (f *Fold) Bind(sym ir.Symbol, v any) { f.ctx.values[sym] = v }

func (f *Fold) Answer(id string, v any) {
	answers := maps.Clone(f.ctx.answers)
	if answers == nil {
		answers = map[string]any{}
	}
	answers[id] = v
	f.ctx.answers = answers
}

func (f *Fold) Fail(err error) {
	if f.ctx.err == nil {
		f.ctx.err = err
	}
}

func (f *Fold) Err() error { return f.ctx.err }

func (f *Fold) TakeFileAssets() []codegen.FileAsset {
	var out []codegen.FileAsset
	for _, fa := range f.ctx.fileAssets {
		out = append(out, codegen.FileAsset{SrcPath: fa.SrcPath, OutPath: fa.OutPath, Data: fa.Data})
	}
	f.ctx.fileAssets = nil
	return out
}

// SettleNatives is evalNativeRounds for the calls a loop variable makes
// constant, which the first Optimize could not see: `lookup.PackageIndex(p.path)`
// in a page per package. Each round probes every document that holds a pure
// native call and discards it, and hands the calls it could not answer to one
// generated program -- answered one at a time instead, a site of a hundred
// such pages built a hundred programs.
func (f *Fold) SettleNatives(probe func(codegen.Fold) error) error {
	ctx := f.ctx
	cfg := ctx.cfg
	if !hasPureNativeFuncs(ctx.pkg, cfg) {
		return nil
	}
	var pending []*nativeRequest
	for range maxEvalRounds {
		ne := &nativeEval{}
		p := ctx.child()
		p.native = ne
		if err := probe(&Fold{ctx: p}); err != nil && p.err == nil {
			p.err = err
		}
		if len(ne.order) == 0 {
			return nil
		}
		pending = ne.order
		if errs := runNativeRequests(cfg.Cache, cfg.Trust, cfg.Dir, ir.IndexNativeDecls(ctx.pkg), pending); len(errs) > 0 {
			cfg.nativeErr, ctx.nativeErr = errs, errs
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

func (f *Fold) HoldsNativeCall(s ir.Stmt) bool {
	found := false
	scanStmts([]ir.Stmt{s}, func(e ir.Expr) {
		if call, ok := e.(*ir.Call); ok && !found && isEvaluableNativeCall(call, f.ctx) {
			found = true
		}
	})
	return found
}

// loopItems evaluates a loop's iterable to the elements an unroll writes out.
// A count past maxStaticUnroll is an error on ctx rather than a list.
func loopItems(fs *ir.For, ctx *evalCtx) ([]any, bool) {
	// A condition and a headless loop walk nothing, so there is no iterable
	// to evaluate: both are refused in a view body, and a nil Iter reaching
	// evalExpr would read as an unevaluable one rather than as another kind of
	// loop.
	if fs.Iter == nil {
		return nil, false
	}
	if t := fs.Iter.ExprType(); t != nil && t.Kind == ir.TypeBool {
		return nil, false
	}
	head := fs.Iter
	// A list walked as a sequence: the first Optimize folds a short sngl:seq
	// into its literal, which the checker's conversion then wraps. Unwrapped
	// here rather than in evalConversion, where every other fold of one would
	// replace the target's own sequence with a list.
	if conv, ok := head.(*ir.Conversion); ok && conv.Type != nil && conv.Type.Kind == ir.TypeIter {
		head = conv.Operand
	}
	val, ok := evalExpr(head, ctx)
	if !ok {
		return nil, false
	}
	items, ok := val.([]any)
	if !ok {
		// A nil is an absent list, not an unevaluable one: a pure native call
		// returning an empty (or nil) slice iterates zero times.
		if val != nil {
			return nil, false
		}
		items = nil
	}
	if len(items) > maxStaticUnroll {
		if ctx.err == nil {
			pos := ""
			if fs.AST != nil {
				pos = fs.AST.Pos.String() + ": "
			}
			ctx.err = fmt.Errorf("%sthis loop repeats %d times, and this target writes every iteration into its output (limit %d): give it a smaller count, or build for a target that can run the loop",
				pos, len(items), maxStaticUnroll)
		}
		return nil, false
	}
	return items, true
}

// bindLoopVars is a child of ctx with one iteration's variables bound, typed
// the way the checker types them: in `for var k, v = xs` the key is the index
// and the value the element, and in `for var x = xs` the one variable is the
// element. The form is the syntactic one -- findLoopVar is nil for a variable
// nothing reads, so keying off it would bind an unused `x`'s index to `i`.
func bindLoopVars(fs *ir.For, ctx *evalCtx, keyVar, valueVar *ir.LoopVar, i int, item any) *evalCtx {
	child := ctx.child()
	if fs.Value != "" {
		if keyVar != nil {
			child.values[keyVar] = i
		}
		if valueVar != nil {
			child.values[valueVar] = item
		}
	} else if keyVar != nil {
		child.values[keyVar] = item
	}
	return child
}
