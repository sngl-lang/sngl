package golang

import (
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/ir"
)

func TestEmitMergeFuncsGo(t *testing.T) {
	sd := &ir.StructDef{Name: "Style", Fields: []*ir.StructField{
		{Name: "gap", Type: ir.OptionOf(ir.TypInt)},
		{Name: "color", Type: ir.OptionOf(ir.TypString)},
		{Name: "flex", Type: ir.TypFloat}, // plain field → type-zero test
		{Name: "weight", Type: &ir.Type{Kind: ir.TypeEnum, Decl: &ir.EnumDef{Name: "W"}}},
		{Name: "size", Type: multiBaseUnitType()},
	}}
	out := EmitMergeFuncs([]*ir.StructDef{sd})
	for _, want := range []string{
		"func __merge_Style(base, ov Style) Style {",
		"if ov.Gap != nil { base.Gap = ov.Gap }",
		"if ov.Color != nil { base.Color = ov.Color }",
		"if ov.Flex != 0 { base.Flex = ov.Flex }",
		`if ov.Weight != "" { base.Weight = ov.Weight }`,
		"if ov.Size != (Measurement{}) { base.Size = ov.Size }",
		"return base",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in:\n%s", want, out)
		}
	}
}
