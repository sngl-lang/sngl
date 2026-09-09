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
import . "sngl:ui"
import app "sngl:app"
app.window {
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

// TestStringHelperEmittedFromSetter guards the one remaining flag-on-shared-ctx
// path with no other coverage: a derived var's @change handler becomes a setter
// ($set_<var>), translated during emitScript AFTER optimizeIR but BEFORE the
// helper-emit check. Here String(...) appears ONLY in that setter body — not in
// any init value, updater, or other handler. The helper must still be emitted.
// If a future refactor moved emitSetter after the helper check, this would catch
// the regression (the setter's flag would arrive too late).
func TestStringHelperEmittedFromSetter(t *testing.T) {
	src := `
import . "sngl:ui"
import app "sngl:app"
app.window {
    var label = ""
    var tracked = 0 @change {
        label = string(tracked)
    }
    button(text="+", @click { tracked = tracked + 1 })
    text(value=label)
}
`
	out := renderComponentHTML(t, src)
	if !strings.Contains(out, "function $set_tracked(v)") {
		t.Fatalf("expected a setter for the @change var; got:\n%s", out)
	}
	if !strings.Contains(out, "String(state.tracked)") {
		t.Fatalf("expected String(state.tracked) inside the setter; got:\n%s", out)
	}
	if !strings.Contains(out, "function String(v)") {
		t.Errorf("String helper declaration missing: a helper used only in a setter " +
			"body was dropped (emitSetter must flag before the helper-emit check)")
	}
}
