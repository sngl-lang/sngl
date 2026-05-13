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

// platformBlueprints returns the blueprint table loaded at init().
// Used by intrinsic translators to look up widget constructors and
// binding records by component tag.
func platformBlueprints() map[string]*fyneBlueprint {
	return loadBlueprints()
}

func (t *fyneTranslator) OnCreateNode(id, tag string) string {
	bp, ok := t.blueprints[tag]
	if !ok {
		return ""
	}
	if bp.Constructor == nil {
		return ""
	}
	goType := bp.Constructor.GoType
	if goType == "" {
		return ""
	}
	t.fieldSink(id, goType)
	args := zeroArgsFor(tag)
	return "m." + id + " = " + bp.Constructor.GoFn + "(" + args + ")\n"
}

// zeroArgsFor returns the constructor-arg string used when a slot-time
// CreateNode emits a widget; the slot's subsequent OnPropAssign calls
// fill the real values.
func zeroArgsFor(tag string) string {
	switch tag {
	case "text", "label":
		return `""`
	case "button":
		return `"", nil`
	default:
		return ""
	}
}

func (t *fyneTranslator) OnAppendChild(parent, child string) string {
	return parent + ".Add(" + modelRef(child) + ")\n"
}

func (t *fyneTranslator) OnRemoveChild(parent, child string) string {
	return parent + ".Remove(" + modelRef(child) + ")\n"
}

// modelRef qualifies a node id with "m." when it's a Plan A synthetic
// widget ref (`__nN`). Loop-local and parameter refs stay bare.
func modelRef(name string) string {
	if strings.HasPrefix(name, "__n") {
		return "m." + name
	}
	return name
}

func (t *fyneTranslator) OnAttachHandler(node, event, h string) string {
	return ""
}
func (t *fyneTranslator) OnPropAssign(nodeID, prop string, valueExpr ir.Expr) string {
	return ""
}
