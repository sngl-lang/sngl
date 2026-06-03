package kotlin

import (
	"fmt"
	"strings"

	"git.duckfam.us/jonathan/sngl/ir"
)

// EmitMergeFuncs returns Kotlin source for one __merge_<Struct> helper per
// struct, using data-class .copy(). For option (T?) fields the elvis operator
// `ov.f ?: base.f` is exactly null-skip. Plain fields fall back to an
// if-expression against the Kotlin zero value.
func EmitMergeFuncs(structs []*ir.StructDef) string {
	var b strings.Builder
	for _, sd := range structs {
		name := exportName(sd.Name)
		fmt.Fprintf(&b, "fun __merge_%s(base: %s, ov: %s): %s =\n", name, name, name, name)
		b.WriteString("    base.copy(\n")
		for _, f := range sd.Fields {
			if f.Type != nil && f.Type.Kind == ir.TypeOption {
				fmt.Fprintf(&b, "        %s = ov.%s ?: base.%s,\n", f.Name, f.Name, f.Name)
			} else {
				fmt.Fprintf(&b, "        %s = if (ov.%s != %s) ov.%s else base.%s,\n",
					f.Name, f.Name, kotlinZeroComparand(f.Type), f.Name, f.Name)
			}
		}
		b.WriteString("    )\n\n")
	}
	return b.String()
}

func kotlinZeroComparand(t *ir.Type) string {
	if t == nil {
		return "null"
	}
	switch t.Kind {
	case ir.TypeString, ir.TypeURL, ir.TypeEmail, ir.TypeUUID, ir.TypeRegex,
		ir.TypeBase64, ir.TypeIPV4, ir.TypeIPV6, ir.TypeHostname, ir.TypeDecimal, ir.TypeColor:
		return `""`
	case ir.TypeBool:
		return "false"
	case ir.TypeFloat:
		return "0.0"
	default:
		return "0"
	}
}
