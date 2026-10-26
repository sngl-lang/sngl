package golang

import (
	"strings"
	"testing"

	"duckfam.us/sngl/ir"
)

func TestEmitMergeFuncsGo(t *testing.T) {
	sd := &ir.StructDef{Name: "Style", Fields: []*ir.StructField{
		{Name: "gap", Type: ir.OptionOf(ir.TypInt)},
		{Name: "color", Type: ir.OptionOf(ir.TypString)},
		{Name: "flex", Type: ir.TypFloat}, // plain field → type-zero test
		{Name: "weight", Type: &ir.Type{Kind: ir.TypeEnum, Decl: &ir.EnumDef{Name: "W", Members: []*ir.EnumMember{{Name: "unset"}, {Name: "bold"}}}}},
		{Name: "size", Type: multiBaseUnitType()},
		{Name: "tags", Type: ir.ListOf(ir.TypString)},
	}}
	out := EmitMergeFuncs([]*ir.StructDef{sd}, []ir.MergeSet{{Struct: sd, Field: "flex"}})
	for _, want := range []string{
		"func __merge_Style(base, ov Style) Style {",
		"if ov.Gap != nil { base.Gap = ov.Gap }",
		"if ov.Color != nil { base.Color = ov.Color }",
		"if ov.Flex != 0 { base.Flex = ov.Flex }",
		`if ov.Weight != "unset" { base.Weight = ov.Weight }`,
		"if ov.Size != (Measurement{Px: 0}) { base.Size = ov.Size }",
		"if len(ov.Tags) > 0 { base.Tags = ov.Tags }",
		"func __set_Style_flex(base Style, v float64) Style { base.Flex = v; return base }",
		"return base",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in:\n%s", want, out)
		}
	}
}
