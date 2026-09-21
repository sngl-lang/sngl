package codegen

import (
	"maps"

	"git.duckfam.us/jonathan/sngl/ir"
)

// CodegenCtx is the codegen-layer view of a checked package, created by a
// platform generator at the start of Generate().
type CodegenCtx struct {
	Pkg      *ir.Package
	Analysis *CommonAnalysis
	Deps     *DepTracker
	ExprCtx  *ExprCtx
	Namer    *Namer
	Platform string
	// RootComponent names the component a harness isolated as the whole
	// program; empty for every ordinary build, where a window is the root.
	// The test launcher sets it per-group so each test binary builds its
	// Model from the component under test.
	RootComponent string
	// Canvases is the drawings in this package and the function each paints
	// with. Built once here rather than by each platform: the names are method
	// names, so two platforms computing them separately would be two places to
	// number them in.
	Canvases *CanvasDraws
}

func NewCodegenCtx(req *Request, platform string) *CodegenCtx {
	root := OptionString(req.Options, "rootComponent")
	if root != "" && req.Pkg != nil {
		// AnalyzeCommon runs from the package alone and asks the same
		// question, so the option is stamped where it can read it.
		req.Pkg.RootComponent = root
	}
	analysis := AnalyzeCommon(req.Pkg)
	exprCtx := NewExprCtx(req.Pkg)
	exprCtx.Platform = platform
	exprCtx.Maps = req.Maps
	exprCtx.OutDir = req.OutDir
	return &CodegenCtx{
		Pkg:           req.Pkg,
		Analysis:      analysis,
		Deps:          NewDepTrackerFromPkg(req.Pkg),
		ExprCtx:       exprCtx,
		Namer:         NewNamer(),
		Platform:      platform,
		RootComponent: root,
		Canvases:      NewCanvasDraws(req.Pkg),
	}
}

// ScopedExprCtx returns the ExprCtx scoped to the declaration whose body a
// single-model platform emits: the main component when the program has one,
// and otherwise the window, when there is exactly one to be unambiguous about.
//
// A window declares state the way a component does, so a read of it has to
// resolve against the window or it falls out of scope resolution entirely and
// renders as a bare identifier the target never declared. Every platform that
// builds one Model wrote the component half of this itself; none wrote the
// window half, so a window's own state was a name none of them could resolve.
func (ctx *CodegenCtx) ScopedExprCtx() *ExprCtx {
	c := ctx.ExprCtx
	if root := ctx.RootDecl(); root != nil {
		c = c.ForComponent(root)
	}
	if w := ctx.EntryWindow(); w != nil {
		c = c.ForWindow(w)
	}
	return c
}

// EntryWindow is the window a single-model target scopes to, or nil where the
// program has not said which.
//
// The single-window case is unambiguous and always has been. Past one, the
// answer used to be nothing at all -- scoping to a guess was worse than
// scoping to none -- and `output(entry = home)` is what lets the program
// answer instead.
func (ctx *CodegenCtx) EntryWindow() *ir.Window {
	if ctx.Pkg == nil {
		return nil
	}
	if name := ctx.Pkg.EntryWindow; name != "" {
		for _, w := range ctx.Pkg.Windows {
			if w.ID == name {
				return w
			}
		}
	}
	// A window written inside a component is already in that component's
	// scope, so only the root-level ones are counted here.
	if len(ctx.Pkg.Windows) == 1 {
		return ctx.Pkg.Windows[0]
	}
	return nil
}

// OwnedVar is one binding a target puts in its Model, paired with the
// declaration that owns it. The owner matters to a target that scopes an
// expression per var -- gtk4 builds a per-var ExprCtx from it -- and to
// nothing else, which is why the enumeration can be shared even where the
// emission cannot.
//
// Sym is a symbol rather than an *ir.Var because not everything a Model holds
// is a declaration a body made: a window's route parameters are the *ir.Param
// its slot population binds, and whatever serves the page fills them in. A
// target stores the two the same way, which is the whole of what this list
// says; the accessors below are what a Param answers and a Var answers more
// of.
type OwnedVar struct {
	Sym  ir.Symbol
	Comp *ir.Component // the component declaring it, if one does
	Win  *ir.Window    // the window declaring it, if one does
}

// Name and Type are the two questions every binding answers.
func (o OwnedVar) Name() string   { return o.Sym.SymName() }
func (o OwnedVar) Type() *ir.Type { return o.Sym.SymType() }

// Var is the declaration where one was made, and nil for a binding something
// outside the program supplies. A caller that needs the three questions below
// should ask them rather than this.
func (o OwnedVar) Var() *ir.Var { v, _ := o.Sym.(*ir.Var); return v }

// Init is what the cell holds before anything writes it.
//
// For a parameter that is its type's zero, written out. Nothing in the program
// initialises one -- whatever serves the page does, and a target with no
// request never does -- so left nil the cell renders as whatever that backend
// calls nothing, and the page read `state.v.pkg` off an empty string. A
// declaration keeps its own answer, including none: a `var` with no
// initializer is a question each backend already has a zero for.
func (o OwnedVar) Init() ir.Expr {
	if v := o.Var(); v != nil {
		return v.Init
	}
	return ir.ZeroExpr(o.Type())
}

// IsConst and Synthesized are facts about a declaration, so a parameter is
// neither: it is written in source, and it is written to.
func (o OwnedVar) IsConst() bool     { v := o.Var(); return v != nil && v.IsConst }
func (o OwnedVar) Synthesized() bool { v := o.Var(); return v != nil && v.Synthesized }

// ModelState returns every var a single-Model target puts in its Model, in
// emission order: the package's vars and consts, then the main component's,
// then each window's.
//
// This is one answer to "which declarations own state", and it used to be four
// -- one per target, each spelling the same literal `pkg.Vars` plus
// `RootDecl().Vars`. Nothing made them agree, and #133 is what that cost:
// a window is an owner none of them named, so a window-level `var` reached no
// target at all. A target that does not want consts in its Model filters them
// out; what it must not do is decide for itself who owns state.
func (ctx *CodegenCtx) ModelState() []OwnedVar {
	if ctx.Pkg == nil {
		return nil
	}
	root := ctx.RootDecl()
	var out []OwnedVar
	// Keyed by the declaration, because one may be owned by several windows:
	// a root component that renders two of them is spliced into the package
	// body as one copy, so both read the same cell -- which is what "the
	// reference is shared" means on a target whose windows are one process.
	// Emitted per owner instead, the Model declared `hits int` twice and did
	// not compile.
	seen := map[ir.Symbol]bool{}
	add := func(v ir.Symbol, o ir.Owner) {
		if v == nil || seen[v] {
			return
		}
		seen[v] = true
		out = append(out, OwnedVar{Sym: v, Comp: o.Comp, Win: o.Win})
	}
	for _, o := range ir.Owners(ctx.Pkg) {
		// One Model holds one component's state: the root's. The others are
		// inlined into it before codegen, and a child's `count` is not this
		// model's `count`.
		if o.Comp != nil && o.Comp != root {
			continue
		}
		for _, v := range o.Vars {
			add(v, o)
		}
		for _, c := range o.Consts {
			add(c, o)
		}
	}
	// A window's route parameters are the one cell no owner declares: the
	// window's scoped slot binds them and whatever serves the page fills them
	// in. A target with no request never fills one and renders the struct's
	// zero -- but it still reads the binding, so the Model has to hold it or
	// the read names a field nothing declared.
	for _, w := range ctx.Windows() {
		if w.Window != nil && w.Window.Params != nil {
			add(w.Window.Params, ir.Owner{Win: w.Window})
		}
	}
	return out
}

func (ctx *CodegenCtx) BuildMutation(stmts []ir.Stmt) *MutationModel {
	m := NewMutationModel(ctx.Analysis, ctx.Deps)
	m.Handlers = ctx.collectHandlers(stmts)
	m.Timers = ctx.collectTimers()
	return m
}

func (ctx *CodegenCtx) BuildRender(stmts []ir.Stmt) *RenderModel {
	m := NewRenderModel(ctx.Analysis)
	m.Handlers = ctx.collectHandlers(stmts)
	m.Timers = ctx.collectTimers()
	return m
}

func (ctx *CodegenCtx) collectHandlers(stmts []ir.Stmt) []Handler {
	var handlers []Handler
	WalkVisualTree(stmts, func(n *ir.NodeInst, _ int) bool {
		elemID := n.ID
		if elemID == "" {
			elemID = ctx.Namer.NextPrefixed("$")
		}
		for _, h := range n.Handlers {
			mutated := make(map[ir.Symbol]struct{})
			if h.Func != nil {
				for _, stmt := range h.Func.Block {
					maps.Copy(mutated, MutatedFields(nil, ctx.Deps, stmt))
				}
			}
			handlers = append(handlers, Handler{
				NodeID:  elemID,
				Event:   h.Name,
				Mutated: mutated,
			})
		}
		return false
	})
	return handlers
}

func (ctx *CodegenCtx) collectTimers() []TimerHandler {
	var timers []TimerHandler
	var allTimers []ScheduledTimer
	for _, o := range ir.Owners(ctx.Pkg) {
		allTimers = append(allTimers, CollectTimers(o.Stmts())...)
	}
	for i, t := range allTimers {
		mutated := make(map[ir.Symbol]struct{})
		for _, stmt := range t.Handler.Block {
			maps.Copy(mutated, MutatedFields(nil, ctx.Deps, stmt))
		}
		activeVar := ""
		if t.Enabled != nil {
			if ident, ok := t.Enabled.(*ir.Ident); ok {
				activeVar = ident.Name
			}
		}
		timers = append(timers, TimerHandler{
			TimerInfo: TimerInfo{
				Index:      i,
				IntervalMs: IntervalToMs(t.Interval),
				ActiveVar:  activeVar,
				Body:       t.Handler.Block,
			},
			Mutated: mutated,
		})
	}
	return timers
}
