package html

import (
	"strings"
	"testing"
)

// TestStringHelperEmittedFromHandler guards against pruneHelpers deleting a
// runtime helper that is used only in a handler (or timer) body. Such bodies
// are translated by the JsIRContext path and reference String(...), but they
// are not part of the MutationModel's Updaters, so an earlier version of
// pruneHelpers (which scanned only updater bodies) deleted "String" from the
// shared CommonAnalysis.Helpers map and the `function String(v)` declaration
// was never emitted — producing a runtime "String is not defined" when the
// handler fired.
func TestStringHelperEmittedFromHandler(t *testing.T) {
	src := `
component main {
    var count = 0
    var label = ""
    button(text="Add", @click { count = count + 1
        label = string(count) })
    text(value=label)
}
`
	out := renderComponentHTML(t, src)
	if !strings.Contains(out, "String(state.count)") {
		t.Fatalf("expected the handler to emit a String(state.count) call; got:\n%s", out)
	}
	if !strings.Contains(out, "function String(v)") {
		t.Errorf("String helper declaration missing: pruneHelpers deleted a helper " +
			"used only in a handler body (it must scan handler/timer bodies too)")
	}
}
