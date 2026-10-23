package ir

// An enum member may carry a type, its payload, and a value of such an enum
// holds exactly one member and that member's payload with it. An enum whose
// members carry nothing is every enum there was before, and every target
// emits it as it always has; one that carries anything (Carries) is a value
// each target holds as a tag and a payload.
//
// The IR spells what a program does with one in constructs every walk already
// reaches, so no pass needed to learn a node:
//
//   - a value holding a member with a payload is a StructLit of the enum's
//     type naming that one member, `{ok=v}`, which is how it is written; one
//     holding a member with none is the member Ident, as it always was;
//   - a read of a member with a payload, `r.ok`, is a Select typed
//     option[payload], present only where r holds that member, so the null
//     test that narrows any option narrows it;
//   - whether r holds a member is a call of EnumIsIntrinsic, and the payload
//     of the member r is known to hold a call of EnumPayloadIntrinsic. A
//     match arm is taken on the first and binds the second.

// EnumIsIntrinsic is the id of the test whether an enum value holds a member:
// `enum.is(r, "ok")`.
const EnumIsIntrinsic = "enum.is"

// EnumPayloadIntrinsic is the id of the read of the payload of the member an
// enum value is known to hold: `enum.payload(r, "ok")`. What it answers where
// r holds another member is unspecified; the IR writes it only where the
// member is the one held.
const EnumPayloadIntrinsic = "enum.payload"

// Carries reports whether a member of e carries a payload.
func (e *EnumDef) Carries() bool {
	if e == nil {
		return false
	}
	for _, m := range e.Members {
		if m.Payload != nil {
			return true
		}
	}
	return false
}

// Member is e's member called name, or nil.
func (e *EnumDef) Member(name string) *EnumMember {
	if e == nil {
		return nil
	}
	for _, m := range e.Members {
		if m.Name == name {
			return m
		}
	}
	return nil
}

// MemberIndex is the position of e's member called name, or -1.
func (e *EnumDef) MemberIndex(name string) int {
	if e == nil {
		return -1
	}
	for i, m := range e.Members {
		if m.Name == name {
			return i
		}
	}
	return -1
}

// PayloadEnum is t's declaration when t is an enum some member of which
// carries a payload, and nil for any other type.
func PayloadEnum(t *Type) *EnumDef {
	if t == nil || t.Kind != TypeEnum {
		return nil
	}
	if ed, ok := t.Decl.(*EnumDef); ok && ed.Carries() {
		return ed
	}
	return nil
}

// MemberPayload is the type member carries in a value of t, the enum's type
// parameters bound to t's arguments; nil for a member with no payload.
func MemberPayload(t *Type, member string) *Type {
	ed, _ := t.Decl.(*EnumDef)
	m := ed.Member(member)
	if m == nil || m.Payload == nil {
		return nil
	}
	return m.Payload.Substitute(EnumBindings(t))
}

// EnumBindings binds an enum's type parameters to t's arguments, or is nil
// for an enum that has none.
func EnumBindings(t *Type) map[string]*Type {
	ed, _ := t.Decl.(*EnumDef)
	if ed == nil || len(ed.TypeParams) == 0 || len(t.Elems) == 0 {
		return nil
	}
	out := make(map[string]*Type, len(ed.TypeParams))
	for i, tp := range ed.TypeParams {
		if i < len(t.Elems) {
			out[tp.Name] = t.Elems[i]
		}
	}
	return out
}

// EnumIs is the test whether e, a value of a payload enum, holds member.
func EnumIs(e Expr, member string) *Call {
	t := e.ExprType()
	return &Call{
		Type: TypBool,
		Func: &Func{
			Name:      "is",
			Intrinsic: EnumIsIntrinsic,
			Params:    []*Param{{Name: "v", Type: t}, {Name: "member", Type: TypString}},
			Return:    TypBool,
			Purity:    PurityPure,
		},
		Args: []CallArg{{Value: e}, {Value: &Literal{Type: TypString, Value: member}}},
	}
}

// EnumPayloadRead is the payload of member, which e is known to hold.
func EnumPayloadRead(e Expr, member string) *Call {
	t := e.ExprType()
	payload := MemberPayload(t, member)
	return &Call{
		Type: payload,
		Func: &Func{
			Name:      "payload",
			Intrinsic: EnumPayloadIntrinsic,
			Params:    []*Param{{Name: "v", Type: t}, {Name: "member", Type: TypString}},
			Return:    payload,
			Purity:    PurityPure,
		},
		Args: []CallArg{{Value: e}, {Value: &Literal{Type: TypString, Value: member}}},
	}
}

// EnumRead is the operand and member of a call EnumIs or EnumPayloadRead
// made, and which of the two it is.
func EnumRead(e Expr) (operand Expr, member, intrinsic string, ok bool) {
	c, isCall := e.(*Call)
	if !isCall || c.Func == nil || len(c.Args) != 2 {
		return nil, "", "", false
	}
	if c.Func.Intrinsic != EnumIsIntrinsic && c.Func.Intrinsic != EnumPayloadIntrinsic {
		return nil, "", "", false
	}
	lit, isLit := c.Args[1].Value.(*Literal)
	if !isLit {
		return nil, "", "", false
	}
	return c.Args[0].Value, lit.Value, c.Func.Intrinsic, true
}

// EnumLitMember is the member a value of a payload enum holds where e
// writes one -- a struct literal naming it, or the member's Ident -- with its
// payload, nil for a member that carries none.
func EnumLitMember(e Expr) (member string, payload Expr, ok bool) {
	switch x := e.(type) {
	case *StructLit:
		if PayloadEnum(x.Type) == nil || len(x.Fields) != 1 {
			return "", nil, false
		}
		return x.Fields[0].Name, x.Fields[0].Value, true
	case *Ident:
		if PayloadEnum(x.Type) == nil || x.Member == "" {
			return "", nil, false
		}
		return x.Member, nil, true
	}
	return "", nil, false
}
