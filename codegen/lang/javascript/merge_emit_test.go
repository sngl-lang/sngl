package javascript

import (
	"strings"
	"testing"

	"duckfam.us/sngl/ir"
)

func TestEmitMergeFuncsJS(t *testing.T) {
	sd := &ir.StructDef{Name: "Style", Fields: []*ir.StructField{
		{Name: "gap", Type: ir.OptionOf(ir.TypInt)},
		{Name: "color", Type: ir.OptionOf(ir.TypString)},
		{Name: "name", Type: ir.TypString},
		{Name: "tags", Type: ir.ListOf(ir.TypString)},
	}}
	out := EmitMergeFuncs([]*ir.StructDef{sd}, []ir.MergeSet{{Struct: sd, Field: "name"}})
	for _, want := range []string{
		"function __merge_Style(base, ov) {",
		"const r = { ...base };",
		"if (ov.gap != null) r.gap = ov.gap;",
		"if (ov.color != null) r.color = ov.color;",
		`if (ov.name != null && !(ov.name === ("")))`,
		"if (ov.tags != null && ov.tags.length > 0) r.tags = ov.tags;",
		"function __set_Style_name(base, v) { return { ...base, name: v }; }",
		"return r;",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in:\n%s", want, out)
		}
	}
}
