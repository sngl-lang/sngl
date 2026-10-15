package kotlin

import (
	"strconv"
	"strings"
	"unicode"

	"duckfam.us/sngl/codegen"
	"duckfam.us/sngl/ir"
)

// A copy is written out from the value's type (ir/valuecopy.go). Kotlin
// shares a list, a map and a data class, so a list is copied into a new
// mutable list, a map into a new mutable map and a struct with the data
// class's copy(), as deep as the type holds one. A struct that can hold
// itself is a local function calling itself.
func init() {
	codegen.RegisterIntrinsic(langKt, ir.ValueCopyIntrinsic, func(args []ir.Expr, tr func(ir.Expr) string) (string, []string) {
		if len(args) != 1 {
			return "null", nil
		}
		c := &ktCopier{named: map[*ir.StructDef]string{}}
		return c.copy(args[0].ExprType(), tr(args[0])), nil
	})
}

type ktCopier struct {
	n     int
	named map[*ir.StructDef]string
}

// operand is v ready to have a member called on it: bare where it is a name
// or a path of names, parenthesized otherwise.
func operand(v string) string {
	for _, r := range v {
		if r != '_' && r != '.' && !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			return "(" + v + ")"
		}
	}
	return v
}

func (c *ktCopier) fresh(prefix string) string {
	c.n++
	return prefix + strconv.Itoa(c.n)
}

// copy is the expression copying v, a value of t, which it evaluates once.
func (c *ktCopier) copy(t *ir.Type, v string) string {
	if !ir.HoldsValue(t) || ir.StringReprStruct(t) {
		return v
	}
	switch t.Kind {
	case ir.TypeList:
		e := t.Elems[0]
		if !ir.HoldsValue(e) {
			return operand(v) + ".toMutableList()"
		}
		x := c.fresh("_e")
		return operand(v) + ".map { " + x + " -> " + c.copy(e, x) + " }.toMutableList()"
	case ir.TypeMap:
		if len(t.Elems) < 2 || !ir.HoldsValue(t.Elems[1]) {
			return operand(v) + ".toMutableMap()"
		}
		x := c.fresh("_e")
		return operand(v) + ".mapValues { " + x + " -> " + c.copy(t.Elems[1], x+".value") + " }.toMutableMap()"
	case ir.TypeOption, ir.TypeReadonly:
		x := c.fresh("_o")
		return operand(v) + "?.let { " + x + " -> " + c.copy(t.Elems[0], x) + " }"
	case ir.TypeStruct:
		def := ir.CopiedStruct(t)
		if name, ok := c.named[def]; ok {
			return name + "(" + v + ")"
		}
		fields := ir.CopiedFields(t)
		if len(fields) == 0 {
			return operand(v) + ".copy()"
		}
		if ir.Recursive(t) {
			name := c.fresh("_copy")
			c.named[def] = name
			x := c.fresh("_s")
			body := c.structBody(t, x)
			delete(c.named, def)
			goT := IRTypeToKt(t)
			return "run { fun " + name + "(" + x + ": " + goT + "): " + goT + " { return " + body + " }; " + name + "(" + v + ") }"
		}
		x := c.fresh("_s")
		return operand(v) + ".let { " + x + " -> " + c.structBody(t, x) + " }"
	}
	return v
}

// structBody is the data class copy of x, a value of the struct type t, with
// each field holding a list, a map or a struct copied in turn.
func (c *ktCopier) structBody(t *ir.Type, x string) string {
	var out strings.Builder
	out.WriteString(x + ".copy(")
	for i, f := range ir.CopiedFields(t) {
		if i > 0 {
			out.WriteString(", ")
		}
		name := SafeIdent(f.Field.Name)
		out.WriteString(name + " = " + c.copy(f.Type, x+"."+name))
	}
	return out.String() + ")"
}
