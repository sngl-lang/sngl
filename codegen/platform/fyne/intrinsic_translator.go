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

func (t *fyneTranslator) OnCreateNode(id, tag string) []string {
	bp, ok := t.blueprints[tag]
	if !ok {
		return nil
	}
	if bp.Constructor == nil {
		return nil
	}
	goType := bp.Constructor.GoType
	if goType == "" {
		return nil
	}
	t.fieldSink(id, goType)
	t.idTags[id] = tag
	return []string{"m." + id + " = " + bp.Constructor.GoFn + "(" + bp.Constructor.ZeroArgs + ")"}
}

func (t *fyneTranslator) OnAppendChild(parent, child string) []string {
	return []string{t.qualifyParent(parent) + ".Add(" + t.qualifyChild(child) + ")"}
}

func (t *fyneTranslator) OnRemoveChild(parent, child string) []string {
	return []string{t.qualifyParent(parent) + ".Remove(" + t.qualifyChild(child) + ")"}
}

// qualifyParent maps the bare IR ident name for a slot-function's parent
// param ("parent") onto the type-asserted local ("container") emitted in
// emitIRSlotFunc's prologue. Pre-qualified inputs (those starting with
// "m." or already "container") are returned untouched so existing direct
// callers of OnAppendChild still work.
func (t *fyneTranslator) qualifyParent(p string) string {
	if p == "parent" {
		return "container"
	}
	return p
}

// qualifyChild prefixes a bare synthesized ref (e.g. "__n0", "__entry") with
// "m." so the emitted Go resolves through the Model receiver. Names
// starting with "m." or "_" loop-locals (like "__entry" — but slot
// teardown still wants the loop var bare) are tricky. Convention from
// the lower pass: synthesized node refs use "__n<N>"; teardown's loop
// var is bound as "__entry" without Synthesized=true. We approximate by
// prepending "m." only for "__n"-prefixed names; the loop-key case
// remains bare.
func (t *fyneTranslator) qualifyChild(c string) string {
	if strings.HasPrefix(c, "m.") {
		return c
	}
	if strings.HasPrefix(c, "__n") {
		return "m." + c
	}
	return c
}

func (t *fyneTranslator) OnAttachHandler(node, event, handlerRef string) []string {
	// `node` may arrive pre-qualified ("m.<id>") from the translator's
	// own qualification helpers; idTags is keyed by the bare id stored
	// at OnCreateNode time.
	bareID := strings.TrimPrefix(node, "m.")
	tag, ok := t.idTags[bareID]
	if !ok {
		return nil
	}
	bp, ok := t.blueprints[tag]
	if !ok {
		return nil
	}
	target := ""
	for _, b := range bp.Bindings {
		if b.Kind == bindEvent && b.Prop == event {
			target = b.Target
			break
		}
	}
	if target == "" {
		return nil
	}
	qNode := node
	if !strings.HasPrefix(qNode, "m.") {
		qNode = "m." + qNode
	}
	qHandler := handlerRef
	if !strings.HasPrefix(qHandler, "m.") && strings.HasPrefix(qHandler, "__") {
		qHandler = "m." + qHandler
	}
	return []string{qNode + target + " = " + qHandler}
}
func (t *fyneTranslator) OnPropAssign(nodeID, prop string, valueExpr ir.Expr) []string {
	tag, ok := t.idTags[nodeID]
	if !ok {
		return nil
	}
	bp, ok := t.blueprints[tag]
	if !ok {
		return nil
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
		return nil
	}
	val := t.gc.EvalExpr(valueExpr)
	if transform != "" {
		val = transform + "(" + val + ")"
	}
	return []string{"m." + nodeID + target + "(" + val + ")"}
}

func (t *fyneTranslator) OnDefault(stmt ir.Stmt) []string {
	return t.gc.EvalStmt(stmt)
}

func (t *fyneTranslator) OnSlotReset(slotID string) []string {
	return []string{"m." + slotID + " = nil"}
}

func (t *fyneTranslator) OnSlotAppend(slotID, childID string) []string {
	return []string{"m." + slotID + " = append(m." + slotID + ", m." + childID + ")"}
}

func (t *fyneTranslator) OnIter(iter ir.Expr) string {
	if id, ok := iter.(*ir.Ident); ok && id.Synthesized {
		return "m." + id.Name
	}
	return t.gc.EvalExpr(iter)
}

func (t *fyneTranslator) OnCond(cond ir.Expr) string {
	return t.gc.EvalExpr(cond)
}
