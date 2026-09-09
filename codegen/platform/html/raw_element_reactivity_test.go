package html

import (
	"strings"
	"testing"
)

// A bodyless raw platform element (`html.progress(value=…)`) with a reactive
// attribute must update when the var it reads is mutated. Regression for the
// tutorial "raw platform elements" lesson: the -/+10 buttons changed the
// percent label but left the <progress> bar frozen.
//
// Root cause: the checker lowered namespace-qualified bodyless calls
// (`html.progress(...)`) to ir.CallStmt rather than ir.NodeInst, so the
// reactivity lowering pass — which only walks NodeInsts — never registered the
// element's reactive prop, never assigned it a __nN id, and never injected a
// mutation-update into the click handlers. The element rendered once via the
// platform's init-write path but never updated.
func TestRawPlatformElementReactiveAttrUpdates(t *testing.T) {
	src := `
import . "sngl:ui"
import "sngl:platform/html"
import "sngl:platform/html"
import app "sngl:app"
output { none { html() } }
app.window {
    var volume = 60
    vbox {
        html.progress(value=string(volume), max="100")
        button(text="+10", @click { volume = volume + 10 })
    }
}
`
	out := generateMainPage(t, src)

	// The click handler that mutates volume must also patch the progress
	// element's value, and must patch it the same way the initial render
	// does. `value` is a live IDL attribute on <progress>, so the property
	// write drives the bar; what matters here is that one prop has one
	// spelling -- writing the property at init and the content attribute in
	// the handler was two answers to one question.
	handler := out[strings.Index(out, "state.volume = state.volume + 10"):]
	if i := strings.Index(handler, "});"); i >= 0 {
		handler = handler[:i]
	}
	if !strings.Contains(handler, `__n0.value = String(state.volume)`) {
		t.Errorf("click handler does not update the progress value after mutating volume:\n%s", handler)
	}
	// The same spelling at init, which is the half that used to disagree.
	if !strings.Contains(out, `__n0.value = String(state.volume)`) {
		t.Errorf("the initial render writes the progress value differently from the handler:\n%s", out)
	}
}
