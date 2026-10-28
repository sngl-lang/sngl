package javascript

import (
	"fmt"
	"strings"

	"duckfam.us/sngl/ir"
)

// EmitMergeFuncs returns JS source for one __merge_<Struct> helper per struct.
// A spread writes each field of its operand not at its type's zero, so the
// helper copies base and overwrites each field ov holds otherwise
// (mergeWrites).
func EmitMergeFuncs(structs []*ir.StructDef, sets []ir.MergeSet) string {
	var b strings.Builder
	t := &Translator{}
	// A field written after a spread is written whatever it holds.
	for _, set := range sets {
		fmt.Fprintf(&b, "function %s(base, v) { return { ...base, %s: v }; }\n\n", ir.MergeSetName(set), jsFieldKey(set.Struct, set.Field))
	}
	for _, sd := range structs {
		fmt.Fprintf(&b, "function __merge_%s(base, ov) {\n", sd.Name)
		b.WriteString("  const r = { ...base };\n")
		for _, f := range sd.Fields {
			key := jsFieldKey(sd, f.Name)
			fmt.Fprintf(&b, "  if (%s) r.%s = ov.%s;\n", t.mergeWrites(f.Type, "ov."+key), key, key)
		}
		b.WriteString("  return r;\n}\n\n")
	}
	return b.String()
}

// mergeWrites is the condition under which a spread writes v, a field of
// type ft: v is not ft's zero. An option is null at its zero, and a list or
// map is empty; anything else is compared with its zero as == compares it.
func (t *Translator) mergeWrites(ft *ir.Type, v string) string {
	if ft == nil {
		return v + " != null"
	}
	switch ft.Kind {
	case ir.TypeOption, ir.TypeFunc, ir.TypeRef, ir.TypeNull:
		return v + " != null"
	case ir.TypeList:
		return v + " != null && " + v + ".length > 0"
	case ir.TypeMap:
		return v + " != null && " + v + ".size > 0"
	}
	zero := ir.ZeroExpr(ft)
	if zero == nil {
		return v + " != null"
	}
	return v + " != null && !" + jsEqualExpr(ft, v, "("+t.TranslateIRLiteral(zero)+")")
}
