package kotlin

import (
	"fmt"
	"strings"

	"duckfam.us/sngl/ir"
)

// EmitMergeFuncs returns Kotlin source for one __merge_<Struct> helper per
// struct, using data-class .copy(): a spread writes each field of its operand
// not at its type's zero. For option (T?) fields the elvis operator
// `ov.f ?: base.f` is exactly null-skip. Plain fields fall back to an
// if-expression against the field type's zero (KtFieldZero), which Kotlin's
// == compares structurally.
func EmitMergeFuncs(structs []*ir.StructDef, sets []ir.MergeSet) string {
	var b strings.Builder
	// A field written after a spread is written whatever it holds.
	for _, set := range sets {
		name := exportName(set.Struct.Name)
		ft := "Any?"
		if d := ir.FieldNamed(set.Struct.Fields, set.Field); d != nil {
			ft = IRTypeToKt(d.Type)
		}
		fmt.Fprintf(&b, "fun %s(base: %s, v: %s): %s = base.copy(%s = v)\n\n", ir.MergeSetName(set), name, ft, name, set.Field)
	}
	for _, sd := range structs {
		name := exportName(sd.Name)
		fmt.Fprintf(&b, "fun __merge_%s(base: %s, ov: %s): %s =\n", name, name, name, name)
		b.WriteString("    base.copy(\n")
		for _, f := range sd.Fields {
			if f.Type != nil && f.Type.Kind == ir.TypeOption {
				fmt.Fprintf(&b, "        %s = ov.%s ?: base.%s,\n", f.Name, f.Name, f.Name)
			} else {
				fmt.Fprintf(&b, "        %s = if (ov.%s != %s) ov.%s else base.%s,\n",
					f.Name, f.Name, KtFieldZero(f.Type), f.Name, f.Name)
			}
		}
		b.WriteString("    )\n\n")
	}
	return b.String()
}

// KtFieldZero is the default a data-class field takes when its declaration
// writes none, and so also what a merge reads as unset. A library struct is
// not body-checked, so its fields carry no synthesized default the way a
// program's do, and `Style(color = …)` named every other field.
func KtFieldZero(t *ir.Type) string {
	if t != nil {
		switch t.Kind {
		case ir.TypeStruct:
			if ir.IsColorStruct(t) {
				return colorKtType + "()"
			}
			if ir.IsBuilder(t) {
				return "StringBuilder()"
			}
			if ir.StringReprStruct(t) {
				return `""`
			}
			if sd, ok := t.Decl.(*ir.StructDef); ok && sd.Foreign.Name == "" {
				return exportName(sd.Name) + "()"
			}
		case ir.TypeUnit:
			if u := ir.UnitDeclOf(t); u != nil && !u.IsSingleBase() {
				return exportName(u.Name) + "()"
			}
			if UnitKtType(ir.UnitDeclOf(t)) == "Long" {
				return "0L"
			}
			return "0.0"
		case ir.TypeMap:
			return "mapOf()"
		}
	}
	return ktZeroFor(t)
}
