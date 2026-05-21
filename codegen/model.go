package codegen

import (
	"git.duckfam.us/jonathan/sngl/ir"
)

// MutationModel is the intermediate representation for platforms that emit
// a static component tree once and generate targeted updater functions to
// patch specific parts when state changes (Document+Mutations model).
//
// Used by HTML and Fyne.
type MutationModel struct {
	Analysis   *CommonAnalysis
	DepTracker *DepTracker
	Updaters   []Updater
	Handlers   []Handler
	Timers     []TimerHandler
}

// RenderModel is the intermediate representation for platforms that
// re-render the full view from state on every change (Render Loop model).
//
// Used by BubbleTea and Android/Compose.
type RenderModel struct {
	Analysis *CommonAnalysis
	Handlers []Handler
	Timers   []TimerHandler
}

// Updater is a registered reactive update function with its dependencies.
// The platform-specific code body is stored as a string; deps track which
// root state fields this updater reads.
type Updater struct {
	Name     string               // e.g., "$u_0_text", "updateLabel0"
	Kind     string               // "text", "attr", "if", "list", "else", "disabled", etc.
	Node     *ir.NodeInst         // the node this updater targets
	Expr     ir.Expr              // the reactive expression being watched
	Body     string               // platform-specific code body (filled during emit)
	Deps     map[*ir.Var]struct{} // root state var dependencies
	InitOnly bool                 // run only on initial sync; mutation updates are emitted inline elsewhere
}

// DepVars implements Dependent for use with FindAffected.
func (u Updater) DepVars() map[*ir.Var]struct{} { return u.Deps }

// Handler represents an event binding on a visual node.
type Handler struct {
	NodeID  string               // element/widget identifier
	Event   string               // "click", "input", "change"
	Body    ir.Stmt              // mutation IR (ir.Assign, ir.CallStmt, etc.)
	Mutated map[*ir.Var]struct{} // vars this handler mutates
}

// TimerHandler combines timer metadata with its mutation info.
type TimerHandler struct {
	TimerInfo
	Mutated map[*ir.Var]struct{} // vars mutated by the timer body
}

// NewMutationModel creates a MutationModel from a CommonAnalysis.
func NewMutationModel(a *CommonAnalysis) *MutationModel {
	return &MutationModel{
		Analysis:   a,
		DepTracker: a.DepTracker(),
	}
}

// NewRenderModel creates a RenderModel from a CommonAnalysis.
func NewRenderModel(a *CommonAnalysis) *RenderModel {
	return &RenderModel{
		Analysis: a,
	}
}

// AffectedUpdaters returns the updaters affected by a set of mutated vars.
func (m *MutationModel) AffectedUpdaters(mutated map[*ir.Var]struct{}) []Updater {
	return FindAffected(m.DepTracker, m.Updaters, mutated)
}
