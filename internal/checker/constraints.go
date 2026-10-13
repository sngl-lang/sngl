package checker

import (
	"fmt"

	"duckfam.us/sngl/ast"
	"duckfam.us/sngl/ir"
)

// A type parameter's constraint says what a type bound to it must be. `any`
// says nothing; an anonymous struct says the type is a struct with at least
// its fields, each at an equal type. A named struct satisfies one as readily
// as an anonymous struct -- the constraint is about fields, not identity --
// and a field default on the struct being measured plays no part.

// resolveConstraint resolves what a type parameter's constraint names: `any`,
// found in scope as a scalar is so a program's own `any` shadows it, or an
// anonymous struct, interned as one written anywhere else is.
func (c *checker) resolveConstraint(te ast.TypeExpr) *ir.Type {
	switch t := te.(type) {
	case *ast.NamedType:
		if t.Package == "" && len(t.TypeArgs) == 0 {
			if sym, ok := c.scope.Lookup(t.Name); ok {
				if typ, _, ok := scalarOf(sym); ok && typ == ir.TypAny {
					return typ
				}
			}
		}
	case *ast.StructDef:
		if len(t.TypeParams) == 0 {
			for _, f := range t.Fields() {
				if f.Default != nil {
					c.error(f.Pos, "a constraint's field takes no default: a constraint says what a type has, not what it starts with")
				}
			}
			return c.resolveAnonStruct(t)
		}
	}
	c.error(*te.ExprPos(), "a type parameter's constraint is `any` or an anonymous struct, `struct { name string }`")
	return ir.TypAny
}

// constraintStruct is the anonymous struct a constraint names, nil for one
// that says nothing.
func constraintStruct(constraint *ir.Type) *ir.StructDef {
	if constraint == nil || constraint.Kind != ir.TypeStruct {
		return nil
	}
	sd, _ := constraint.Decl.(*ir.StructDef)
	return sd
}

// paramConstraint is the struct constraint of the type parameter t is, nil
// where t is none or its parameter's constraint says nothing. In a generic
// body this is what a select and a spread of a T read.
func paramConstraint(t *ir.Type) *ir.StructDef {
	if t == nil || t.Kind != ir.TypeTypeParam {
		return nil
	}
	sym, _ := t.Decl.(*ir.TypeParamSym)
	if sym == nil {
		return nil
	}
	return constraintStruct(sym.Constraint)
}

// unsatisfied says why t does not satisfy constraint, "" where it does. A dyn
// says nothing to measure, and neither does a type parameter no declaration
// constrains -- one a substitution made rather than a scope -- so both pass.
// A type parameter is measured by its own constraint: a T constrained to more
// fields than a U asks for may be handed on to U.
func unsatisfied(t, constraint *ir.Type) string {
	want := constraintStruct(constraint)
	if want == nil || t == nil {
		return ""
	}
	var fields []*ir.StructField
	var bindings map[string]*ir.Type
	switch t.Kind {
	case ir.TypeDyn, ir.TypeInvalid:
		return ""
	case ir.TypeStruct:
		sd, _ := t.Decl.(*ir.StructDef)
		if sd == nil {
			return ""
		}
		fields = sd.Fields
		if len(sd.TypeParams) > 0 && len(t.Elems) == len(sd.TypeParams) {
			bindings = make(map[string]*ir.Type, len(sd.TypeParams))
			for i, tp := range sd.TypeParams {
				bindings[tp.Name] = t.Elems[i]
			}
		}
	case ir.TypeTypeParam:
		if sym, _ := t.Decl.(*ir.TypeParamSym); sym == nil {
			return ""
		}
		sd := paramConstraint(t)
		if sd == nil {
			return "its constraint says nothing about its fields"
		}
		fields = sd.Fields
	default:
		return "it is no struct"
	}
	for _, w := range want.Fields {
		var got *ir.StructField
		for _, f := range fields {
			if f.Name == w.Name {
				got = f
				break
			}
		}
		if got == nil {
			return fmt.Sprintf("it has no field %q", w.Name)
		}
		ft := got.Type
		if bindings != nil {
			ft = ft.Substitute(bindings)
		}
		if !ft.Equal(w.Type) {
			return fmt.Sprintf("its field %q is %s, not %s", w.Name, ft, w.Type)
		}
	}
	return ""
}

// checkConstraint reports at pos a type bound to tp that does not satisfy its
// constraint. Every place a type parameter is bound asks this.
func (c *checker) checkConstraint(pos ast.Pos, tp ir.TypeParam, t *ir.Type) {
	if why := unsatisfied(t, tp.Constraint); why != "" {
		c.error(pos, "%s does not satisfy %s, the constraint on %s: %s", t, tp.Constraint, tp.Name, why)
	}
}

// checkConstraints is checkConstraint for each of params bindings binds.
func (c *checker) checkConstraints(pos ast.Pos, params []ir.TypeParam, bindings map[string]*ir.Type) {
	for _, tp := range params {
		if t, ok := bindings[tp.Name]; ok {
			c.checkConstraint(pos, tp, t)
		}
	}
}
