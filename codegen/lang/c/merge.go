package c

import (
	"strings"

	"duckfam.us/sngl/ir"
)

// merge is a call of a helper lower's FlattenStructSpread synthesized for a
// spread it could not read, which the file defines where it is first called:
// __merge_<Struct> writes each field of ov not at its zero over base, and a
// MergeSet's writes its one field whatever it holds.
func (c *Context) merge(n *ir.Call, args []string) (string, bool) {
	fn := n.Func
	if !fn.Synthesized || len(fn.Block) > 0 || len(fn.Params) != 2 || len(args) != 2 {
		return "", false
	}
	name := funcName(fn)
	if strings.HasPrefix(fn.Name, "__merge_") {
		t := fn.Params[0].Type
		sd, ok := t.Decl.(*ir.StructDef)
		if !ok || t.Kind != ir.TypeStruct {
			return "", false
		}
		typ := c.typeName(t)
		var b strings.Builder
		b.WriteString("static " + typ + " " + name + "(" + typ + " base, " + typ + " ov) {\n")
		for _, f := range sd.Fields {
			v := "ov." + Local(f.Name)
			b.WriteString("\tif (" + c.notZero(f.Type, v) + ") {\n\t\tbase." + Local(f.Name) + " = " + v + ";\n\t}\n")
		}
		b.WriteString("\treturn base;\n}\n")
		c.file.helper(name, b.String())
		return name + "(" + args[0] + ", " + args[1] + ")", true
	}
	if c.Ctx == nil || c.Ctx.Pkg == nil {
		return "", false
	}
	for _, set := range c.Ctx.Pkg.MergeSets {
		if ir.MergeSetName(set) != fn.Name {
			continue
		}
		typ, ft := c.typeName(fn.Params[0].Type), c.typeName(fn.Params[1].Type)
		c.file.helper(name, "static "+typ+" "+name+"("+typ+" base, "+ft+" v) {\n\tbase."+Local(set.Field)+" = v;\n\treturn base;\n}\n")
		return name + "(" + args[0] + ", " + args[1] + ")", true
	}
	return "", false
}

// notZero is whether v, a value of t, is not t's zero, which is what a spread
// writes: SNGL's zero, so an enum's first member and a struct's defaults.
func (c *Context) notZero(t *ir.Type, v string) string {
	switch t.Kind {
	case ir.TypeOption:
		if c.ptrOption(t) {
			return "(" + v + ") != NULL"
		}
		return "(" + v + ").ok"
	case ir.TypeList, ir.TypeMap:
		return "(" + v + ").len > 0"
	case ir.TypeString:
		return "(" + v + ").len != 0"
	case ir.TypeFunc:
		return "(" + v + ").fn != NULL"
	case ir.TypeBool:
		return v
	case ir.TypeInt, ir.TypeFloat:
		return "(" + v + ") != 0"
	case ir.TypeEnum:
		if ir.PayloadEnum(t) == nil {
			return "(" + v + ") != 0"
		}
	case ir.TypeUnit:
		if !multiBase(t) {
			return "(" + v + ") != 0"
		}
	}
	zero := ir.ZeroExpr(t)
	if zero == nil {
		return c.fail("spreading a %s", t)
	}
	return "!" + c.equal(t, v, c.EvalExpr(zero))
}
