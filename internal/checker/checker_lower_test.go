package checker

import (
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/internal/parser"
	"git.duckfam.us/jonathan/sngl/ir"
)

func TestInternalLowerNamespace(t *testing.T) {
	src := `
import "sngl://internal/lower"

func test() => lower.CreateNode("text")
`
	doc, err := parser.Parse("test.sngl", []byte(withStd(src)))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	pkg, diags := Check(doc, &Config{IsMain: true})
	for _, d := range diags {
		if d.Severity == ir.Error {
			t.Errorf("unexpected diag: %s", d.Msg)
		}
	}
	if pkg == nil {
		t.Fatal("Check returned nil pkg")
	}
	var found bool
	for _, imp := range pkg.Imports {
		if strings.HasSuffix(imp.Path, "lower") && imp.Pkg != nil {
			found = true
			var hasCreateNode bool
			for _, f := range imp.Pkg.Funcs {
				if f.Name == "CreateNode" {
					hasCreateNode = true
					break
				}
			}
			if !hasCreateNode {
				t.Errorf("lower.CreateNode not registered on imported pkg")
			}
		}
	}
	if !found {
		t.Errorf("sngl://internal/lower import not present in checked pkg")
	}
}

func TestLowerNamespaceAutoImported(t *testing.T) {
	// User source that DOES NOT explicitly import internal://lower
	// should still resolve `lower.CreateNode` because the stdlib
	// brings the import in transitively via lib/lower.sngl.
	src := `func test() => lower.CreateNode("text")`
	doc, err := parser.Parse("test.sngl", []byte(withStd(src)))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	_, diags := Check(doc, &Config{IsMain: true})
	for _, d := range diags {
		if d.Severity == ir.Error {
			t.Errorf("unexpected diag: %s", d.Msg)
		}
	}
}
