package html

import (
	"strings"
	"testing"
)

// A handler that mutates a timer's `enabled` var must re-sync the timer, or the
// timer never starts/stops when state changes. Regression for the tutorial
// "timers" lesson: clicking Start flipped `running` (the button label updated)
// but the timer never started, so `seconds` never advanced. The reactive DOM
// updater for the label was injected into the handler, but the timer sync —
// the side effect of `running` changing — was not.
func TestTimerActiveVarMutationSyncsTimer(t *testing.T) {
	src := `
import . "sngl:std"
output { none { html() } }
component main {
    var seconds = 0
    var running = false
    timer(interval=1000ms, enabled=running, @tick { seconds += 1 })
    vbox {
        text(value="{seconds} s")
        button(text=running ? "Pause" : "Start", @click { running!! })
    }
}
`
	out := generateMainPage(t, src)

	// The Start/Pause handler toggles running; it must also re-sync the timer.
	start := strings.Index(out, "state.running = !state.running")
	if start < 0 {
		t.Fatalf("expected a handler toggling running:\n%s", out)
	}
	handler := out[start:]
	if i := strings.Index(handler, "});"); i >= 0 {
		handler = handler[:i]
	}
	if !strings.Contains(handler, "$timer_0_sync()") {
		t.Errorf("handler mutating timer's enabled var does not re-sync the timer:\n%s", handler)
	}
}
