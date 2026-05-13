package fyne

import (
	"strings"

	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/codegen/lang/golang"
	"git.duckfam.us/jonathan/sngl/ir"
)

// fyneTranslator implements codegen.IntrinsicTranslator for fyne. It
// emits Go source that mutates an enclosing *Model receiver — the
// generated functions all sit on Model so widget refs (e.g. `m.n0`)
// resolve as struct fields.
//
// One translator instance is constructed per __renderSlot<N> Func
// emission; widget-field registrations performed during that emission
// flow back into the enclosing *compilation via fieldSink.
type fyneTranslator struct {
	gc         *golang.GoIRContext
	blueprints map[string]*fyneBlueprint
	fieldSink  func(name, goType string)
}

// newFyneTranslator constructs a translator. `blueprints` is the
// platform-wide blueprint table loaded once in init(); `fieldSink`
// receives every (widget-name, go-type) pair so the enclosing emitter
// can declare the field on Model.
func newFyneTranslator(gc *golang.GoIRContext, blueprints map[string]*fyneBlueprint, fieldSink func(name, goType string)) *fyneTranslator {
	return &fyneTranslator{gc: gc, blueprints: blueprints, fieldSink: fieldSink}
}

// Compile-time interface check.
var _ codegen.IntrinsicTranslator = (*fyneTranslator)(nil)

func (t *fyneTranslator) OnCreateNode(id, tag string) string       { return "" }
func (t *fyneTranslator) OnAppendChild(parent, child string) string { return "" }
func (t *fyneTranslator) OnRemoveChild(parent, child string) string { return "" }
func (t *fyneTranslator) OnAttachHandler(node, event, h string) string {
	return ""
}
func (t *fyneTranslator) OnPropAssign(nodeID, prop string, valueExpr ir.Expr) string {
	return ""
}

// dummy use of strings so the import isn't unused before later tasks
// flesh out the method bodies. Removed by Task 2.
var _ = strings.Builder{}
