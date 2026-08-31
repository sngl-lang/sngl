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
	// RootComponent overrides which component is treated as "main"; empty
	// means the literal "main" lookup. The test launcher sets it per-group so
	// each test binary builds its Model from the component-under-test.
	RootComponent string
}

func NewCodegenCtx(req *Request, platform string) *CodegenCtx {
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
		RootComponent: OptionString(req.Options, "rootComponent"),
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
	if main := ctx.MainComponent(); main != nil {
		c = c.ForComponent(main)
	}
	// Only when there is exactly one, so that the window being scoped to is
	// not a guess. A window statement inside the main component needs no entry
	// here: it is already inside that component's scope.
	if len(ctx.Pkg.Windows) == 1 {
		c = c.ForWindow(ctx.Pkg.Windows[0])
	}
	return c
}

// OwnedVar is one var a target puts in its Model, paired with the declaration
// that owns it. The owner matters to a target that scopes an expression per var
// -- gtk4 builds a per-var ExprCtx from it -- and to nothing else, which is why
// the enumeration can be shared even where the emission cannot.
type OwnedVar struct {
	Var  *ir.Var
	Comp *ir.Component // the component declaring it, if one does
	Win  *ir.Window    // the window declaring it, if one does
}

// ModelState returns every var a single-Model target puts in its Model, in
// emission order: the package's vars and consts, then the main component's,
// then each window's.
//
// This is one answer to "which declarations own state", and it used to be four
// -- one per target, each spelling the same literal `pkg.Vars` plus
// `MainComponent().Vars`. Nothing made them agree, and #133 is what that cost:
// a window is an owner none of them named, so a window-level `var` reached no
// target at all. A target that does not want consts in its Model filters them
// out; what it must not do is decide for itself who owns state.
func (ctx *CodegenCtx) ModelState() []OwnedVar {
	if ctx.Pkg == nil {
		return nil
	}
	root := ctx.MainComponent()
	var out []OwnedVar
	for _, o := range ir.Owners(ctx.Pkg) {
		// One Model holds one component's state: the root's. The others are
		// inlined into it before codegen, and a child's `count` is not this
		// model's `count`.
		if o.Comp != nil && o.Comp != root {
			continue
		}
		for _, v := range o.Vars {
			out = append(out, OwnedVar{Var: v, Comp: o.Comp, Win: o.Win})
		}
		for _, c := range o.Consts {
			out = append(out, OwnedVar{Var: c, Comp: o.Comp, Win: o.Win})
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
			mutated := make(map[*ir.Var]struct{})
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
	allTimers := append([]*ir.Timer{}, ctx.Pkg.Timers...)
	if main := ctx.MainComponent(); main != nil {
		allTimers = append(allTimers, main.Timers...)
	}
	for i, t := range allTimers {
		mutated := make(map[*ir.Var]struct{})
		if t.Handler != nil {
			for _, stmt := range t.Handler.Block {
				maps.Copy(mutated, MutatedFields(nil, ctx.Deps, stmt))
			}
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
