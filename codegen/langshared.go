package codegen

import (
	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/ir"
)

// Shared IR-level helpers used identically by more than one language
// translator (codegen/lang/{golang,javascript,kotlin}). They operate purely on
// IR and emit no language-specific syntax, so they live here rather than being
// copy-pasted per language.

// IsNullToFuncConv reports whether n converts a null literal to a func type
// (`null` used where a func value is expected). Each language renders its own
// stub for the callable substitute, but the detection is identical.
func IsNullToFuncConv(n *ir.Conversion) bool {
	if n == nil || n.Type == nil || n.Type.Kind != ir.TypeFunc {
		return false
	}
	lit, ok := n.Operand.(*ir.Literal)
	return ok && lit.Type != nil && lit.Type.Kind == ir.TypeNull
}

// IsErrorRaiseFunc reports whether fn is the error-raise intrinsic (the
// `ErrorRaise` intrinsic, or the stdlib `error.raise` method).
func IsErrorRaiseFunc(fn *ir.Func) bool {
	if fn == nil {
		return false
	}
	if fn.Intrinsic == "ErrorRaise" {
		return true
	}
	return fn.Receiver == "error" && fn.Name == "raise"
}

// PluralKeyConstString maps a predeclared i18n.PluralKey constant selector
// (`i18n.zero`..`i18n.other`) to its quoted string-literal form, shared by the
// JS and Kotlin runtimes which both key plural forms by string category.
// Returns "" for non-matches. (Go deliberately differs — it emits real
// i18n.Plural* constants — so it does not use this.)
func PluralKeyConstString(qual string) string {
	switch qual {
	case "i18n.zero":
		return `"zero"`
	case "i18n.one":
		return `"one"`
	case "i18n.two":
		return `"two"`
	case "i18n.few":
		return `"few"`
	case "i18n.many":
		return `"many"`
	case "i18n.other":
		return `"other"`
	}
	return ""
}

// StmtIRPos extracts a source position from an IR statement when one is
// available. Each *ir.<Stmt>.AST points back at the original AST node whose
// Pos is the position. Returns the zero Pos when the statement has no AST
// origin (e.g. lower-pass-synthesized statements).
func StmtIRPos(s ir.Stmt) ast.Pos {
	switch n := s.(type) {
	case *ir.Assign:
		if n.AST != nil {
			return n.AST.Pos
		}
	case *ir.Toggle:
		if n.AST != nil {
			return n.AST.Pos
		}
	case *ir.CallStmt:
		if n.Call != nil && n.Call.AST != nil {
			return n.Call.AST.Pos
		}
	case *ir.Emit:
		if n.AST != nil {
			return n.AST.Pos
		}
	case *ir.LocalVar:
		if n.AST != nil {
			return n.AST.Pos
		}
	case *ir.Return:
		if n.AST != nil {
			return n.AST.Pos
		}
	case *ir.If:
		if n.AST != nil {
			return n.AST.Pos
		}
	case *ir.For:
		if n.AST != nil {
			return n.AST.Pos
		}
	}
	return ast.Pos{}
}
