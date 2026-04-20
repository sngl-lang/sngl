package codegen

import (
	"git.duckfam.us/jonathan/sngl/ir"
)

// CodegenCtx is the codegen-layer view of a checked package.
// Platform generators create one at the start of Generate() and use
// it for iteration, name generation, and dependency tracking.
type CodegenCtx struct {
	Pkg      *ir.Package
	Analysis *CommonAnalysis
	Deps     *DepTracker
	ExprCtx  *ExprCtx
	Namer    *Namer
	Platform string
}

// NewCodegenCtx creates a CodegenCtx from a codegen Request.
func NewCodegenCtx(req *Request, platform string) *CodegenCtx {
	analysis := AnalyzeCommon(req.Pkg)
	return &CodegenCtx{
		Pkg:      req.Pkg,
		Analysis: analysis,
		Deps:     analysis.DepTracker(),
		ExprCtx:  NewExprCtx(req.Pkg),
		Namer:    NewNamer(),
		Platform: platform,
	}
}

// BuildMutation creates a MutationModel pre-populated with handlers and timers.
func (ctx *CodegenCtx) BuildMutation(stmts []ir.Stmt) *MutationModel {
	m := NewMutationModel(ctx.Analysis)
	m.Handlers = ctx.collectHandlers(stmts)
	m.Timers = ctx.collectTimers()
	return m
}

// BuildRender creates a RenderModel pre-populated with handlers and timers.
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
			mutated := make(map[string]bool)
			if h.Func != nil {
				for _, stmt := range h.Func.Block {
					for k, v := range MutatedFields(stmt) {
						mutated[k] = v
					}
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
		mutated := make(map[string]bool)
		if t.Handler != nil {
			for _, stmt := range t.Handler.Block {
				for k, v := range MutatedFields(stmt) {
					mutated[k] = v
				}
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
