package checker

import (
	"strconv"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/ir"
)

// A written type is held to the signature; an unknown want means there is none.
func (c *checker) bindParamType(written ast.TypeExpr, want *ir.Type, what string) *ir.Type {
	unknown := want == nil || want.Kind == ir.TypeDyn
	if written == nil {
		if unknown {
			return TypDyn
		}
		return want
	}
	got := c.resolveType(written)
	if unknown {
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
