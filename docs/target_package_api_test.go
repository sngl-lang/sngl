package docs

import (
	"testing"

	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/internal/checker"
)

// A target's library package is not under lib/ -- the plugin serves it -- so
// the package-level readers have to reach it through the registry rather than
// through the embedded tiers. These assert the three things that silently
// returned nothing when they did not: the option schema every `--opt` table is
// built from, the source that schema's declaration is found in, and the
// language half of the same question.
func TestTargetPackagesAnswerThePackageLevelAPI(t *testing.T) {
	for _, uri := range []string{"platforms/html", "languages/go"} {
		t.Run(uri, func(t *testing.T) {
			if checker.HasPackage(uri) {
				t.Fatalf("%s is embedded under lib/ after all; this test no longer proves anything", uri)
			}
			sd := checker.OptionsStruct(uri)
			if sd == nil {
				t.Fatalf("OptionsStruct(%q) = nil: no --opt documentation for this target", uri)
			}
			if sd.AST == nil {
				t.Fatalf("OptionsStruct(%q) carries no declaration", uri)
			}
			// The docs read the mark off the IR and then find the declaration
			// in the source, by pointer. Two parses of one file never share
			// one, so this fails unless the source is served from one parse.
			found := false
			for _, doc := range checker.PackageSource(uri) {
				for _, stmt := range doc.Stmts {
					if stmt == sd.AST {
						found = true
					}
				}
			}
			if !found {
				t.Errorf("the #[options] declaration of %s is in no document PackageSource returns", uri)
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
