package c

import (
	"fmt"
	"strings"

	"duckfam.us/sngl/ast"
	"duckfam.us/sngl/ir"
)

// A unit is a magnitude per base, as on Go: a single-base unit is a double,
// and one of several bases -- sngl:ui's `measurement { px, em, rem }` -- a
// struct of a double per base, which its fields are read off by name
// (`m.px`). A literal is reduced to its base, and C, which adds no struct,
// adds and scales one through functions the file defines for the unit.
// A duration is one of a single base, its milliseconds.

// unitDef is t's unit where C writes it, or nil.
func unitDef(t *ir.Type) *ir.UnitDef {
	if t == nil || t.Kind != ir.TypeUnit {
		return nil
	}
	return ir.UnitDeclOf(t)
}

// multiBase reports whether t is a unit of several bases, which C holds as a
// struct.
func multiBase(t *ir.Type) bool {
	ud := unitDef(t)
	return ud != nil && !ud.IsSingleBase()
}

// unitType is t, a unit, as C names it.
func (c *Context) unitType(ud *ir.UnitDef) string {
	if ud.IsSingleBase() {
		return "double"
	}
	name := Decl(&ud.DeclInfo)
	var b strings.Builder
	b.WriteString("typedef struct " + name + " {\n")
	for _, base := range ud.Bases() {
		b.WriteString("\tdouble " + Local(base.Name) + ";\n")
	}
	b.WriteString("} " + name + ";\n")
	c.file.define(name, b.String())
	return name
}

// unitLiteral is lit, a unit literal, reduced to its base.
func (c *Context) unitLiteral(lit *ir.Literal) (string, bool) {
	ud := unitDef(lit.Type)
	if ud == nil {
		return "", false
	}
	mag, base, ok := ir.UnitMagnitude(lit)
	if !ok {
		return "", false
	}
	v := ir.FormatUnitMagnitude(mag)
	if !strings.ContainsAny(v, ".eE") {
		v += ".0"
	}
	if ud.IsSingleBase() {
		return v, true
	}
	return "((" + c.unitType(ud) + "){." + Local(base) + " = " + v + "})", true
}

// unitBinary is n, an operation on a unit of several bases, through the
// function the file defines for it: two of them added, subtracted or
// compared, and one scaled by a number either side of it or divided by one.
func (c *Context) unitBinary(n *ir.Binary, left, right string) (string, bool) {
	lt, rt := n.Left.ExprType(), n.Right.ExprType()
	switch {
	case multiBase(lt) && multiBase(rt):
		switch n.Op {
		case ast.BinAdd:
			return c.unitFunc(unitDef(lt), "add") + "(" + left + ", " + right + ")", true
		case ast.BinSub:
			return c.unitFunc(unitDef(lt), "sub") + "(" + left + ", " + right + ")", true
		case ast.BinEq:
			return c.unitFunc(unitDef(lt), "eq") + "(" + left + ", " + right + ")", true
		case ast.BinNeq:
			return "!" + c.unitFunc(unitDef(lt), "eq") + "(" + left + ", " + right + ")", true
		}
	case multiBase(lt) && n.Op == ast.BinMul:
		return c.unitFunc(unitDef(lt), "scale") + "(" + left + ", (double)(" + right + "))", true
	case multiBase(lt) && n.Op == ast.BinDiv:
		return c.unitFunc(unitDef(lt), "scale") + "(" + left + ", 1.0 / (double)(" + right + "))", true
	case multiBase(rt) && n.Op == ast.BinMul:
		return c.unitFunc(unitDef(rt), "scale") + "(" + right + ", (double)(" + left + "))", true
	case !multiBase(lt) && !multiBase(rt):
		return "", false
	}
	return c.fail("the %s operator on a %s", n.Op, lt), true
}

// unitFunc is the function the file defines for op on ud's values: each
// base's magnitude added, subtracted, scaled or compared.
func (c *Context) unitFunc(ud *ir.UnitDef, op string) string {
	t := c.unitType(ud)
	name := t + "_" + op
	var each []string
	for _, base := range ud.Bases() {
		f := Local(base.Name)
		switch op {
		case "add":
			each = append(each, fmt.Sprintf(".%s = a.%s + b.%s", f, f, f))
		case "sub":
			each = append(each, fmt.Sprintf(".%s = a.%s - b.%s", f, f, f))
		case "scale":
			each = append(each, fmt.Sprintf(".%s = a.%s * k", f, f))
		case "eq":
			each = append(each, fmt.Sprintf("a.%s == b.%s", f, f))
		}
	}
	var text string
	switch op {
	case "eq":
		text = fmt.Sprintf("static bool %s(%s a, %s b) {\n\treturn %s;\n}\n", name, t, t, strings.Join(each, " && "))
	case "scale":
		text = fmt.Sprintf("static %s %s(%s a, double k) {\n\treturn (%s){%s};\n}\n", t, name, t, t, strings.Join(each, ", "))
	default:
		text = fmt.Sprintf("static %s %s(%s a, %s b) {\n\treturn (%s){%s};\n}\n", t, name, t, t, t, strings.Join(each, ", "))
	}
	c.file.helper(name, text)
	return name
}

// unitText is v, a value of ud, as it is displayed: each base it holds a
// magnitude in, joined as the sum the source would write, and a value holding
// none its zero in the first base.
func (c *Context) unitText(ud *ir.UnitDef, v string) string {
	bases := ud.Bases()
	if len(bases) == 0 {
		return c.fail("the text of a unit with no base")
	}
	if ud.IsSingleBase() {
		return fmt.Sprintf("sngl_unit_term(%s, SNGL_STR(%q))", v, bases[0].Name)
	}
	t := c.unitType(ud)
	name := t + "_text"
	var b strings.Builder
	fmt.Fprintf(&b, "static sngl_string %s(%s v) {\n\tsngl_string s = SNGL_STR(\"\");\n", name, t)
	for _, base := range bases {
		f := Local(base.Name)
		fmt.Fprintf(&b, "\tif (v.%s != 0) {\n\t\tif (s.len != 0) {\n\t\t\ts = sngl_concat(s, SNGL_STR(%q));\n\t\t}\n\t\ts = sngl_concat(s, sngl_unit_term(v.%s, SNGL_STR(%q)));\n\t}\n", f, ir.UnitTermSep, f, base.Name)
	}
	fmt.Fprintf(&b, "\treturn s.len != 0 ? s : SNGL_STR(%q);\n}\n", ir.FormatUnitZero(ud))
	c.file.helper(name, b.String())
	return name + "(" + v + ")"
}
