package golang

import (
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
func VarGoType(v *ir.Var) string { return BindGoType(v.Type, v.Init) }

// BindGoType is VarGoType for a binding with no declaration behind it -- a
// window's route parameters, which the slot population declares and the
// request fills.
func BindGoType(t *ir.Type, init ir.Expr) string {
	if t != nil {
		return IRTypeToGo(t)
	}
	if init != nil {
		if et := init.ExprType(); et != nil {
			return IRTypeToGo(et)
		}
	}
	return "any"
}
