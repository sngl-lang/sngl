package html

import (
	"testing"

	"git.duckfam.us/jonathan/sngl/internal/checker"
	"git.duckfam.us/jonathan/sngl/internal/parser"
	"git.duckfam.us/jonathan/sngl/ir"
)

// checkedPkgForTest checks a program that names the html platform package.
func checkedPkgForTest(t *testing.T, src string) *ir.Package {
	t.Helper()
	doc, err := parser.Parse("elemdecl.sngl", []byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	pkg, diags := checker.Check(doc, &checker.Config{
		IsMain:    true,
		Platforms: []ir.Platform{&Generator{}},
		Targets:   []ir.StaticTarget{{Platform: "html", Language: "none"}},
	})
	if len(diags) > 0 {
		t.Fatalf("check: %v", diags[0])
	}
	return pkg
}

// A compilation resolves the element declaration once, however many windows it
// emits. The memo is poisoned between the two generators rather than counted:
// one that resolved the declaration for itself would find the real one and
// never see the poison.
func TestRawElementResolvedOncePerCompilation(t *testing.T) {
	pkg := checkedPkgForTest(t, "import . \"sngl://std\"\nimport html \"sngl://platforms/html\"\n\nwindow(\"a\") {\n    html.div {}\n}\n\nwindow(\"b\") {\n    html.span {}\n}\n")

	shared := newWindowShared("", nil)
	first := newHTMLGen(pkg, nil, htmlConfig{}, shared).rawElement()
	if first == nil {
		t.Fatal("first window resolved no element declaration")
	}

	poison := &ir.Component{Name: "poison", Wildcard: "*", WildcardInto: "tag"}
	shared.rawElem = poison

	if got := newHTMLGen(pkg, nil, htmlConfig{}, shared).rawElement(); got != poison {
		t.Errorf("second window re-resolved the element declaration (got %v); "+
			"the memo belongs to the compilation, not the generator", got.Name)
	}
}
