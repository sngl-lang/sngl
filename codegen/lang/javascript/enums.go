package javascript

import (
	"strconv"
	"strings"

	"duckfam.us/sngl/ast"
	"duckfam.us/sngl/codegen"
	"duckfam.us/sngl/ir"
)

// A value of an enum whose members carry payloads (ir/enums.go) is
// `{tag, value}`: the member it holds, by name, and that member's payload,
// absent for a member carrying none. An enum carrying nothing is its member's
// name, as it always was.
func init() {
	codegen.RegisterIntrinsic(langJS, ir.EnumIsIntrinsic, func(args []ir.Expr, tr func(ir.Expr) string) (string, []string) {
		return "(" + tr(args[0]) + ".tag === " + strconv.Quote(memberArg(args)) + ")", nil
	})
	codegen.RegisterIntrinsic(langJS, ir.EnumPayloadIntrinsic, func(args []ir.Expr, tr func(ir.Expr) string) (string, []string) {
		return tr(args[0]) + ".value", nil
	})
}

// memberArg is the member an enum read names, the literal its second argument
// is.
func memberArg(args []ir.Expr) string {
	if lit, ok := args[1].(*ir.Literal); ok {
		return lit.Value
	}
	return ""
}

// enumLitJS is the value a literal or a member Ident of a payload enum builds.
func enumLitJS(member, payload string) string {
	if payload == "" {
		return "{tag: " + strconv.Quote(member) + "}"
	}
	return "{tag: " + strconv.Quote(member) + ", value: " + payload + "}"
}

// enumSelectJS is `v.member` off a value of a payload enum: its payload where
// v holds member, and null where it holds another. The value is bound once.
func enumSelectJS(member, operand string) string {
	return "((e) => e.tag === " + strconv.Quote(member) + " ? e.value : null)(" + operand + ")"
}

// enumEqualJS is == or != between two values of a payload enum, or options of
// one: the same member and equal payloads, compared as each payload's type
// compares, since JavaScript's === is an object's identity.
func enumEqualJS(n *ir.Binary, left, right string) (string, bool) {
	lt, rt := n.Left.ExprType(), n.Right.ExprType()
	t := optionElemJS(lt)
	if ir.PayloadEnum(t) == nil {
		t = optionElemJS(rt)
	}
	if ir.PayloadEnum(t) == nil || (n.Op != ast.BinEq && n.Op != ast.BinNeq) {
		return "", false
	}
	if lt.Kind == ir.TypeNull || rt.Kind == ir.TypeNull {
		return "", false
	}
	eq := "((a, b) => a == null || b == null ? a === b : " + jsEqualExpr(t, "a", "b") + ")(" + left + ", " + right + ")"
	if n.Op == ast.BinNeq {
		return "!" + eq, true
	}
	return eq, true
}

func optionElemJS(t *ir.Type) *ir.Type {
	if t != nil && t.Kind == ir.TypeOption && len(t.Elems) == 1 {
		return t.Elems[0]
	}
	return t
}

// jsEqualExpr is SNGL's == between a and b, two values of t, written out from
// t: a struct field by field, a payload enum member and payload, and
// anything else the host's ===. Only the types ir.RebuildIncomparable admits
// reach it.
func jsEqualExpr(t *ir.Type, a, b string) string {
	if ed := ir.PayloadEnum(t); ed != nil {
		var arms []string
		for _, m := range ed.Members {
			if m.Payload == nil {
				continue
			}
			arms = append(arms, a+".tag === "+strconv.Quote(m.Name)+" ? "+jsEqualExpr(ir.MemberPayload(t, m.Name), a+".value", b+".value"))
		}
		payloads := "true"
		if len(arms) > 0 {
			payloads = "(" + strings.Join(arms, " : ") + " : true)"
		}
		return "(" + a + ".tag === " + b + ".tag && " + payloads + ")"
	}
	if ud := ir.UnitDeclOf(t); ud != nil && !ud.IsSingleBase() {
		return multiBaseUnitEqualJS(ud, ast.BinEq, a, b)
	}
	if def := ir.CopiedStruct(t); def != nil {
		bind := map[string]*ir.Type{}
		for i, tp := range def.TypeParams {
			if i < len(t.Elems) {
				bind[tp.Name] = t.Elems[i]
			}
		}
		if len(def.Fields) == 0 {
			return "true"
		}
		parts := make([]string, 0, len(def.Fields))
		for _, f := range def.Fields {
			ft := f.Type
			if len(bind) > 0 {
				ft = ft.Substitute(bind)
			}
			key := jsFieldKey(def, f.Name)
			parts = append(parts, jsEqualExpr(ft, a+"."+key, b+"."+key))
		}
		return "(" + strings.Join(parts, " && ") + ")"
	}
	return "(" + a + " === " + b + ")"
}
