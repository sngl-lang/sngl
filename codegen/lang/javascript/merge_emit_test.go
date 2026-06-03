package javascript

import (
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/ir"
)

func TestEmitMergeFuncsJS(t *testing.T) {
	sd := &ir.StructDef{Name: "Style", Fields: []*ir.StructField{
		{Name: "gap", Type: ir.OptionOf(ir.TypInt)},
		{Name: "color", Type: ir.OptionOf(ir.TypString)},
	}}
	out := EmitMergeFuncs([]*ir.StructDef{sd})
	for _, want := range []string{
		"function __merge_Style(base, ov) {",
		"const r = { ...base };",
		"if (ov.gap != null) r.gap = ov.gap;",
		"if (ov.color != null) r.color = ov.color;",
		"return r;",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in:\n%s", want, out)
		}
	}
}
