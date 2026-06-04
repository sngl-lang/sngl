package checker_test

import (
	"testing"
)

// TestHtmlPlacementDirectivesResolve verifies that the checker resolves
// html.frontend / html.backend as generic identity funcs (the wrapped value's
// type flows through) with no diagnostics.
func TestHtmlPlacementDirectivesResolve(t *testing.T) {
	src := `
component main {
    var n = 0
    text(value="v {html.frontend(n)}")
    text(value="w {html.backend(n)}")
}`
	for _, d := range checkSrc(t, src) {
		t.Fatalf("unexpected diag: %s", d.Msg)
	}
}
