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
func ModelFieldRef(name string) ir.Expr {
	return &ir.Select{
		Operand: &ir.Ident{Name: "m"},
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
