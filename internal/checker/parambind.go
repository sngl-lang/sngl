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

// bindParams builds the parameters a handler binds. The list is the ParamList a
// function declares its parameters with, so it admits a default value and a
// mark; supplier is what hands the value over, and names it in the report.
func (c *checker) bindParams(pl ast.ParamList, want *ir.Type, owner, supplier string) []*ir.Param {
	c.refuseParamMarks(pl.Params)
	params := make([]*ir.Param, len(pl.Params))
	for i, p := range pl.Params {
		what := bindParamWhat(p.Name, owner)
		if p.Default != nil {
			c.rejectBindDefault(ast.Pos(*p.Default.ExprPos()), what, supplier)
		}
		params[i] = &ir.Param{Name: p.Name, Type: c.bindParamType(p.Type, want, what)}
	}
	return params
}

func (c *checker) rejectBindDefault(pos ast.Pos, what, supplier string) {
	c.error(pos, "%s takes no default value: %s supplies what the name is bound to", what, supplier)
}
