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
	// Every name here has a test in its own package pairing the capability
	// with the implementation. Membership is that test existing, and nothing
	// else: fyne and gtk4 were once listed on the grounds that they walk
	// lowered IR and so could one day meet the op, which exempted them from
	// this check while no such test existed -- so either of them turning the
	// capability on would have been reported by nobody, and first seen as the
	// walker's panic. A platform earns a line here in the commit that gives it
	// the assertion.
	asserted := map[string]bool{
		"html": true, // codegen/platform/html/insertbefore_test.go
	}
	declares := func(gen codegen.PlatformGenerator) bool {
		for _, lang := range codegen.Langs() {
			if lt := codegen.LookupLang(lang); lt != nil && gen.Capabilities(lt).InsertBefore {
				return true
			}
		}
		return false
	}
	for _, name := range codegen.Platforms() {
		gen := codegen.LookupPlatform(name)
		if gen == nil {
			t.Errorf("%s: registered but not resolvable", name)
			continue
		}
		if asserted[name] {
			continue
		}
		// Not on the list: it must not declare the capability, because
		// nothing in its package asserts an implementation to go with it.
		if declares(gen) {
			t.Errorf("%s declares Features.InsertBefore, but no test in its package pairs it with a codegen.ChildInserter; add one and list it here", name)
		}
	}
	// And the converse, so the list cannot go stale: an entry claiming a
	// platform is asserted elsewhere while that platform declares nothing is
	// an exemption shielding a capability nobody turned on -- which is what
	// fyne and gtk4 were.
	for name := range asserted {
		gen := codegen.LookupPlatform(name)
		if gen == nil {
			t.Errorf("%s is listed as asserting the InsertBefore pairing, but no such platform is registered", name)
			continue
		}
		if !declares(gen) {
			t.Errorf("%s is listed as asserting the InsertBefore pairing but declares Features.InsertBefore under no language; drop the line", name)
		}
	}
}
