package golang

import (
	"strings"

	"git.duckfam.us/jonathan/sngl/ir"
)

// Helpers shared by the Go-emitting platform generators (fyne, gtk4,
// bubbletea), which all build on this language package. They were previously
// copy-pasted byte-for-byte into each platform's compiler/intrinsic files.

// FuncReturnGoType renders the Go type a func returns: its declared return
// type, or (for a single-return-statement body) the inferred type of the
// returned expression. Returns "" when neither is known.
func FuncReturnGoType(f *ir.Func) string {
	if f.Return != nil && f.Return.Kind != ir.TypeDyn {
		return IRTypeToGo(f.Return)
	}
	if len(f.Block) == 1 {
		if ret, ok := f.Block[0].(*ir.Return); ok && ret.Value != nil {
			if t := ret.Value.ExprType(); t != nil {
				return IRTypeToGo(t)
			}
		}
	}
	return ""
}

// VarGoType renders the Go type of a var: its declared type, or the inferred
// type of its initializer. Falls back to "any".
func VarGoType(v *ir.Var) string {
	if v.Type != nil {
		return IRTypeToGo(v.Type)
	}
	if v.Init != nil {
		if t := v.Init.ExprType(); t != nil {
			return IRTypeToGo(t)
		}
	}
	return "any"
}

// IdentBareName returns an ident's name with the "m." model-struct prefix
// stripped, or "" if e is not an *ir.Ident.
func IdentBareName(e ir.Expr) string {
	if id, ok := e.(*ir.Ident); ok {
		return strings.TrimPrefix(id.Name, "m.")
	}
	return ""
}

// ModelFieldRef builds an `m.<name>` selector against the generated model
// struct — the shared field-access convention of the Go desktop generators.
func ModelFieldRef(name string) ir.Expr {
	return &ir.Select{
		Operand: &ir.Ident{Name: "m"},
		Field:   name,
		Type:    ir.TypDyn,
	}
}
