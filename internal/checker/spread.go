package checker

import (
	"strconv"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/ir"
)

// spreadArg is a `...operand` written in an argument list, read as the named
// arguments its fields stand for.
type spreadArg struct {
	ast    *ast.SpreadExpr
	typ    *ir.Type
	fields []spreadField
	bound  int
}

type spreadField struct {
	name  string
	typ   *ir.Type
	value ir.Expr
}

// expandSpread checks the operand of a spread argument. Every argument list
// reads one this way -- a call, a component's props, a slot insertion, an
// emit -- so what a spread means does not depend on where it is written.
func (c *checker) expandSpread(sp *ast.SpreadExpr) (*spreadArg, bool) {
	operand := c.checkExpr(sp.Operand)
	fields, ok := spreadFieldsOf(operand)
	if !ok {
		got := "(nil)"
		if t := exprType(operand); t != nil {
			got = t.String()
		}
		c.error(sp.Pos, "spread requires a struct type, got %s", got)
		return nil, false
	}
	return &spreadArg{ast: sp, typ: exprType(operand), fields: fields}, true
}

// spreadFieldsOf reads each field of a struct-typed operand, typed as the
// operand's own type arguments make it.
func spreadFieldsOf(operand ir.Expr) ([]spreadField, bool) {
	t := exprType(operand)
	var sd *ir.StructDef
	if t != nil && t.Kind == ir.TypeStruct {
		sd, _ = t.Decl.(*ir.StructDef)
	}
	if sd == nil {
		return nil, false
	}
	var bindings map[string]*ir.Type
	if len(sd.TypeParams) > 0 && len(t.Elems) == len(sd.TypeParams) {
		bindings = make(map[string]*ir.Type, len(sd.TypeParams))
		for i, tp := range sd.TypeParams {
			bindings[tp.Name] = t.Elems[i]
		}
	}
	var out []spreadField
	for _, f := range sd.Fields {
		ft := f.Type
		if bindings != nil {
			ft = ft.Substitute(bindings)
		}
		out = append(out, spreadField{
			name:  f.Name,
			typ:   ft,
			value: &ir.Select{Type: ft, Operand: operand, Field: f.Name},
		})
	}
	return out, true
}

// bindSpreadField measures field f against the type of the parameter or prop
// it names, which what describes for the diagnostic. The caller counts the
// match in bound before asking, so a duplicate still counts as naming one.
func (c *checker) bindSpreadField(s *spreadArg, f spreadField, want *ir.Type, what string) (ir.Expr, bool) {
	if want == nil {
		return f.value, true
	}
	if f.typ.Kind != ir.TypeDyn && want.Kind != ir.TypeDyn && !f.typ.IsAssignableTo(want) {
		c.error(s.ast.Pos, "cannot use field %q (%s) as %s (%s)", f.name, f.typ, what, want)
		return nil, false
	}
	return wrapIfNeeded(f.value, want), true
}

// spreadProps binds a spread written in a component call's arguments to the
// props its fields name, handing each to add. comp is nil for a node with no
// declaration to name props against, and a spread there binds nothing it can
// be held to.
func (c *checker) spreadProps(sp *ast.SpreadExpr, comp *ir.Component, boundProps map[string]bool, add func(name string, v ir.Expr)) {
	spread, ok := c.expandSpread(sp)
	if !ok || comp == nil {
		return
	}
	for _, f := range spread.fields {
		propType := componentPropType(comp, f.name)
		if propType == nil {
			continue
		}
		spread.bound++
		if boundProps[f.name] {
			c.error(sp.Pos, "prop %q already provided on component %s", f.name, comp.Name)
			continue
		}
		v, fits := c.bindSpreadField(spread, f, propType, "prop "+strconv.Quote(f.name))
		if !fits {
			continue
		}
		boundProps[f.name] = true
		add(f.name, v)
	}
	c.finishSpread(spread)
}

// finishSpread reports a spread none of whose fields named anything. A field
// naming nothing is left out, which is what lets one options struct feed
// several callees; a spread whose every field is left out passes nothing, and
// reads as though it passed something.
func (c *checker) finishSpread(s *spreadArg) bool {
	if s.bound == 0 && len(s.fields) > 0 {
		c.error(s.ast.Pos, "spread of %s names no parameter: none of its fields matches one", s.typ)
		return false
	}
	return true
}
