//go:build !js

package html

import (
	"strings"
	"testing"
)

// TestIndexedForBindsIndexAndElement guards a correctness bug where the
// optimizer's for-unrolling bound the index/element to the wrong variables for
// the two-var form `for i, x = list`: the checker types `i` as the index (int)
// and `x` as the element, but unrolling bound `i`=element and `x`=index.
func TestIndexedForBindsIndexAndElement(t *testing.T) {
	src := `
import . "sngl://std"
output { none { html() } }
component main {
    for i, x = ["A", "B", "C"] {
        text(value="i={i} x={x}")
    }
}
`
	out := generateMainPage(t, src)
	for _, want := range []string{"i=0 x=A", "i=1 x=B", "i=2 x=C"} {
		if !strings.Contains(out, want) {
			t.Errorf("indexed for misbound; want %q in output:\n%s", want, out)
		}
	}
	// Single-var form must remain element-bound.
	out2 := generateMainPage(t, `
import . "sngl://std"
output { none { html() } }
component main { for x = ["P", "Q"] { text(value=x) } }
`)
	if !strings.Contains(out2, ">P<") || !strings.Contains(out2, ">Q<") {
		t.Errorf("single-var for regressed:\n%s", out2)
	}

	// Two-var form where the element var is UNUSED must still bind the index
	// (the form is syntactic, not usage-based: an unreferenced value var must
	// not collapse `for i, x` into the single-var element binding).
	out3 := generateMainPage(t, `
import . "sngl://std"
output { none { html() } }
component main { for i, x = ["A", "B"] { text(value="n" + string(i)) } }
`)
	if !strings.Contains(out3, ">n0<") || !strings.Contains(out3, ">n1<") {
		t.Errorf("two-var with unused element var misbound the index:\n%s", out3)
	}
}
