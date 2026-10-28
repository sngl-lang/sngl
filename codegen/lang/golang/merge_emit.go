package golang

import (
	"fmt"
	"strings"

	"duckfam.us/sngl/ir"
)

// EmitMergeFuncs returns Go source for one __merge_<Struct> helper per struct
// in structs. A spread writes each field of its operand not at its type's
// zero, so each helper copies the (value-semantics) base and overwrites a
// field the override holds otherwise (mergeWrites).
func EmitMergeFuncs(structs []*ir.StructDef, sets []ir.MergeSet) string {
	var b strings.Builder
	// A field written after a spread is written whatever it holds.
	for _, set := range sets {
		name := ExportName(set.Struct.Name)
		ft := "any"
		if d := ir.FieldNamed(set.Struct.Fields, set.Field); d != nil {
			ft = IRTypeToGo(d.Type)
		}
		fmt.Fprintf(&b, "func %s(base %s, v %s) %s { base.%s = v; return base }\n\n", ir.MergeSetName(set), name, ft, name, ExportName(set.Field))
	}
	for _, sd := range structs {
		name := ExportName(sd.Name)
		fmt.Fprintf(&b, "func __merge_%s(base, ov %s) %s {\n", name, name, name)
		for _, f := range sd.Fields {
			fn := ExportName(f.Name)
			fmt.Fprintf(&b, "\tif %s { base.%s = ov.%s }\n", mergeWrites(f.Type, "ov."+fn), fn, fn)
		}
		b.WriteString("\treturn base\n}\n\n")
	}
	return b.String()
}

// mergeWrites is the condition under which a spread writes v, a field of
// type t: v is not t's zero. An option, function or reference is nil at its
// zero and a list or map empty; anything else is compared with SNGL's zero
// for it, which for an enum is its first member and for a struct its
// fields' defaults, not Go's.
func mergeWrites(t *ir.Type, v string) string {
	if t == nil {
		return v + " != nil"
	}
	switch t.Kind {
	case ir.TypeOption, ir.TypeRef, ir.TypeFunc:
		return v + " != nil"
	case ir.TypeList, ir.TypeMap:
		return "len(" + v + ") > 0"
	case ir.TypeString:
		return v + ` != ""`
	case ir.TypeBool:
		return v
	case ir.TypeInt, ir.TypeFloat:
		return v + " != 0"
	case ir.TypeEnum:
		if ed, ok := t.Decl.(*ir.EnumDef); ok && ir.PayloadEnum(t) == nil && len(ed.Members) > 0 {
			return fmt.Sprintf("%s != %q", v, ed.Members[0].Name)
		}
	}
	if zero := ir.ZeroExpr(t); zero != nil {
		if z := IRLiteralToGo(zero); z != "" {
			if !goComparesNatively(t) {
				return "!" + (&goEqualer{}).equal(t, v, "("+z+")")
			}
			return v + " != (" + z + ")"
		}
	}
	z := ZeroValueGo(IRTypeToGo(t))
	return v + " != (" + z + ")"
}
