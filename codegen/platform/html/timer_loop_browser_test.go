//go:build !js

package html

import (
	"fmt"
	"testing"
)

// Timers in a reactive loop keep one schedule each.
//
// A row of a reactive loop is a live instance, and its record is the only
// per-iteration storage in the language -- so the handle html's `timer`
// override holds is a local of that instance's factory call and two rows never
// share one. The rows run at 20ms and 500ms, so their counts have to diverge by
// roughly the ratio of their periods; one shared handle would arm twice into
// one cell and leave a schedule nothing could clear.
//
// What makes such a row rebuildable is `#[construct]` on `interval`: a running
// schedule cannot have its period written into it, so a row whose period
// changed is destroyed and built again rather than patched.
func TestTimer_ARowOfTimersKeepsOneScheduleEach(t *testing.T) {
	src := `
import . "sngl:ui"
import . "sngl:time"
component App() node {
    var fast = 0
    var slow = 0
    var periods list<duration> = [20ms, 500ms]

    for var p = periods {
        timer(interval=p, enabled=true, @tick {
            if p == 20ms {
                fast += 1
            } else {
                slow += 1
            }
        })
    }
    text(value="fast=" + string(fast) + " slow=" + string(slow))
}
window(title="H") { App() }
`
	b := startComponent(t, src)
	defer b.Close()
	page := b.Page()
	read := func() string { return page.MustElement("body").MustText() }

	// Both rows must run, and the fast one must outpace the slow one by enough
	// that no single schedule could account for both counts. A tolerant
	// factor, because a loaded machine coalesces timers.
	counts := func(s string) (fast, slow int, ok bool) {
		_, err := fmt.Sscanf(s, "fast=%d slow=%d", &fast, &slow)
		return fast, slow, err == nil
	}
	got := waitForText(t, read, func(s string) bool {
		f, sl, ok := counts(s)
		return ok && sl >= 2 && f > sl*5
	}, "both rows to tick with the fast one well ahead")

	f, sl, _ := counts(got)
	t.Logf("fast=%d slow=%d (periods 20ms and 500ms)", f, sl)
}
