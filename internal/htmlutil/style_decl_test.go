package htmlutil_test

import (
	"testing"

	"duckfam.us/sngl/ast"
	"duckfam.us/sngl/internal/htmlutil"
	"duckfam.us/sngl/internal/parser"
	"duckfam.us/sngl/ir"
	"duckfam.us/sngl/lib"
)

// TestStyleFieldsAllReachCSS holds lib/std's `Style` declaration and this
// package's prop→CSS mapping in step. A field can be declared, type-check at
// every call site, and render as nothing at all — which is how minWidth,
// fontFamily, borderBottom, borderLeft, borderCollapse, borderStyle and cursor
// came to be silently dropped, two of them added by the very change that
// needed them.
//
// The Style declaration is read from the embedded stdlib source rather than
// listed here, so adding a field to it fails this test until the field renders.
func TestStyleFieldsAllReachCSS(t *testing.T) {
	fields := styleFields(t)
	if len(fields) < 20 {
		t.Fatalf("found only %d Style fields; the declaration was not read", len(fields))
	}
	// Through the renderer the platform actually calls, with a value that suits
	// every field's type. A predicate written beside the test can agree with the
	// declaration while the renderer drops the field -- which is what happened:
	// the six multi-declaration props were listed as supported by hand, so
	// deleting one of their arms went undetected.
	for _, name := range fields {
		if css := htmlutil.StylePropToCSSIR(name, &ir.Literal{Type: ir.TypInt, Value: "1"}); css == "" {
			t.Errorf("Style.%s is declared in lib/ui and renders as nothing", name)
		}
	}
}

// styleFields returns the field names of the `Style` struct as lib/ui
// declares it.
func styleFields(t *testing.T) []string {
	t.Helper()
	entries, err := lib.FS.ReadDir("ui")
	if err != nil {
		t.Fatalf("read lib/ui: %v", err)
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		src, err := lib.FS.ReadFile("ui/" + e.Name())
		if err != nil {
			t.Fatalf("read lib/ui/%s: %v", e.Name(), err)
		}
		doc, err := parser.Parse(e.Name(), src)
		if err != nil {
			t.Fatalf("parse lib/ui/%s: %v", e.Name(), err)
		}
		for _, d := range doc.Stmts {
			sd, ok := d.(*ast.StructDef)
			if !ok || sd.Name != "Style" {
				continue
			}
			var out []string
			for _, f := range sd.Fields() {
				out = append(out, f.Names...)
			}
			return out
		}
	}
	t.Fatal("no `struct Style` declaration found in lib/std")
	return nil
}
