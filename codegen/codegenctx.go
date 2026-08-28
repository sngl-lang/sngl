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
	exprCtx.Maps = req.Maps
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
