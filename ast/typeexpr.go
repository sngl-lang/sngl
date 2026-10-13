package ast

// TypeFromExpr reads an expression written where a type argument goes as the
// type it spells: a name, a qualified name, or either with arguments of its
// own, `option[User]`. A call site cannot tell a type from a value until the
// checker knows what the bracket is on, so a type argument is parsed as the
// expression its name already is. False for anything a type is not written as.
func TypeFromExpr(e Expr) (TypeExpr, bool) {
	switch x := e.(type) {
	case *IdentExpr:
		return &NamedType{Pos: x.Pos, Name: x.Name}, true
	case *SelectExpr:
		pkg, ok := x.Operand.(*IdentExpr)
		if !ok {
			return nil, false
		}
		return &NamedType{Pos: pkg.Pos, Package: pkg.Name, Name: x.Field}, true
	case *IndexExpr:
		t, ok := TypeFromExpr(x.Operand)
		if !ok {
			return nil, false
		}
		nt := t.(*NamedType)
		if len(nt.TypeArgs) > 0 {
			return nil, false
		}
		for _, ix := range x.Indices {
			arg, ok := TypeFromExpr(ix)
			if !ok {
				return nil, false
			}
			nt.TypeArgs = append(nt.TypeArgs, arg)
		}
		return nt, true
	}
	return nil, false
}

// ExprFromType is TypeFromExpr's inverse, for a printer writing a type where
// an expression goes. False for a type no expression spells.
func ExprFromType(t TypeExpr) (Expr, bool) {
	nt, ok := t.(*NamedType)
	if !ok {
		return nil, false
	}
	var e Expr = &IdentExpr{Pos: nt.Pos, Name: nt.Name}
	if nt.Package != "" {
		e = &SelectExpr{Pos: nt.Pos, Operand: &IdentExpr{Pos: nt.Pos, Name: nt.Package}, Field: nt.Name}
	}
	if len(nt.TypeArgs) == 0 {
		return e, true
	}
	ix := &IndexExpr{Pos: nt.Pos, Operand: e}
	for _, a := range nt.TypeArgs {
		ae, ok := ExprFromType(a)
		if !ok {
			return nil, false
		}
		ix.Indices = append(ix.Indices, ae)
	}
	return ix, true
}
