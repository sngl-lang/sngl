package codegen

import (
	"strings"

	"git.duckfam.us/jonathan/sngl/ir"
)

// Field-reference helpers for the generated model struct. The Go-emitting model
// platforms (RenderModel/MutationModel: fyne, gtk4, bubbletea) share the "m"
// model-receiver convention, so these live with the model layer rather than
// being copied into each platform.

// ModelFieldRef builds an `m.<name>` selector against the generated model
// struct.
func ModelFieldRef(name string) ir.Expr { return RecvFieldRef(ModelReceiver, name) }

// ModelReceiver is the receiver a model platform's methods carry. It is the
// default, not the only one: a component instance is a record of its own, and
// its ctor and setters reach the same fields through the record.
const ModelReceiver = "m"

// ModelTypeName is the Go struct the three model platforms generate. Named
// here beside the receiver because a lifted type method takes the Model as a
// parameter and has to spell its type, which nothing in the IR can.
const ModelTypeName = "Model"

// SlotAnchorField names the field holding a render slot's anchor: a hidden
// child of the container the slot renders into, which the slot's entries are
// inserted before.
func SlotAnchorField(slot string) string { return slot + "_at" }

// RootSlotRenders is the set of render-slot calls written directly in body
// that render into the container named root. A handler inside the body
// renders the same slot into the same container, and is not a position in the
// body's own layout.
func RootSlotRenders(body []ir.Stmt, root string) map[ir.Stmt]bool {
	set := map[ir.Stmt]bool{}
	for _, s := range body {
		cs, ok := s.(*ir.CallStmt)
		if !ok || cs.Call == nil || cs.Call.Func == nil || !cs.Call.Func.SlotRender || len(cs.Call.Args) != 1 {
			continue
		}
		if id, ok := cs.Call.Args[0].Value.(*ir.Ident); ok && id.Name == root {
			set[s] = true
		}
	}
	return set
}

// Receiver is the symbol of the receiver RecvFieldRef selects through, so a
// program's own binding of the same name is never what it resolves to.
type Receiver struct{ Name string }

func (r *Receiver) SymName() string   { return r.Name }
func (r *Receiver) SymType() *ir.Type { return ir.TypDyn }

// RecvFieldRef builds a `<recv>.<name>` selector. recv is the receiver the
// scope being emitted dispatches through -- the model in a model method, the
// instance record inside a component's ctor -- so a node field written by a
// translator lands on whichever struct actually holds it.
func RecvFieldRef(recv, name string) ir.Expr {
	if recv == "" {
		recv = ModelReceiver
	}
	return &ir.Select{
		Operand: &ir.Ident{Name: recv, Sym: &Receiver{Name: recv}},
		Field:   name,
		Type:    ir.TypDyn,
	}
}

// IdentBareName returns an ident's name with the "m." model-struct prefix
// stripped, or "" if e is not an *ir.Ident.
func IdentBareName(e ir.Expr) string {
	if id, ok := e.(*ir.Ident); ok {
		return strings.TrimPrefix(id.Name, "m.")
	}
	return ""
}

// HandlerPayload is the struct type a handler's SNGL parameter is declared as,
// or nil for one that takes none. A test hands its trigger a value of it.
func HandlerPayload(handler ir.Expr) (*ir.Type, *ir.StructDef) {
	var fn *ir.Func
	switch h := handler.(type) {
	case *ir.Ident:
		fn, _ = h.Sym.(*ir.Func)
	case *ir.Lambda:
		fn = h.Func
	}
	if fn == nil || len(fn.Params) == 0 || fn.Params[0] == nil || fn.Params[0].Type == nil {
		return nil, nil
	}
	sd, ok := fn.Params[0].Type.Decl.(*ir.StructDef)
	if !ok || sd == nil {
		return nil, nil
	}
	return fn.Params[0].Type, sd
}

// TriggerEventName is the event a test can write for a handler: the one the
// program wrote, when a platform override re-raised it, and otherwise the name
// the handler already carries.
//
// A platform override subscribes to its host widget's event and re-raises the
// component's — gtk4's button is `@clicked { click() }` — so a promoted handler
// is named for the host while a test writes `c.inc.click()`. The program's name
// is recorded during the substitution that inlined its block
// (ir.Func.LoweredFromComponentEvent), because that is the last point both are
// visible. A platform whose primitive declares the component's own event name
// gets the same answer from the fallback.
func TriggerEventName(handler ir.Expr, fallback string) string {
	var fn *ir.Func
	switch h := handler.(type) {
	case *ir.Ident:
		fn, _ = h.Sym.(*ir.Func)
	case *ir.Lambda:
		fn = h.Func
	}
	if fn == nil || fn.LoweredFromComponentEvent == "" {
		return fallback
	}
	return fn.LoweredFromComponentEvent
}

// ReadEventField rewrites every `<param>.<field>` read in stmts to a clone of
// value: the widget-level value a target's callback is handed, or reads off the
// widget, in place of a payload struct it never builds. It reports whether the
// param is still read some other way afterwards, which the caller cannot
// spell.
func ReadEventField(stmts []ir.Stmt, param *ir.Param, field string, value ir.Expr) (rest bool) {
	isParam := func(e ir.Expr) bool {
		id, ok := e.(*ir.Ident)
		return ok && (id.Sym == ir.Symbol(param) || (id.Sym == nil && id.Name == param.Name))
	}
	_ = ir.Rewrite(stmts, func(nd ir.Node) (ir.Node, error) {
		if sel, ok := nd.(*ir.Select); ok && sel.Field == field && isParam(sel.Operand) {
			return ir.CloneExprSharingDecls(value), ir.SkipDir
		}
		return nd, nil
	})
	_ = ir.WalkExprs(stmts, func(e ir.Expr) error {
		if isParam(e) {
			rest = true
		}
		return nil
	})
	return rest
}
