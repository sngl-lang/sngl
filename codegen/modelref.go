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

// RecvFieldRef builds a `<recv>.<name>` selector. recv is the receiver the
// scope being emitted dispatches through -- the model in a model method, the
// instance record inside a component's ctor -- so a node field written by a
// translator lands on whichever struct actually holds it.
func RecvFieldRef(recv, name string) ir.Expr {
	if recv == "" {
		recv = ModelReceiver
	}
	return &ir.Select{
		Operand: &ir.Ident{Name: recv},
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
	id, ok := handler.(*ir.Ident)
	if !ok {
		return fallback
	}
	fn, ok := id.Sym.(*ir.Func)
	if !ok || fn.LoweredFromComponentEvent == "" {
		return fallback
	}
	return fn.LoweredFromComponentEvent
}
