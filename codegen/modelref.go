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
