package fyne

import (
	"regexp"
	"strings"
	"testing"
)

// inlinedPairSrc instantiates `Pair` three times, in the three call spellings,
// so passNoInlineComponents substitutes it three times and clones its `both()`
// once per instance.
//
// `sep` is what keeps the clones: it is state, so the component is impure and
// `both()` is not a constant. Written over the props alone -- which is
// testdata/test_named_component_props.sngl, where this test read its source
// from until the inliner stopped leaving a receiver on a hoisted clone -- the
// whole call folds to "alpha+beta" at build time and there is no method left
// for either of these tests to be about.
const inlinedPairSrc = `
import . "sngl:ui"

component Pair(first string, second string) node {
    var sep = "+"

    func both() => first + sep + second

    text(value=both())
    button(text="flip", @click { sep = "/" })
}

component main node {
    Pair("alpha", "beta")            // all positional
    Pair("gamma", second="delta")    // positional then named
    Pair(second="zeta", first="eta") // out-of-order named
}

window {
    main
}
`

// TestAnInlinedComponentLeavesNoMethodBehind compiles inlinedPairSrc, whose
// `Pair` component is instantiated three times and inlined away entirely.
//
// Each instance gets a clone of `both()` with its arguments folded in. The
// original stays registered in pkg.Funcs -- the checker puts a nested method
// in both its component's Funcs and the package's -- so the inliner dropping
// the declaration left the member behind, and every Go target emitted
// `func (m *Model) both() string { return first + "+" + second }`: props that
// exist nowhere any more, read as bare identifiers, on a Model that declares
// neither.
func TestAnInlinedComponentLeavesNoMethodBehind(t *testing.T) {
	model := generateFyneModelBuilt(t, inlinedPairSrc)

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
