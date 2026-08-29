package checker

import (
	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/ir"
)

// synthRecvTypeExpr returns an ast.TypeExpr referring to `name`, with type
// parameters instantiated as themselves. Used to produce the synthetic `this`
// param's declared type when desugaring nested methods.
func synthRecvTypeExpr(pos ast.Pos, name string, typeParams []string) ast.TypeExpr {
	nt := &ast.NamedType{Pos: pos, Name: name}
	if len(typeParams) > 0 {
		args := make([]ast.TypeExpr, len(typeParams))
		for i, tp := range typeParams {
			args[i] = &ast.NamedType{Pos: pos, Name: tp}
		}
		nt.TypeArgs = args
	}
	return nt
}

// currentRecvType returns the IR type of `this` in the innermost active scope,
// or nil if `this` is not bound (i.e. we're not inside a method body).
func (c *checker) currentRecvType() *ir.Type {
	sym, ok := c.scope.Lookup(ir.ReceiverParam)
	if !ok {
		return nil
	}
	p, ok := sym.(*ir.Param)
	if !ok {
		return nil
	}
	return p.Type
}

// isFieldOrMember reports whether the receiver type t carries a member named
// `name` that is addressable via `this.<name>`: struct field, enum member,
// or component var.
func isFieldOrMember(t *ir.Type, name string) bool {
	if t == nil || t.Decl == nil {
		return false
	}
	switch d := t.Decl.(type) {
	case *ir.StructDef:
		for _, f := range d.Fields {
			if f.Name == name {
				return true
			}
		}
	case *ir.EnumDef:
		for _, m := range d.Members {
			if m.Name == name {
				return true
			}
		}
	case *ir.Component:
		for _, v := range d.Vars {
			if v.Name == name {
				return true
			}
		}
	}
	return false
}

// recvTypeName returns the declared name of a receiver type (struct, enum, or
// component). Empty for any other kind.
func recvTypeName(t *ir.Type) string {
	if t == nil || t.Decl == nil {
		return ""
	}
	switch d := t.Decl.(type) {
	case *ir.StructDef:
		return d.Name
	case *ir.EnumDef:
		return d.Name
	case *ir.Component:
		return d.Name
	}
	return ""
}

// elideThis rewrites a bare identifier as `this.<name>` when:
//   - we're inside a method body (currentRecvType != nil),
//   - the name is not otherwise in scope, AND
//   - the receiver type has a field/member/var or method with that name.
//
// Returns the rewritten SelectExpr or nil if elision does not apply.
func (c *checker) elideThis(ident *ast.IdentExpr) *ast.SelectExpr {
	recv := c.currentRecvType()
	if recv == nil {
		return nil
	}
	if _, ok := c.scope.Lookup(ident.Name); ok {
		return nil
	}
	hit := isFieldOrMember(recv, ident.Name)
	if !hit {
		if name := recvTypeName(recv); name != "" {
			if _, ok := c.lookupMethod(name, ident.Name); ok {
				// A sibling method on a COMPONENT receiver must NOT desugar to a
				// `this.method` selector: component methods are emitted as free
				// functions (`main_foo(state)`) / Model methods (`m.foo()`), not
				// as methods on the receiver value. Returning nil routes it
				// through inferIdent's currentComponent path, which yields a
				// receiver-stripped func ref → Pattern A call (codegen supplies
				// the per-instance receiver). Struct/enum methods keep `this.method`.
				if _, isComp := recv.Decl.(*ir.Component); isComp {
					return nil
				}
				hit = true
			}
		}
	}
	if !hit {
		return nil
	}
	return &ast.SelectExpr{
		Pos:     ident.Pos,
		Operand: &ast.IdentExpr{Pos: ident.Pos, Name: ir.ReceiverParam},
		Field:   ident.Name,
	}
}

// hasMemberLikeName reports whether the IR type symbol has a field/member/var
// of the given name — covering struct fields, enum members, and component vars.
func hasMemberLikeName(d ir.Symbol, name string) bool {
	switch x := d.(type) {
	case *ir.StructDef:
		for _, f := range x.Fields {
			if f.Name == name {
				return true
			}
		}
	case *ir.EnumDef:
		for _, m := range x.Members {
			if m.Name == name {
				return true
			}
		}
	case *ir.Component:
		for _, v := range x.Vars {
			if v.Name == name {
				return true
			}
		}
	}
	return false
}

func typeKindNoun(d ir.Symbol) string {
	switch d.(type) {
	case *ir.StructDef:
		return "struct"
	case *ir.EnumDef:
		return "enum"
	case *ir.Component:
		return "component"
	}
	return "type"
}

// registerNestedMethods desugars each nested *ast.FuncDef into a top-level
// method form (prepends a synthetic `this` param, sets the receiver type name)
// and registers via the existing symtab.RegisterMethod path.
//
// recvName is the type name (struct/enum/component).
// typeParams is the receiver type's type parameters (empty for enum/component).
func (c *checker) registerNestedMethods(recvName string, typeParams []string, nested []*ast.FuncDef) []*ir.Func {
	out := make([]*ir.Func, 0, len(nested))
	for _, n := range nested {
		// Funcs with an explicit method receiver (e.g. `func int.double`
		// inside a component body) are NOT desugared onto the surrounding
		// type — they remain methods on the named type. They'll be picked
		// up by the normal top-level func path.
		if _, _, isMethod := ast.SplitMethodName(n.Name); isMethod {
			fn := c.buildFunc(n)
			c.applyMarks(n, fn)
			if prev := c.declareMethod(n.Pos, fn); prev != nil {
				c.error(n.Pos, "duplicate declaration of %q on type %s", fn.Name, fn.Receiver)
				continue
			}
			c.pkg.Funcs = append(c.pkg.Funcs, fn)
			out = append(out, fn)
			continue
		}

		// Collision check: field/member/var on the receiver type.
		typeDecl, ok := c.symtab.LookupType(recvName)
		if !ok {
			if comp, ok2 := c.symtab.LookupComponent(recvName); ok2 {
				typeDecl = comp
			}
		}
		if typeDecl != nil && hasMemberLikeName(typeDecl, n.Name) {
			c.error(n.Pos, "duplicate declaration of %q on %s %s", n.Name, typeKindNoun(typeDecl), recvName)
			continue
		}

		thisType := synthRecvTypeExpr(n.Pos, recvName, typeParams)
		thisParam := ast.Param{
			Pos:  n.Pos,
			Name: ir.ReceiverParam,
			Type: thisType,
		}
		newParams := ast.ParamList{
			Pos:         n.Params.Pos,
			IsMultiline: n.Params.IsMultiline,
			Params:      append([]ast.Param{thisParam}, n.Params.Params...),
		}
		synthetic := &ast.FuncDef{
			Pos:            n.Pos,
			Name:           recvName + "." + n.Name,
			TypeParams:     n.TypeParams,
			RecvTypeParams: append([]string(nil), typeParams...),
			Params:         newParams,
			ReturnType:     n.ReturnType,
			Body:           n.Body,
			Block:          n.Block,
			// The marks are the declaration's, not the shape of its parameter
			// list: desugaring the receiver must not drop what a mark said
			// about the function it was written on.
			Attrs: n.Attrs,
		}
		fn := c.buildFunc(synthetic)
		c.applyMarks(synthetic, fn)
		if prev := c.declareMethod(n.Pos, fn); prev != nil {
			noun := "type"
			if typeDecl != nil {
				noun = typeKindNoun(typeDecl)
			}
			c.error(n.Pos, "duplicate declaration of %q on %s %s", n.Name, noun, recvName)
			continue
		}
		c.pkg.Funcs = append(c.pkg.Funcs, fn)
		out = append(out, fn)
	}
	return out
}
