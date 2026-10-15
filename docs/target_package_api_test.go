package docs

import (
	"testing"

	"duckfam.us/sngl/ast"

	"duckfam.us/sngl/codegen"
	"duckfam.us/sngl/internal/checker"
)

// A target's library package is a lib/ directory (codegen/declared.go), and
// the package-level readers reach it through the registry as well as through
// the embedded tiers. These assert the three things that silently returned
// nothing when they did not: the option schema every `--opt` table is built
// from, the source that schema's declaration is found in, and the language
// half of the same question. gtk4 is the one whose package imports another --
// its widgets are the gir: scheme's.
func TestTargetPackagesAnswerThePackageLevelAPI(t *testing.T) {
	for _, uri := range []string{"platform/html", "language/go", "platform/gtk4"} {
		t.Run(uri, func(t *testing.T) {
			if !checker.HasPackage(uri) {
				t.Fatalf("%s is not under lib/", uri)
			}
			comp := checker.TargetNode(uri)
			if comp == nil {
				t.Fatalf("TargetNode(%q) = nil: this target has no build node, so no --opt documentation", uri)
			}
			if comp.AST == nil {
				t.Fatalf("TargetNode(%q) carries no declaration", uri)
			}
			// The docs read the node off the IR and then find the declaration
			// in the source, by pointer. Two parses of one file never share
			// one, so this fails unless the source is served from one parse.
			found := false
			for _, doc := range checker.PackageSource(uri) {
				for _, stmt := range doc.Stmts {
					if stmt == ast.Stmt(comp.AST) {
						found = true
					}
				}
			}
			if !found {
				t.Errorf("the build node of %s is in no document PackageSource returns", uri)
			}
			if opts := optionsForPackage(uri); len(opts) == 0 {
				t.Errorf("optionsForPackage(%q) is empty: the target documents no options", uri)
			}
		})
	}
}

// LangDocs is the language half of PlatformDocs, and go's package is
// lib/language/go.
func TestLangDocsReadsTheServedPackage(t *testing.T) {
	l := codegen.LookupLang("go")
	if l == nil {
		t.Skip("go language not registered")
	}
	if len(codegen.LangDocs(l)) == 0 {
		t.Error("LangDocs(go) is empty: `sngl doc go` has no package to render")
	}
}
