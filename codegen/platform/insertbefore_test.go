package platform_test

import (
	"testing"

	"git.duckfam.us/jonathan/sngl/codegen"
	_ "git.duckfam.us/jonathan/sngl/codegen/lang"
	_ "git.duckfam.us/jonathan/sngl/codegen/platform"
)

// InsertBefore is the one optional node operation, and it is optional in two
// places at once: a platform declares Features.InsertBefore so lowering may
// emit the op, and implements codegen.ChildInserter so something answers it.
// Either alone is a bug that no compile catches -- a capability with no
// implementation emits an op that reaches OnDefault, and an implementation
// with no capability is a method nothing ever calls.
//
// The pairing is asserted per platform, in the package that can see its
// translator type; this test holds the roster itself, so a platform added
// without the assertion is reported here rather than discovered by a page that
// throws.
func TestEveryPlatformPairsInsertBefore(t *testing.T) {
	// Platforms whose lowered IR goes through WalkLowered, and so could ever
	// meet the op. The others walk the tree themselves and have no translator
	// at all; see lower.hasInstanceRuntime for the same split.
	asserted := map[string]bool{
		"html": true,
		"fyne": true,
		"gtk4": true,
	}
	for _, name := range codegen.Platforms() {
		gen := codegen.LookupPlatform(name)
		if gen == nil {
			t.Errorf("%s: registered but not resolvable", name)
			continue
		}
		if _, ok := asserted[name]; ok {
			continue
		}
		// Not on the list: it must not declare the capability, because
		// nothing in its package asserts an implementation to go with it.
		for _, lang := range codegen.Langs() {
			lt := codegen.LookupLang(lang)
			if lt == nil {
				continue
			}
			if gen.Capabilities(lt).InsertBefore {
				t.Errorf("%s declares Features.InsertBefore under --lang %s, but no test in its package pairs it with a codegen.ChildInserter; add one", name, lang)
			}
		}
	}
}
