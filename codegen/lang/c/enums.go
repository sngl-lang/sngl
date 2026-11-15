package c

import (
	"strconv"
	"strings"

	"duckfam.us/sngl/ir"
)

// A payload enum is a struct of its member's position, `tag`, and a field
// per member carrying a payload, as Go's is: its zero is the first member's
// with that member's payload zero, as SNGL's is. A generic one is copied per
// instantiation before it reaches C (monomorphizeEnums). An enum whose
// members carry nothing is the integer passEnum makes it.

// enumType defines ed, a payload enum, as the struct it is.
func (c *Context) enumType(t *ir.Type, ed *ir.EnumDef) string {
	name := Decl(&ed.DeclInfo)
	var b strings.Builder
	var deps []string
	b.WriteString("typedef struct " + name + " {\n\tint64_t tag;\n")
	for _, m := range ed.Members {
		if pt := ir.MemberPayload(t, m.Name); pt != nil {
			ft := c.typeName(pt)
			deps = append(deps, ft)
			b.WriteString("\t" + ft + " " + Local(m.Name) + ";\n")
		}
	}
	b.WriteString("} " + name + ";\n")
	c.file.define(name, b.String(), deps...)
	return name
}

// enumLit is a value of t, a payload enum, holding member with payload, nil
// for a member that carries none.
func (c *Context) enumLit(t *ir.Type, member string, payload string) string {
	ed := ir.PayloadEnum(t)
	tag := strconv.Itoa(ed.MemberIndex(member))
	if payload == "" {
		return "((" + c.typeName(t) + "){.tag = " + tag + "})"
	}
	return "((" + c.typeName(t) + "){.tag = " + tag + ", ." + Local(member) + " = " + payload + "})"
}

// enumIntrinsic renders enum.is and enum.payload, and reports whether n was
// one.
func (c *Context) enumIntrinsic(n *ir.Call) (string, bool) {
	id := n.Func.Intrinsic
	if id != ir.EnumIsIntrinsic && id != ir.EnumPayloadIntrinsic || len(n.Args) != 2 {
		return "", false
	}
	t := n.Args[0].Value.ExprType()
	ed := ir.PayloadEnum(t)
	lit, ok := n.Args[1].Value.(*ir.Literal)
	if ed == nil || !ok {
		return c.fail("%s of a %s", id, t), true
	}
	v := c.EvalExpr(n.Args[0].Value)
	if id == ir.EnumIsIntrinsic {
		return "((" + v + ").tag == " + strconv.Itoa(ed.MemberIndex(lit.Value)) + ")", true
	}
	return "(" + v + ")." + Local(lit.Value), true
}

// enumEqual is whether a and b, values of t, hold one member and its payload
// alike, through a function the file defines for t.
func (c *Context) enumEqual(t *ir.Type, ed *ir.EnumDef, a, b string) string {
	name := c.typeName(t)
	fn := name + "__eq"
	var arms []string
	for i, m := range ed.Members {
		if pt := ir.MemberPayload(t, m.Name); pt != nil {
			f := Local(m.Name)
			arms = append(arms, "\tcase "+strconv.Itoa(i)+":\n\t\treturn "+c.equal(pt, "a."+f, "b."+f)+";\n")
		}
	}
	c.file.helper(fn, "static bool "+fn+"("+name+" a, "+name+" b) {\n\tif (a.tag != b.tag) {\n\t\treturn false;\n\t}\n\tswitch (a.tag) {\n"+strings.Join(arms, "")+"\t}\n\treturn true;\n}\n")
	return fn + "(" + a + ", " + b + ")"
}

// enumCopy is v, a value of t, with the payload it holds copied.
func (c *Context) enumCopy(t *ir.Type, ed *ir.EnumDef, v string) string {
	name := c.typeName(t)
	fn := name + "_copy"
	var arms []string
	for i, m := range ed.Members {
		if pt := ir.MemberPayload(t, m.Name); pt != nil && !ir.SharesNothing(pt) {
			f := Local(m.Name)
			arms = append(arms, "\tcase "+strconv.Itoa(i)+":\n\t\te."+f+" = "+c.copyValue(pt, "e."+f)+";\n\t\tbreak;\n")
		}
	}
	if len(arms) == 0 {
		return v
	}
	c.file.helper(fn, "static "+name+" "+fn+"("+name+" e) {\n\tswitch (e.tag) {\n"+strings.Join(arms, "")+"\t}\n\treturn e;\n}\n")
	return fn + "(" + v + ")"
}
