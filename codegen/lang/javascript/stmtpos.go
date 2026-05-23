package javascript

import (
	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/ir"
)

// stmtIRPos extracts a source position from an IR statement when one is
// available. Mirrors the equivalent helper in codegen/lang/golang.
// Returns the zero Pos when the statement has no AST origin (e.g.
// lower-pass-synthesized statements).
func stmtIRPos(s ir.Stmt) ast.Pos {
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
