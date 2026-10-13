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
// one; such a constraint is no type any value has and is not interned, since
// its fields mean this list's parameters and no other.
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
			sd := c.buildStructDef(t)
			for _, f := range sd.Fields {
				if hasTypeParam(f.Type) {
					sd.Anon = true
					return sd.SymType()
				}
			}
			return c.internAnonStruct(sd.Fields).SymType()
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

// substituteConstraint is constraint with the type parameters its fields
// name bound as bindings says, the constraint itself where none is.
func substituteConstraint(constraint *ir.Type, bindings map[string]*ir.Type) *ir.Type {
	sd := constraintStruct(constraint)
	if sd == nil || len(bindings) == 0 {
		return constraint
	}
	fields := make([]*ir.StructField, len(sd.Fields))
	changed := false
	for i, f := range sd.Fields {
		ft := f.Type.Substitute(bindings)
		changed = changed || ft != f.Type
		fields[i] = &ir.StructField{Name: f.Name, Type: ft}
	}
	if !changed {
		return constraint
	}
	return (&ir.StructDef{Name: sd.Name, Pkg: sd.Pkg, Fields: fields, Anon: true}).SymType()
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

// fieldsOf is the fields a value of type t is known to have, each at its
// type: a struct's own, its type arguments bound, or the constraint's of a
// type parameter. ok is false for a type that has no fields to measure.
func fieldsOf(t *ir.Type) (fields []*ir.StructField, ok bool) {
	switch t.Kind {
	case ir.TypeStruct:
		sd, _ := t.Decl.(*ir.StructDef)
		if sd == nil {
			return nil, false
		}
		if len(sd.TypeParams) == 0 || len(t.Elems) != len(sd.TypeParams) {
			return sd.Fields, true
		}
		bindings := make(map[string]*ir.Type, len(sd.TypeParams))
		for i, tp := range sd.TypeParams {
			bindings[tp.Name] = t.Elems[i]
		}
		out := make([]*ir.StructField, len(sd.Fields))
		for i, f := range sd.Fields {
			out[i] = &ir.StructField{Name: f.Name, Type: f.Type.Substitute(bindings)}
		}
		return out, true
	case ir.TypeTypeParam:
		if sd := paramConstraint(t); sd != nil {
			return sd.Fields, true
		}
	}
	return nil, false
}

// fieldNamed is the field of fields called name, nil for none.
func fieldNamed(fields []*ir.StructField, name string) *ir.StructField {
	for _, f := range fields {
		if f.Name == name {
			return f
		}
	}
	return nil
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
	case ir.TypeDyn, ir.TypeInvalid:
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
	fields, ok := fieldsOf(t)
	if !ok {
		return "it is no struct"
	}
	for _, w := range want.Fields {
		got := fieldNamed(fields, w.Name)
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
			fields, ok := fieldsOf(t)
			if !ok {
				continue
			}
			for _, w := range want.Fields {
				got := fieldNamed(fields, w.Name)
				if got == nil || !hasTypeParam(w.Type) {
					continue
				}
				found := map[string]*ir.Type{}
				bindTypeParams(w.Type, got.Type, found)
				for name, b := range found {
					if _, bound := bindings[name]; !bound && own[name] && b.Kind != ir.TypeDyn {
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
	con := substituteConstraint(tp.Constraint, bindings)
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
