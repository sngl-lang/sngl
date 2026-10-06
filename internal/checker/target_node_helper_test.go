package checker_test

import (
	"fmt"
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/internal/checker"
	"git.duckfam.us/jonathan/sngl/internal/parser"
	"git.duckfam.us/jonathan/sngl/ir"
)

// targetNodeDoc is the build-target node a stub target package declares for
// itself: `component platform() build.platform` named by #[gen.name], which
// is what an override's `[stub.platform]` resolves to. A real target's package
// declares its own; a stub handed in through Config.LibSources is a whole
// package, so it has to carry one too.
//
// uri is the package's LibSources key, `platform/<name>` or
// `language/<name>`.
func targetNodeDoc(t *testing.T, uri string) *ast.Document {
	t.Helper()
	tier, name, ok := strings.Cut(uri, "/")
	if !ok {
		t.Fatalf("targetNodeDoc: %q is not a target package", uri)
	}
	params := "()"
	if tier == "language" {
		params = "(platforms ...component build.platform)"
	}
	src := fmt.Sprintf(`import build "sngl:build"
import gen "sngl:x/gen"

#[gen.name(%q)]
component %s%s build.%s {}
`, name, tier, params, tier)
	doc, err := parser.Parse(name+"_node.sngl", []byte(src))
	if err != nil {
		t.Fatalf("targetNodeDoc: %v", err)
	}
	return doc
}

// A target package's node answers to the package's own name: the build finds
// sngl:platform/<name> by that name, so a node calling itself anything else is
// found by nothing.
func TestTargetPackageNodeCarriesItsPackageName(t *testing.T) {
	stub, err := parser.Parse("pkgstub.sngl", []byte(`import build "sngl:build"
import gen "sngl:x/gen"

#[gen.name("other")]
component platform() build.platform
`))
	if err != nil {
		t.Fatalf("parse stub: %v", err)
	}
	doc, err := parser.Parse("main.sngl", []byte("import p \"sngl:platform/pkgstub\"\n"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	_, diags := checker.Check(doc, &checker.Config{
		Platforms:  []ir.Platform{namedStubPlatform{"pkgstub"}},
		LibSources: map[string][]*ast.Document{"platform/pkgstub": {stub}},
	})
	want := `sngl:platform/pkgstub names its platform "other": a target package's node carries the package's own name, "pkgstub"`
	for _, d := range diags {
		if d.Severity == ir.Error && strings.Contains(d.Msg, want) {
			return
		}
	}
	t.Errorf("want %q, got %v", want, diags)
}
