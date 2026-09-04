package fyne

import (
	"regexp"
	"strings"
	"testing"
)

// TestAnInlinedComponentLeavesNoMethodBehind reads
// testdata/test_named_component_props.sngl, whose `Pair` component is
// instantiated three times and inlined away entirely.
//
// Each instance gets a clone of `both()` with its arguments folded in. The
// original stays registered in pkg.Funcs -- the checker puts a nested method
// in both its component's Funcs and the package's -- so the inliner dropping
// the declaration left the member behind, and every Go target emitted
// `func (m *Model) both() string { return first + "+" + second }`: props that
// exist nowhere any more, read as bare identifiers, on a Model that declares
// neither.
func TestAnInlinedComponentLeavesNoMethodBehind(t *testing.T) {
	model := generateFyneModelBuilt(t, fixtureSource(t, "test_named_component_props.sngl"))

	// The clones are the whole of what Pair contributes.
	for _, want := range []string{
		"func (m *Model) both__inst0() string {",
		"func (m *Model) both__inst1() string {",
		"func (m *Model) both__inst2() string {",
	} {
		if !strings.Contains(model, want) {
			t.Errorf("emitted Go missing %q\n--- model.go ---\n%s", want, model)
		}
	}
	if regexp.MustCompile(`func \(m \*Model\) both\(\)`).MatchString(model) {
		t.Errorf("the inlined component's original method is still emitted\n--- model.go ---\n%s", model)
	}
	// Its body is what does not compile: nothing declares Pair's props.
	if leak := regexp.MustCompile(`\breturn \(\(first `).FindString(model); leak != "" {
		t.Errorf("emitted Go reads an inlined component's prop as a bare identifier: %q\n--- model.go ---\n%s", leak, model)
	}
}
