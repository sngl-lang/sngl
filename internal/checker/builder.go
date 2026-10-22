package checker

import (
	"duckfam.us/sngl/ast"
	"duckfam.us/sngl/ir"
)

// refuseBuilderState reports state that holds a builder: a var of the package
// or a component, a `var` written in a block of a view (state of that block),
// a prop or a context. What a view shows is a string, and a builder is how a
// function makes one; an add is a write in place, which a reactive cell would
// have to report on every piece.
func (c *checker) refuseBuilderState() {
	if c.inLibSource() || c.pkg.Library {
		return
	}
	refuse := func(pos ast.Pos, what string, t *ir.Type) {
		if ir.HoldsBuilder(t) {
			c.error(pos, "%s holds a builder, which is no state: keep the string it builds (b.string()) instead", what)
		}
	}
	viewLocals := func(stmts []ir.Stmt) {
		ir.WalkView(stmts, func(s ir.Stmt) bool {
			if lv, ok := s.(*ir.LocalVar); ok {
				refuse(localPos(lv), "var "+lv.Name, lv.Type)
			}
			return true
		})
	}
	for _, v := range c.pkg.Vars {
		if !v.IsConst {
			refuse(varPos(v), "var "+v.Name, v.Type)
		}
	}
	for _, ctx := range c.pkg.Contexts {
		if ctx.Pkg == c.pkg && ctx.AST != nil {
			refuse(ctx.AST.Pos, "context "+ctx.Name, ctx.Typ)
		}
	}
	for _, comp := range c.pkg.Components {
		for _, v := range comp.Vars {
			refuse(varPos(v), "var "+v.Name, v.Type)
		}
		for _, p := range comp.Props {
			refuse(compDeclPos(comp), "prop "+p.Name+" of "+comp.Name, p.Type)
		}
		viewLocals(comp.Stmts)
	}
	viewLocals(c.pkg.Body)
}

// localPos is where a local was declared.
func localPos(lv *ir.LocalVar) ast.Pos {
	if lv.AST != nil {
		return lv.AST.Pos
	}
	if lv.Sym != nil {
		return varPos(lv.Sym)
	}
	return ast.Pos{}
}
