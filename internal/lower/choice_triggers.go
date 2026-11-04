package lower

import (
	"duckfam.us/sngl/ir"
)

// passChoiceTriggers writes a test's choice of a value, `c.pick.change(m)` on
// a select of Methods, as the choice's key: what the choices' package names
// for it, `ui.choiceLabel(c.pick.options, m)`, the function its
// `#[builtin("choiceKey")]` marks. Every target's select and radio is a native
// control that knows its choices by label -- fyne's Select, gtk4's drop-down,
// android's field, bubbletea's list, html's options -- and a test chooses
// through the control as a user does, so its invoker is handed the label: a
// value it was handed instead named nothing it held, and an int did not
// compile against the string the invoker takes.
//
// A trigger is rewritten where the node's component reads a data slot whose
// family's package marks a choiceKey of that family's records, and the event
// hands one value of the type the choices hold.
var passChoiceTriggers = pass{
	name:    "ChoiceTriggers",
	enabled: func(Features) bool { return true },
	apply: func(pkg *ir.Package, _ Features, _ Options) error {
		_ = ir.Walk(pkg, func(n ir.Node) error {
			if call, ok := n.(*ir.Call); ok && call.Event != "" && len(call.Args) == 1 {
				rewriteChoiceTrigger(call)
			}
			return nil
		})
		return nil
	},
}

func rewriteChoiceTrigger(call *ir.Call) {
	t := call.Receiver.ExprType()
	if t == nil || t.Kind != ir.TypeComponent {
		return
	}
	comp, ok := t.Decl.(*ir.Component)
	if !ok || len(comp.TypeParams) == 0 || len(t.Elems) != len(comp.TypeParams) {
		return
	}
	var event *ir.EventDecl
	for _, e := range comp.Events {
		if e.Name == call.Event {
			event = e
		}
	}
	if event == nil || len(event.Params) != 1 || event.Params[0].Type.Kind != ir.TypeTypeParam {
		return
	}
	bindings := map[string]*ir.Type{}
	for i, tp := range comp.TypeParams {
		bindings[tp.Name] = t.Elems[i]
	}
	for _, slot := range comp.Slots {
		if slot.Values == nil {
			continue
		}
		key := choiceKey(slot.Values.Type)
		if key == nil {
			continue
		}
		list := slot.Values.Type.Substitute(bindings)
		options := &ir.Select{Type: list, Operand: ir.CloneExprSharingDecls(call.Receiver), Field: slot.Values.Name}
		call.Args[0].Value = &ir.Call{
			Type: key.Return,
			Func: key,
			Args: []ir.CallArg{{Value: options}, {Value: call.Args[0].Value}},
		}
		return
	}
}

// choiceKey is the function the package declaring the records list holds
// marks as their key, or nil. It is asked of the record's own package, whose
// functions name that record: a target's copy of a package holds records of
// its own, and a key from another copy read records the build declared twice.
func choiceKey(list *ir.Type) *ir.Func {
	if list == nil || list.Kind != ir.TypeList || len(list.Elems) != 1 {
		return nil
	}
	rec, ok := list.Elems[0].Decl.(*ir.StructDef)
	if !ok || rec.RecordOf == nil || rec.Pkg == nil {
		return nil
	}
	for _, fn := range rec.Pkg.Funcs {
		if fn.Builtin != ir.BuiltinChoiceKey || len(fn.Params) == 0 {
			continue
		}
		if p := fn.Params[0].Type; p.Kind == ir.TypeList && len(p.Elems) == 1 && p.Elems[0].Decl == ir.Symbol(rec) {
			return fn
		}
	}
	return nil
}
