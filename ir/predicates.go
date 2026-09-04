package ir

import "git.duckfam.us/jonathan/sngl/ast"

// IR-level predicates and accessors shared by the checker and every codegen
// backend. These are properties of the IR, not of any target language, so they
// live here alongside IsColorStruct/IsDateStruct/etc. rather than being
// reimplemented per consumer.

// IsNullToFuncConv reports whether n converts a null literal to a func type
// (`null` used where a func value is expected). Consumers render their own
// language-specific stub for the callable substitute; the detection is shared.
func IsNullToFuncConv(n *Conversion) bool {
	if n == nil || n.Type == nil || n.Type.Kind != TypeFunc {
		return false
	}
	lit, ok := n.Operand.(*Literal)
	return ok && lit.Type != nil && lit.Type.Kind == TypeNull
}

// IsErrorRaiseFunc reports whether fn is the error-raise intrinsic: either the
// "error.raise" intrinsic, or the stdlib error.raise method (whose wrapper does
// not carry Intrinsic, so the receiver+name pair is the stable identifier).
func IsErrorRaiseFunc(fn *Func) bool {
	if fn == nil {
		return false
	}
	if fn.Intrinsic == "error.raise" {
		return true
	}
	return fn.Receiver == "error" && fn.Name == "raise"
}

// StmtPos returns the source position of an IR statement when one is available.
// Each statement's AST field points back at the originating AST node whose Pos
// is the position. Returns the zero Pos for statements with no AST origin
// (e.g. lower-pass-synthesized statements).
func StmtPos(s Stmt) ast.Pos {
	switch n := s.(type) {
	case *Assign:
		if n.AST != nil {
			return n.AST.Pos
		}
	case *Toggle:
		if n.AST != nil {
			return n.AST.Pos
		}
	case *CallStmt:
		if n.Call != nil && n.Call.AST != nil {
			return n.Call.AST.Pos
		}
	case *Emit:
		if n.AST != nil {
			return n.AST.Pos
		}
	case *LocalVar:
		if n.AST != nil {
			return n.AST.Pos
		}
	case *Return:
		if n.AST != nil {
			return n.AST.Pos
		}
	case *NodeInst:
		// The one statement whose AST field is the interface rather than a
		// concrete node, which is why it was missing: a diagnostic about a
		// node -- an effect in the wrong scope, a prop a backend cannot emit
		// -- had nowhere to say where the node was written.
		if n.AST != nil {
			if p := n.AST.StmtPos(); p != nil {
				return *p
			}
		}
	case *Window:
		if n.AST != nil {
			return n.AST.Pos
		}
	case *If:
		if n.AST != nil {
			return n.AST.Pos
		}
	case *For:
		if n.AST != nil {
			return n.AST.Pos
		}
	}
	return ast.Pos{}
}
