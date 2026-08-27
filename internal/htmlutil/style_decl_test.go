package htmlutil_test

import (
	"testing"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/internal/htmlutil"
	"git.duckfam.us/jonathan/sngl/internal/parser"
	"git.duckfam.us/jonathan/sngl/lib"
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
	for _, name := range fields {
		if !htmlutil.StylePropSupported(name) {
			t.Errorf("Style.%s is declared in lib/std but has no CSS mapping: it renders as nothing", name)
		}
	}
}

// styleFields returns the field names of the `Style` struct as lib/std
// declares it.
func styleFields(t *testing.T) []string {
	t.Helper()
	entries, err := lib.FS.ReadDir("std")
	if err != nil {
		t.Fatalf("read lib/std: %v", err)
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		src, err := lib.FS.ReadFile("std/" + e.Name())
		if err != nil {
			t.Fatalf("read lib/std/%s: %v", e.Name(), err)
		}
		doc, err := parser.Parse(e.Name(), src)
		if err != nil {
			t.Fatalf("parse lib/std/%s: %v", e.Name(), err)
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
