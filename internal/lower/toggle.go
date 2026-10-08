package lower

import (
	"fmt"

	"duckfam.us/sngl/ast"
	"duckfam.us/sngl/ir"
)

var passToggle = pass{
	name:    "NoToggle",
	enabled: func(c Features) bool { return !c.Toggle },
	apply:   lowerToggle,
}

// lowerToggle rewrites every Toggle stmt (`x!!`) in pkg to an Assign stmt
// (`x = !x`). The Target expression is shared between the new Assign's
// Target and Value; this is safe because Toggle targets are path
// expressions (Ident / Select / Index) the rest of the pipeline already
// treats as immutable.
func lowerToggle(pkg *ir.Package, _ Features, _ Options) error {
	if pkg == nil {
		return nil
	}
	walkPackage(pkg, walkFuncs{
		stmts: rewriteToggleStmts,
	})
	return nil
}

func rewriteToggleStmts(stmts []ir.Stmt) []ir.Stmt {
	for i, s := range stmts {
		switch n := s.(type) {
		case *ir.Toggle:
			stmts[i] = &ir.Assign{
				Target: n.Target,
				Op:     ast.AssignSet,
				Value: &ir.Unary{
					Type:    ir.TypBool,
					Op:      ast.UnaryNot,
					Operand: n.Target,
				},
			}
		case *ir.If:
			n.Body = rewriteToggleStmts(n.Body)
			n.Else = rewriteToggleStmts(n.Else)
		case *ir.For:
			n.Body = rewriteToggleStmts(n.Body)
			n.Else = rewriteToggleStmts(n.Else)
		case *ir.NodeInst:
			n.Children = rewriteToggleStmts(n.Children)
			for j := range n.Handlers {
				if n.Handlers[j].Func != nil {
					n.Handlers[j].Func.Block = rewriteToggleStmts(n.Handlers[j].Func.Block)
				}
			}
		case *ir.SlotInst:
			n.Children = rewriteToggleStmts(n.Children)
		case *ir.ErrorBoundary:
			n.Children = rewriteToggleStmts(n.Children)
			if n.Handler != nil && n.Handler.Func != nil {
				n.Handler.Func.Block = rewriteToggleStmts(n.Handler.Func.Block)
			}
		case *ir.ContextProvider:
			n.Children = rewriteToggleStmts(n.Children)
		case *ir.Assign, *ir.LocalVar, *ir.Return, *ir.CallStmt, *ir.Emit,
			*ir.Break, *ir.Continue:
			// Leaf stmts — no nested Toggle to rewrite.
		default:
			panic(fmt.Sprintf("rewriteToggleStmts: unhandled %T", n))
		}
	}
	return stmts
}
