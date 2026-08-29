package html

import (
	"testing"

	"git.duckfam.us/jonathan/sngl/internal/checker"
	"git.duckfam.us/jonathan/sngl/internal/parser"
	"git.duckfam.us/jonathan/sngl/ir"
)

// The package records the wildcard component its tags resolved to. Codegen
// needs the declaration in the lowered form, where the nodes that carried it
// are gone; it used to search for it instead, walking the whole IR when
// neither the package's components nor its imports' held one.
//
// The second case is that walk's reason for existing: a program that names no
// platform package still emits raw elements, because the stdlib wrappers it
// does use inline into them.
func TestPackageRecordsItsWildcard(t *testing.T) {
	for _, tc := range []struct {
		name string
		src  string
	}{
		{
			"tag written directly",
			"import . \"sngl://std\"\nimport html \"sngl://platforms/html\"\n\nwindow(\"t\") {\n    html.div {}\n}\n",
		},
		{
			"platform package never imported",
			"import . \"sngl://std\"\n\nwindow(\"t\") {\n    vbox {\n        text(value=\"hi\")\n    }\n}\n",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			doc, err := parser.Parse("w.sngl", []byte(tc.src))
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			pkg, diags := checker.Check(doc, &checker.Config{
				IsMain:    true,
				Platforms: []ir.Platform{&Generator{}},
				Targets:   []ir.StaticTarget{{Platform: "html", Language: "none"}},
			})
			for _, d := range diags {
				if d.Severity == ir.Error {
					t.Fatalf("check: %v", d)
				}
			}
			if pkg.Wildcard == nil {
				t.Fatal("pkg.Wildcard not recorded")
			}
			if pkg.Wildcard.Wildcard == "" || pkg.Wildcard.WildcardInto == "" {
				t.Errorf("pkg.Wildcard = %q, which is not a wildcard component", pkg.Wildcard.Name)
			}
		})
	}
}
