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
//
// A constraint's field may name another type parameter of the same list, or
// the one it constrains: `[T any, U struct { value T }]`. It is measured with
// what the list's other parameters are bound to, and where one is not, the
// field of the type bound to U is what binds it (inferFromConstraints).

// resolveConstraint resolves what a type parameter's constraint names: `any`,
// found in scope as a scalar is so a program's own `any` shadows it, or an
// anonymous struct. The list's parameters are in scope, so a field may name
// one, which makes it an anonymous struct like any other naming a type
// parameter: declared un-interned (anonStructType), its fields meaning this
// list's parameters and no other.
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
			return c.anonStructType(c.buildStructDef(t).Fields)
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
	switch t.Kind {
	case ir.TypeInvalid:
		return ""
	case ir.TypeTypeParam:
		if sym, _ := t.Decl.(*ir.TypeParamSym); sym == nil {
			return ""
		}
		if paramConstraint(t) == nil {
			return "its constraint says nothing about its fields"
		}
	case ir.TypeStruct:
		if t.Decl == nil {
			return ""
		}
	}
	fields, ok := ir.FieldsOf(t)
	if !ok {
		return "it is no struct"
	}
	for _, w := range want.Fields {
		got := ir.FieldNamed(fields, w.Name)
		if got == nil {
			return fmt.Sprintf("it has no field %q", w.Name)
		}
		if !got.Type.Equal(w.Type) {
			return fmt.Sprintf("its field %q is %s, not %s", w.Name, got.Type, w.Type)
		}
	}
	return ""
}

// inferFromConstraints binds the parameters of params a constraint's fields
// name from the fields of the type bound to the parameter it constrains:
// `[T any, U struct { value T }]` with U a struct whose value is an int binds
// T to int. Repeated until nothing more binds, since what one binds may be
// what another's constraint is measured against. Nothing already bound is
// rebound, and only params' own names are: a field may name a parameter of
// an enclosing declaration, which this call does not bind.
func inferFromConstraints(params []ir.TypeParam, bindings map[string]*ir.Type) {
	own := make(map[string]bool, len(params))
	for _, tp := range params {
		own[tp.Name] = true
	}
	for changed := true; changed; {
		changed = false
		for _, tp := range params {
			want := constraintStruct(tp.Constraint)
			t := bindings[tp.Name]
			if want == nil || t == nil {
				continue
			}
			fields, ok := ir.FieldsOf(t)
			if !ok {
				continue
			}
			for _, w := range want.Fields {
				got := ir.FieldNamed(fields, w.Name)
				if got == nil || !w.Type.HasTypeParam() {
					continue
				}
				found := map[string]*ir.Type{}
				ir.BindTypeParams(w.Type, got.Type, found)
				for name, b := range found {
					if _, bound := bindings[name]; !bound && own[name] && b.Kind != ir.TypeInvalid {
						bindings[name] = b
						changed = true
					}
				}
			}
		}
	}
}

// checkConstraint reports at pos a type bound to tp that does not satisfy its
// constraint, measured with the list's other parameters bound as bindings
// says. Every place a type parameter is bound asks this.
func (c *checker) checkConstraint(pos ast.Pos, tp ir.TypeParam, t *ir.Type, bindings map[string]*ir.Type) {
	con := tp.Constraint.Substitute(bindings)
	if why := unsatisfied(t, con); why != "" {
		c.error(pos, "%s does not satisfy %s, the constraint on %s: %s", t, con, tp.Name, why)
	}
}

// checkConstraints is checkConstraint for each of params bindings binds,
// once what the constraints themselves bind has been bound.
func (c *checker) checkConstraints(pos ast.Pos, params []ir.TypeParam, bindings map[string]*ir.Type) {
	inferFromConstraints(params, bindings)
	for _, tp := range params {
		if t, ok := bindings[tp.Name]; ok {
			c.checkConstraint(pos, tp, t, bindings)
		}
	}
}
