package javascript

import (
	"strconv"
	"strings"

	"duckfam.us/sngl/codegen"
	"duckfam.us/sngl/ir"
)

// A copy is written out from the value's type (ir/valuecopy.go): JavaScript
// shares every object and array, so a struct is spread into a new object and
// a list mapped into a new array, as deep as the type holds one. A struct
// that can hold itself is a named function expression calling itself.
func init() {
	codegen.RegisterIntrinsic(langJS, ir.ValueCopyIntrinsic, func(args []ir.Expr, tr func(ir.Expr) string) (string, []string) {
		if len(args) != 1 {
			return "undefined", nil
		}
		c := &jsCopier{named: map[*ir.StructDef]string{}}
		return c.copy(args[0].ExprType(), tr(args[0])), nil
	})
}

type jsCopier struct {
	n     int
	named map[*ir.StructDef]string
}

func (c *jsCopier) fresh(prefix string) string {
	c.n++
	return prefix + strconv.Itoa(c.n)
}

// copy is the expression copying v, a value of t, which it evaluates once.
func (c *jsCopier) copy(t *ir.Type, v string) string {
	if !ir.HoldsValue(t) {
		return v
	}
	switch t.Kind {
	case ir.TypeList:
		e := t.Elems[0]
		if !ir.HoldsValue(e) {
			return "[..." + v + "]"
		}
		x := c.fresh("_e")
		return v + ".map(" + x + " => " + c.copy(e, x) + ")"
	case ir.TypeMap:
		if len(t.Elems) < 2 || !ir.HoldsValue(t.Elems[1]) {
			return "new Map(" + v + ")"
		}
		k, x := c.fresh("_k"), c.fresh("_e")
		return "new Map([..." + v + "].map(([" + k + ", " + x + "]) => [" + k + ", " + c.copy(t.Elems[1], x) + "]))"
	case ir.TypeOption, ir.TypeReadonly:
		x := c.fresh("_o")
		return "((" + x + ") => " + x + " == null ? " + x + " : " + c.copy(t.Elems[0], x) + ")(" + v + ")"
	case ir.TypeStruct:
		def := ir.CopiedStruct(t)
		if name, ok := c.named[def]; ok {
			return name + "(" + v + ")"
		}
		if ir.Recursive(t) {
			name := c.fresh("_copy")
			c.named[def] = name
			x := c.fresh("_s")
			body := c.structBody(t, x)
			delete(c.named, def)
			return "(function " + name + "(" + x + ") { return " + body + "; })(" + v + ")"
		}
		fields := ir.CopiedFields(t)
		if len(fields) == 0 {
			return "({..." + v + "})"
		}
		x := c.fresh("_s")
		return "((" + x + ") => " + c.structBody(t, x) + ")(" + v + ")"
	}
	return v
}

// structBody is the object literal copying x, a value of the struct type t.
func (c *jsCopier) structBody(t *ir.Type, x string) string {
	def := ir.CopiedStruct(t)
	var out strings.Builder
	out.WriteString("({..." + x)
	for _, f := range ir.CopiedFields(t) {
		key := jsFieldKey(def, f.Field.Name)
		out.WriteString(", " + key + ": " + c.copy(f.Type, x+"."+key))
	}
	return out.String() + "})"
}
