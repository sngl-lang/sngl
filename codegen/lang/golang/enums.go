package golang

import (
	"fmt"
	"strconv"
	"strings"

	"duckfam.us/sngl/codegen"
	"duckfam.us/sngl/ir"
)

// A value of an enum whose members carry payloads (ir/enums.go) is a struct
// of a tag and a field per member carrying one: the tag is the position of
// the member held, so the struct's zero is the first member's with its
// payload's zero, and two values compare with ==. The tag is unexported,
// which no payload field -- an exported member name -- can collide with. An
// enum carrying nothing is its member's name, as it always was.
func init() {
	codegen.RegisterIntrinsic(langGo, ir.EnumIsIntrinsic, func(args []ir.Expr, tr func(ir.Expr) string) (string, []string) {
		return "(" + tr(args[0]) + ".tag == " + enumTag(args[0].ExprType(), enumMemberArg(args)) + ")", nil
	})
	codegen.RegisterIntrinsic(langGo, ir.EnumPayloadIntrinsic, func(args []ir.Expr, tr func(ir.Expr) string) (string, []string) {
		return tr(args[0]) + "." + ExportName(enumMemberArg(args)), nil
	})
}

func enumMemberArg(args []ir.Expr) string {
	if lit, ok := args[1].(*ir.Literal); ok {
		return lit.Value
	}
	return ""
}

// enumTag is the tag of t's member called name.
func enumTag(t *ir.Type, name string) string {
	ed := ir.PayloadEnum(t)
	return strconv.Itoa(ed.MemberIndex(name))
}

// EmitEnumTypeDecls declares a struct per enum carrying payloads. A generic
// one is declared once per instantiation, which passMonomorphize has made a
// declaration of its own by now.
func EmitEnumTypeDecls(enums []*ir.EnumDef) string {
	var b strings.Builder
	for _, ed := range enums {
		if !ed.Carries() || len(ed.TypeParams) > 0 {
			continue
		}
		fmt.Fprintf(&b, "type %s struct {\n\ttag int\n", ExportName(ed.Name))
		for _, m := range ed.Members {
			if m.Payload != nil {
				fmt.Fprintf(&b, "\t%s %s\n", ExportName(m.Name), IRTypeToGo(m.Payload))
			}
		}
		b.WriteString("}\n\n")
	}
	return b.String()
}

// enumLitGo is the value a literal or a member Ident of a payload enum builds.
func enumLitGo(t *ir.Type, member, payload string) string {
	tag := enumTag(t, member)
	if payload == "" {
		return IRTypeToGo(t) + "{tag: " + tag + "}"
	}
	return IRTypeToGo(t) + "{tag: " + tag + ", " + ExportName(member) + ": " + payload + "}"
}

// enumSelectGo is `v.member` off a value of a payload enum, the option Go
// spells as a pointer: the payload's address where v holds member, nil where
// it holds another. The value is a copy the function took, so its address is
// no alias of the value read.
func enumSelectGo(t *ir.Type, member, operand string) string {
	goT := IRTypeToGo(t)
	payload := IRTypeToGo(ir.MemberPayload(t, member))
	return "func(e " + goT + ") *" + payload + " { if e.tag == " + enumTag(t, member) + " { return &e." + ExportName(member) + " }; return nil }(" + operand + ")"
}
