package kotlin

import (
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/ir"
)

func TestEmitMergeFuncsKotlin(t *testing.T) {
	sd := &ir.StructDef{Name: "Style", Fields: []*ir.StructField{
		{Name: "gap", Type: ir.OptionOf(ir.TypInt)},
		{Name: "color", Type: ir.OptionOf(ir.TypString)},
	}}
	out := EmitMergeFuncs([]*ir.StructDef{sd})
	for _, want := range []string{
		"fun __merge_Style(base: Style, ov: Style): Style =",
		"base.copy(",
		"gap = ov.gap ?: base.gap,",
		"color = ov.color ?: base.color,",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in:\n%s", want, out)
		}
	}
}
