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
// mark; supplier is what hands the values over, and names it in the report.
//
// want is the supplier's signature, bound by position the way a func literal
// binds its parameters: a handler may stop short, but may not name a parameter
// the supplier does not pass. A nil want is a supplier with no signature to
// read -- an undeclared event on a component the checker could not resolve --
// and binds every name as dyn; an empty one passes nothing.
func (c *checker) bindParams(pl ast.ParamList, want []*ir.Param, owner, supplier string) []*ir.Param {
	c.refuseParamMarks(pl.Params)
	c.refuseConstParams(pl.Params, "a handler's")
	if want != nil && len(pl.Params) > len(want) {
		passes := "none"
		if len(want) > 0 {
			passes = strconv.Itoa(len(want))
		}
		c.error(pl.Params[len(want)].Pos, "%s binds %s, but %s passes %s", owner, countOf(len(pl.Params), "parameter"), supplier, passes)
	}
	params := make([]*ir.Param, len(pl.Params))
	for i, p := range pl.Params {
		what := bindParamWhat(p.Name, owner)
		if p.Default != nil {
			c.rejectBindDefault(ast.Pos(*p.Default.ExprPos()), what, supplier)
		}
		var wt *ir.Type
		if i < len(want) {
			wt = want[i].Type
		}
		params[i] = &ir.Param{Name: p.Name, Type: c.bindParamType(p.Type, wt, what)}
	}
	return params
}

func (c *checker) rejectBindDefault(pos ast.Pos, what, supplier string) {
	c.error(pos, "%s takes no default value: %s supplies what the name is bound to", what, supplier)
}

// eventParams resolves the parameters an event declares, which are a
// function's: the same list, resolved by the same code, so each is named and
// typed. A bare `@tick` declares none, as `@tick()` does. What a function's
// parameter may carry and an event's may not is refused here: an emit
// supplies every parameter or forwards them all, so none has a default or a
// `const` to hold the caller to, and a handler binds them by position, so
// none collects the rest.
func (c *checker) eventParams(e ast.EventDecl) []*ir.Param {
	c.refuseParamMarks(e.Params.Params)
	for _, p := range e.Params.Params {
		switch {
		case p.Default != nil:
			c.error(p.Pos, "parameter %q of event @%s takes no default value: the emit supplies it", p.Name, e.Name)
		case p.Const:
			c.error(p.Pos, "parameter %q of event @%s cannot be const: a handler receives what the emit passed", p.Name, e.Name)
		}
		if vt, ok := p.Type.(*ast.VariadicType); ok {
			c.error(vt.Pos, "parameter %q of event @%s cannot collect the rest: a handler binds an event's parameters by position", p.Name, e.Name)
		}
	}
	return c.buildParams(e.Params)
}
