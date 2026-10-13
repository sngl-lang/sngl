package golang

import (
	"strconv"
	"strings"

	"duckfam.us/sngl/codegen"
	"duckfam.us/sngl/ir"
)

// A copy is written out from the value's type (ir/valuecopy.go). Go copies a
// struct on assignment and shares a slice's or a map's storage, so a struct
// holding neither is its own copy, a slice or map of scalars is cloned, and
// anything deeper is a function literal copying it part by part. A struct
// that can hold itself is a function value calling itself.
func init() {
	codegen.RegisterIntrinsic(langGo, ir.ValueCopyIntrinsic, func(args []ir.Expr, tr func(ir.Expr) string) (string, []string) {
		if len(args) != 1 {
			return "nil", nil
		}
		c := &goCopier{named: map[*ir.StructDef]string{}, imports: map[string]bool{}}
		code := c.copy(args[0].ExprType(), tr(args[0]))
		var imports []string
		for _, imp := range []string{"maps", "slices"} {
			if c.imports[imp] {
				imports = append(imports, imp)
			}
		}
		return code, imports
	})
}

type goCopier struct {
	n       int
	named   map[*ir.StructDef]string
	imports map[string]bool
}

func (c *goCopier) fresh(prefix string) string {
	c.n++
	return prefix + strconv.Itoa(c.n)
}

// copy is the expression copying v, a value of t, which it evaluates once.
func (c *goCopier) copy(t *ir.Type, v string) string {
	if ir.SharesNothing(t) {
		return v
	}
	switch t.Kind {
	case ir.TypeList:
		e := t.Elems[0]
		if ir.SharesNothing(e) {
			c.imports["slices"] = true
			return "slices.Clone(" + v + ")"
		}
		goT := IRTypeToGo(t)
		s, out, i, x := c.fresh("_s"), c.fresh("_out"), c.fresh("_i"), c.fresh("_e")
		return "func(" + s + " " + goT + ") " + goT + " { if " + s + " == nil { return nil }; " +
			out + " := make(" + goT + ", len(" + s + ")); for " + i + ", " + x + " := range " + s + " { " +
			out + "[" + i + "] = " + c.copy(e, x) + " }; return " + out + " }(" + v + ")"
	case ir.TypeMap:
		if len(t.Elems) < 2 || ir.SharesNothing(t.Elems[1]) {
			c.imports["maps"] = true
			return "maps.Clone(" + v + ")"
		}
		goT := IRTypeToGo(t)
		m, out, k, x := c.fresh("_m"), c.fresh("_out"), c.fresh("_k"), c.fresh("_e")
		return "func(" + m + " " + goT + ") " + goT + " { if " + m + " == nil { return nil }; " +
			out + " := make(" + goT + ", len(" + m + ")); for " + k + ", " + x + " := range " + m + " { " +
			out + "[" + k + "] = " + c.copy(t.Elems[1], x) + " }; return " + out + " }(" + v + ")"
	case ir.TypeOption, ir.TypeReadonly:
		goT := IRTypeToGo(t)
		p, x := c.fresh("_p"), c.fresh("_v")
		return "func(" + p + " " + goT + ") " + goT + " { if " + p + " == nil { return nil }; " +
			x + " := " + c.copy(t.Elems[0], "*"+p) + "; return &" + x + " }(" + v + ")"
	case ir.TypeStruct:
		def := ir.CopiedStruct(t)
		if name, ok := c.named[def]; ok {
			return name + "(" + v + ")"
		}
		goT := IRTypeToGo(t)
		if ir.Recursive(t) {
			name := c.fresh("_copy")
			c.named[def] = name
			x := c.fresh("_s")
			body := c.structBody(t, x)
			delete(c.named, def)
			return "func() " + goT + " { var " + name + " func(" + goT + ") " + goT + "; " +
				name + " = func(" + x + " " + goT + ") " + goT + " { " + body + "return " + x + " }; return " + name + "(" + v + ") }()"
		}
		x := c.fresh("_s")
		return "func(" + x + " " + goT + ") " + goT + " { " + c.structBody(t, x) + "return " + x + " }(" + v + ")"
	}
	return v
}

// structBody is the statements replacing each field of x, a copy of a value
// of the struct type t, that shares storage with the original.
func (c *goCopier) structBody(t *ir.Type, x string) string {
	var out strings.Builder
	for _, f := range ir.CopiedFields(t) {
		if ir.SharesNothing(f.Type) {
			continue
		}
		field := x + "." + ExportName(f.Field.Name)
		out.WriteString(field + " = " + c.copy(f.Type, field) + "; ")
	}
	return out.String()
}
