package lsp

import (
	"path/filepath"
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl"
	"git.duckfam.us/jonathan/sngl/internal/parser"
	"git.duckfam.us/jonathan/sngl/ir"
)

func checkSource(t *testing.T, src string) *ir.Package {
	t.Helper()
	doc, err := parser.Parse("t.sngl", []byte(withStdSrc(src)))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	dir, _ := filepath.Abs(".")
	pkg, diags := sngl.Check(doc, dir)
	for _, d := range diags {
		if d.Severity == ir.Error {
			t.Fatalf("check: %s", d.Error())
		}
	}
	return pkg
}

func TestLookupStdlibSymbol_Component(t *testing.T) {
	pkg := checkSource(t, `component App ui { vbox {} }`)
	md, ok := lookupStdlibSymbol(pkg, "vbox")
	if !ok {
		t.Fatal("vbox not found in stdlib")
	}
	if !strings.Contains(md, "component vbox") {
		t.Errorf("md missing 'component vbox': %s", md)
	}
	if !strings.Contains(md, "stdlib") {
		t.Errorf("md missing 'stdlib' badge: %s", md)
	}
}

func TestLookupStdlibSymbol_Func(t *testing.T) {
	pkg := checkSource(t, `component App ui {}`)
	md, ok := lookupStdlibSymbol(pkg, "color.rgb")
	if !ok {
		t.Fatal("color.rgb not found")
	}
	if !strings.Contains(md, "func") || !strings.Contains(md, "rgb") {
		t.Errorf("md = %s", md)
	}
}

func TestLookupComponentProp(t *testing.T) {
	pkg := checkSource(t, `component App ui { vbox {} }`)
	// Pick a prop that the vbox stdlib component exposes. Try
	// common ones; skip the test if vbox isn't around.
	comp := findComponent(pkg, "vbox")
	if comp == nil {
		t.Skip("vbox not present")
	}
	if len(comp.Props) == 0 {
		t.Skip("vbox has no props")
	}
	propName := comp.Props[0].Name
	md, ok := lookupComponentProp(pkg, "vbox", propName)
	if !ok {
		t.Fatalf("vbox.%s not found", propName)
	}
	if !strings.Contains(md, "vbox."+propName) {
		t.Errorf("md = %s", md)
	}
}
