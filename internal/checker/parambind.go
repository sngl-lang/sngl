package checker

import (
	"strconv"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/ir"
)

// bindParamType returns want, and errors at written naming what unless written is nil, ir.Type.Equal to want, or want is nil (no signature: written stands).
func (c *checker) bindParamType(written ast.TypeExpr, want *ir.Type, what string) *ir.Type {
	if written == nil {
		if want == nil {
			return TypDyn
		}
		return want
	}
	got := c.resolveType(written)
	if want == nil {
		return got
	}
	if !want.Equal(got) {
		c.error(*written.ExprPos(), "cannot use %s as %s for %s", got, want, what)
	}
	return want
}

func bindParamWhat(name, owner string) string {
	return "parameter " + strconv.Quote(name) + " of " + owner
}

func (c *checker) buildVarHandlerParams(h *ast.EventHandler, varType *ir.Type) []*ir.Param {
	params := make([]*ir.Param, len(h.Params.Params))
	for i, p := range h.Params.Params {
		typ := c.bindParamType(p.Type, varType, bindParamWhat(p.Name, "@"+h.Name))
		params[i] = &ir.Param{Name: p.Name, Type: typ}
	}
	return params
}
