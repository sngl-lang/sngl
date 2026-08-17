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
	// Requires records the runtime helpers and native imports this updater's
	// body needs, collected structurally during its translation (not by
	// text-scanning). Because these ride on the Updater, a dead updater dropped
	// by OptimizeMutation contributes nothing, and merged updaters union them —
	// so the platform's final helper/import set reflects only surviving code.
	Requires Requirement
}

// Requirement is the set of runtime helpers and native imports a generated
// code body depends on. Unioned across merged updaters; dropped with dead ones.
type Requirement struct {
	Helpers       map[string]bool            // helper function names (e.g. "String")
	NativeImports map[string]map[string]bool // module path → set of imported names
}

// MergeInto unions r's helpers and native imports into the given (non-nil)
// maps. Used by platforms to fold a surviving updater's requirements into the
// shared helper/import sets they render from.
func (r Requirement) MergeInto(helpers map[string]bool, native map[string]map[string]bool) {
	for h := range r.Helpers {
		helpers[h] = true
	}
	for mod, names := range r.NativeImports {
		if native[mod] == nil {
			native[mod] = map[string]bool{}
		}
		for n := range names {
			native[mod][n] = true
		}
	}
}

// union merges other into r (in place), allocating r's maps as needed.
func (r *Requirement) union(other Requirement) {
	for h := range other.Helpers {
		if r.Helpers == nil {
			r.Helpers = map[string]bool{}
		}
		r.Helpers[h] = true
	}
	for mod, names := range other.NativeImports {
		if r.NativeImports == nil {
			r.NativeImports = map[string]map[string]bool{}
		}
		if r.NativeImports[mod] == nil {
			r.NativeImports[mod] = map[string]bool{}
		}
		for n := range names {
			r.NativeImports[mod][n] = true
		}
	}
}

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
