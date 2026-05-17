package checker

import (
	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/ir"
)

// isContextDeclCallStmt reports whether s has the shape `context #id(arg)`:
// Call.Func is *ast.SelectExpr{Operand: *ast.IdentExpr{Name: "context"}, Kind: SelectElemRef}.
func isContextDeclCallStmt(s *ast.CallStmt) bool {
	sel, ok := s.Call.Func.(*ast.SelectExpr)
	if !ok || sel.Kind != ast.SelectElemRef {
		return false
	}
	ident, ok := sel.Operand.(*ast.IdentExpr)
	return ok && ident.Name == "context"
}

func (c *checker) registerRootContextDecl(s *ast.CallStmt) {
	sel := s.Call.Func.(*ast.SelectExpr)
	name := sel.Field
	ctx := &ir.Context{AST: s, Name: name}
	if name == "" {
		c.error(s.Pos, "context decl requires #identifier")
	}
	args := s.Call.Args.Args
	if len(args) != 1 {
		c.error(s.Pos, "context decl takes exactly one positional default value")
		c.pkg.Contexts = append(c.pkg.Contexts, ctx)
		return
	}
	a, isArg := args[0].(ast.Arg)
	if !isArg || a.Name != "" {
		c.error(s.Pos, "context decl takes exactly one positional default value")
		c.pkg.Contexts = append(c.pkg.Contexts, ctx)
		return
	}
	def := c.checkExpr(a.Value)
	if def != nil && !ir.IsConst(def) {
		c.error(*a.Value.ExprPos(), "context default must be a constant expression")
	}
	ctx.Default = def
	if def != nil {
		ctx.Typ = def.ExprType()
	}
	c.pkg.Contexts = append(c.pkg.Contexts, ctx)
	if name != "" {
		c.scope.Declare(ctx)
	}
}
