package docs

import (
	"testing"

	"git.duckfam.us/jonathan/sngl/ast"

	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/internal/checker"
)

// A target's library package is served one of two ways -- by its plugin, or
// as a lib/ directory once it has moved (codegen/declared.go) -- and the
// package-level readers have to answer for both. These assert the three things
// that silently returned nothing when they did not: the option schema every
// `--opt` table is built from, the source that schema's declaration is found
// in, and the language half of the same question.
func TestTargetPackagesAnswerThePackageLevelAPI(t *testing.T) {
	for _, tc := range []struct {
		uri      string
		embedded bool
	}{{"platform/html", true}, {"language/go", false}} {
		uri := tc.uri
		t.Run(uri, func(t *testing.T) {
			if checker.HasPackage(uri) != tc.embedded {
				t.Fatalf("%s: embedded under lib/ is %v, want %v; this case no longer proves what it says", uri, !tc.embedded, tc.embedded)
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

// LangDocs is the language half of PlatformDocs, and go's package moved out of
// lib/languages with everything else.
func TestLangDocsReadsTheServedPackage(t *testing.T) {
	l := codegen.LookupLang("go")
	if l == nil {
		t.Skip("go language not registered")
	}
	if len(codegen.LangDocs(l)) == 0 {
		t.Error("LangDocs(go) is empty: `sngl doc go` has no package to render")
	}
}
