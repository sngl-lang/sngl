package checker

import (
	"strings"

	"duckfam.us/sngl/ast"
	"duckfam.us/sngl/ir"
)

// An enum member may carry a payload (ir/enums.go). A value holding one is
// written as a struct literal naming the member, read by a selector that is
// an option of the payload, and matched by member, binding it.

// literalEnum is the enum a struct literal builds a value of: one it names,
// or, written with no name, the enum expected where it stands.
func (c *checker) literalEnum(x *ast.StructExpr) *ir.EnumDef {
	if c.nativeValues || x.Anon != nil {
		return nil
	}
	if x.Name == "" {
		if c.expected != nil && c.expected.Kind == ir.TypeEnum {
			ed, _ := c.expected.Decl.(*ir.EnumDef)
			return ed
		}
		return nil
	}
	var sym ir.Symbol
	if x.Package != "" {
		nsSym, ok := c.scope.Lookup(x.Package)
		if !ok {
			return nil
		}
		ns, ok := nsSym.(*ir.Namespace)
		if !ok || ns.Pkg == nil {
			return nil
		}
		if sym, ok = ns.Pkg.Symbols.LookupMemberType(x.Name); !ok {
			return nil
		}
	} else if s, ok := c.scope.Lookup(x.Name); ok {
		sym = s
	}
	ed, _ := sym.(*ir.EnumDef)
	return ed
}

// inferEnumLit checks a struct literal building a value of ed: it names one
// member, and that member's payload.
func (c *checker) inferEnumLit(x *ast.StructExpr, ed *ir.EnumDef) ir.Expr {
	invalid := &ir.Literal{Type: TypInvalid}
	typ := ed.SymType()
	if c.expected != nil && c.expected.Kind == ir.TypeEnum && c.expected.Decl == ed {
		typ = c.expected
	}
	if len(x.Fields) != 1 || x.Fields[0].Spread {
		for _, f := range x.Fields {
			c.checkExpr(f.Value)
		}
		c.error(x.Pos, "an enum value holds one member: name one of %s", strings.Join(payloadMembers(ed), ", "))
		return invalid
	}
	f := x.Fields[0]
	m := ed.Member(f.Name)
	if m == nil || !c.enumMember(f.NamePos, ed, f.Name) {
		c.checkExpr(f.Value)
		if m == nil {
			c.error(f.NamePos, "%s has no member %s", ed.Name, f.Name)
		}
		return invalid
	}
	if m.Payload == nil {
		c.checkExpr(f.Value)
		c.error(f.NamePos, "member %s carries no payload: write %s.%s", m.Name, ed.Name, m.Name)
		return invalid
	}
	generic := len(ed.TypeParams) > 0
	if generic && len(typ.Elems) != len(ed.TypeParams) {
		// The enum's arguments are what the payload binds.
		val := c.checkExprExpecting(f.Value, nil)
		bindings := map[string]*ir.Type{}
		ir.BindTypeParams(m.Payload, exprType(val), bindings)
		elems := make([]*ir.Type, len(ed.TypeParams))
		for i, tp := range ed.TypeParams {
			b, ok := bindings[tp.Name]
			if !ok {
				c.error(x.Pos, "cannot infer %s for %s from member %s: write the type, %s[…]{%s=…}", tp.Name, ed.Name, m.Name, ed.Name, m.Name)
				return invalid
			}
			elems[i] = b
		}
		typ = &ir.Type{Kind: ir.TypeEnum, Decl: ed, Elems: elems}
		val = c.coerce(f.Value, val, ir.MemberPayload(typ, m.Name), c.payloadSlot(ed, m))
		return &ir.StructLit{AST: x, Type: typ, Fields: []ir.FieldInit{{Name: m.Name, NamePos: f.NamePos, Value: val}}}
	}
	val := c.checkExprAs(f.Value, ir.MemberPayload(typ, m.Name), c.payloadSlot(ed, m))
	return &ir.StructLit{AST: x, Type: typ, Fields: []ir.FieldInit{{Name: m.Name, NamePos: f.NamePos, Value: val}}}
}

func (c *checker) payloadSlot(ed *ir.EnumDef, m *ir.EnumMember) slot {
	return slot{
		keepShape: true,
		mismatch: func(want, got string) string {
			return "cannot use " + got + " as " + want + " for member " + m.Name + " of " + ed.Name
		},
	}
}

// payloadMembers names the members of ed that carry a payload, which are the
// ones a struct literal may name.
func payloadMembers(ed *ir.EnumDef) []string {
	var out []string
	for _, m := range ed.Members {
		if m.Payload != nil {
			out = append(out, m.Name)
		}
	}
	return out
}

// namesEnumType reports whether e is the enum's own name, `Shape` in
// `Shape.empty`, rather than a value of it.
func namesEnumType(e ir.Expr, ed *ir.EnumDef) bool {
	id, ok := e.(*ir.Ident)
	return ok && id.Member == "" && id.Sym == ir.Symbol(ed)
}

// enumMemberRead checks `v.member`, a read off a value of an enum: whether it
// holds the member, for one with no payload, and otherwise the payload as an
// option, present only where the member is the one held -- which the null
// test that narrows any option narrows.
func (c *checker) enumMemberRead(x *ast.SelectExpr, operandExpr ir.Expr, operand *ir.Type, ed *ir.EnumDef) ir.Expr {
	m := ed.Member(x.Field)
	if m == nil || !c.enumMember(x.Pos, ed, x.Field) {
		if m == nil && isExportedMemberName(x.Field) {
			c.error(x.Pos, "no member %q on enum %s", x.Field, ed.Name)
		}
		return &ir.Select{AST: x, Type: TypInvalid, Operand: operandExpr, Field: x.Field}
	}
	if m.Payload == nil {
		if ed.Carries() {
			call := ir.EnumIs(operandExpr, m.Name)
			return call
		}
		member := &ir.Ident{Type: operand, Name: m.Name, Member: m.Name, Sym: ed}
		return &ir.Binary{Type: TypBool, Op: ast.BinEq, Left: operandExpr, Right: member}
	}
	payload := ir.MemberPayload(operand, m.Name)
	if payload.Kind == ir.TypeOption {
		c.error(x.Pos, "member %s carries an option, which a selector cannot wrap in another: match %s instead", m.Name, exprSpelling(x.Operand))
		return &ir.Select{AST: x, Type: TypInvalid, Operand: operandExpr, Field: x.Field}
	}
	return &ir.Select{AST: x, Type: ir.OptionOf(payload), Operand: operandExpr, Field: x.Field}
}

// exprSpelling is how a diagnostic names an operand: the name it reads, or
// "the value" where it is more than a name.
func exprSpelling(e ast.Expr) string {
	switch x := e.(type) {
	case *ast.IdentExpr:
		return x.Name
	case *ast.SelectExpr:
		return exprSpelling(x.Operand) + "." + x.Field
	}
	return "the value"
}

// refuseIncomparableEnum refuses `==` between values of an enum whose
// payloads do not all compare alike on every target: a value compares as its
// member and that member's payload.
func (c *checker) refuseIncomparableEnum(pos ast.Pos, left, right *ir.Type) {
	for _, t := range []*ir.Type{optionElem(left), optionElem(right)} {
		if ir.PayloadEnum(t) == nil {
			continue
		}
		if bad := ir.RebuildIncomparable(t); bad != "" {
			c.error(pos, "%s cannot be compared with ==: %s does not compare alike on every target", t, bad)
		}
		return
	}
}
