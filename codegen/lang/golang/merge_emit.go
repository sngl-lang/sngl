package golang

import (
	"fmt"
	"strings"

	"git.duckfam.us/jonathan/sngl/ir"
)

// EmitMergeFuncs returns Go source for one __merge_<Struct> helper per struct
// in structs. Each copies the (value-semantics) base, overwrites a field only
// when the override's value is "present" — non-nil for option<T> (*T), or
// non-zero for plain fields — and returns the merged copy.
func EmitMergeFuncs(structs []*ir.StructDef) string {
	var b strings.Builder
	for _, sd := range structs {
		name := ExportName(sd.Name)
		fmt.Fprintf(&b, "func __merge_%s(base, ov %s) %s {\n", name, name, name)
		for _, f := range sd.Fields {
			fn := ExportName(f.Name)
			fmt.Fprintf(&b, "\tif ov.%s != %s { base.%s = ov.%s }\n", fn, goZeroComparand(f.Type), fn, fn)
		}
		b.WriteString("\treturn base\n}\n\n")
	}
	return b.String()
}

// goZeroComparand returns the Go expression a field is compared against to
// decide "unset". Option/pointer/reference types compare to nil; scalars to
// their zero literal.
func goZeroComparand(t *ir.Type) string {
	if t == nil {
		return "nil"
	}
	switch t.Kind {
	case ir.TypeOption, ir.TypeList, ir.TypeMap, ir.TypeRef, ir.TypeFunc:
		return "nil"
	case ir.TypeString, ir.TypeURL, ir.TypeEmail, ir.TypeUUID, ir.TypeRegex,
		ir.TypeBase64, ir.TypeIPV4, ir.TypeIPV6, ir.TypeHostname, ir.TypeDecimal, ir.TypeColor:
		return `""`
	case ir.TypeBool:
		return "false"
	default:
		return "0"
	}
}
