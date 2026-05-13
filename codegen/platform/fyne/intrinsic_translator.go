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
	idTags     map[string]string
}

// newFyneTranslator constructs a translator. `blueprints` is the
// platform-wide blueprint table loaded once in init(); `fieldSink`
// receives every (widget-name, go-type) pair so the enclosing emitter
// can declare the field on Model.
func newFyneTranslator(gc *golang.GoIRContext, blueprints map[string]*fyneBlueprint, fieldSink func(name, goType string)) *fyneTranslator {
	return &fyneTranslator{
		gc:         gc,
		blueprints: blueprints,
		fieldSink:  fieldSink,
		idTags:     map[string]string{},
	}
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
	t.idTags[id] = tag
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
	return parent + ".Add(" + child + ")\n"
}

func (t *fyneTranslator) OnRemoveChild(parent, child string) string {
	return parent + ".Remove(" + child + ")\n"
}

func (t *fyneTranslator) OnAttachHandler(node, event, handlerRef string) string {
	// `node` may arrive pre-qualified ("m.<id>") from dispatch's
	// identRef() helper; idTags is keyed by the bare id stored at
	// OnCreateNode time.
	bareID := strings.TrimPrefix(node, "m.")
	tag, ok := t.idTags[bareID]
	if !ok {
		return ""
	}
	bp, ok := t.blueprints[tag]
	if !ok {
		return ""
	}
	target := ""
	for _, b := range bp.Bindings {
		if b.Kind == bindEvent && b.Prop == event {
			target = b.Target
			break
		}
	}
	if target == "" {
		return ""
	}
	return node + target + " = " + handlerRef + "\n"
}
func (t *fyneTranslator) OnPropAssign(nodeID, prop string, valueExpr ir.Expr) string {
	tag, ok := t.idTags[nodeID]
	if !ok {
		return ""
	}
	bp, ok := t.blueprints[tag]
	if !ok {
		return ""
	}
	var (
		target    string
		transform string
		found     bool
	)
	for _, b := range bp.Bindings {
		if (b.Kind == bindReactive || b.Kind == bindInit) && b.Prop == prop {
			target = b.Target
			transform = b.Transform
			found = true
			break
		}
	}
	if !found || target == "" {
		return ""
	}
	val := t.gc.EvalExpr(valueExpr)
	if transform != "" {
		val = transform + "(" + val + ")"
	}
	return "m." + nodeID + target + "(" + val + ")\n"
}

func (t *fyneTranslator) OnDefault(stmt ir.Stmt) []string {
	return t.gc.EvalStmt(stmt)
}

func (t *fyneTranslator) OnSlotReset(slotID string) string {
	return "m." + slotID + " = nil\n"
}

func (t *fyneTranslator) OnSlotAppend(slotID, childID string) string {
	return "m." + slotID + " = append(m." + slotID + ", m." + childID + ")\n"
}
