package kotlin

import (
	"fmt"
	"strings"

	"duckfam.us/sngl/codegen"
	"duckfam.us/sngl/ir"
)

// A value of an enum whose members carry payloads (ir/enums.go) is a sealed
// class: a data class per member carrying one, holding it as `value`, and an
// object per member carrying none. Two values compare with ==, which a data
// class answers member by member. An enum carrying nothing is an enum class,
// as it always was.
func init() {
	codegen.RegisterIntrinsic(langKt, ir.EnumIsIntrinsic, func(args []ir.Expr, tr func(ir.Expr) string) (string, []string) {
		return "(" + tr(args[0]) + " is " + enumVariant(args[0].ExprType(), enumMemberArg(args)) + ")", nil
	})
	codegen.RegisterIntrinsic(langKt, ir.EnumPayloadIntrinsic, func(args []ir.Expr, tr func(ir.Expr) string) (string, []string) {
		return "(" + tr(args[0]) + " as " + enumVariant(args[0].ExprType(), enumMemberArg(args)) + ").value", nil
	})
}

func enumMemberArg(args []ir.Expr) string {
	if lit, ok := args[1].(*ir.Literal); ok {
		return lit.Value
	}
	return ""
}

// enumVariant is the class of t's member called member.
func enumVariant(t *ir.Type, member string) string {
	return EnumName(t.Decl) + "." + variantName(member)
}

// variantName is a member's class: its name, capitalized as a class's is.
// Not upper-cased like an enum class's entry, which would read as a constant.
func variantName(member string) string { return exportName(member) }

// PayloadEnumDecl declares ed, an enum carrying payloads, as a sealed class.
// Each variant prints as its SNGL member, as an enum class's entry does.
func PayloadEnumDecl(ed *ir.EnumDef) string {
	var b strings.Builder
	name := EnumName(ed)
	fmt.Fprintf(&b, "sealed class %s {\n", name)
	for _, m := range ed.Members {
		v := variantName(m.Name)
		if m.Payload == nil {
			fmt.Fprintf(&b, "    data object %s : %s() { override fun toString(): String = %q }\n", v, name, m.Name)
			continue
		}
		fmt.Fprintf(&b, "    data class %s(val value: %s) : %s()\n", v, IRTypeToKt(m.Payload), name)
	}
	b.WriteString("}\n")
	return b.String()
}

// enumLitKt is the value a literal or a member Ident of a payload enum builds.
func enumLitKt(t *ir.Type, member, payload string) string {
	if payload == "" {
		return enumVariant(t, member)
	}
	return enumVariant(t, member) + "(" + payload + ")"
}

// enumZeroKt is a payload enum's zero: its first member, with its payload's
// zero.
func enumZeroKt(t *ir.Type) string {
	ed := ir.PayloadEnum(t)
	first := ed.Members[0]
	if first.Payload == nil {
		return enumLitKt(t, first.Name, "")
	}
	return enumLitKt(t, first.Name, ktZeroFor(ir.MemberPayload(t, first.Name)))
}
