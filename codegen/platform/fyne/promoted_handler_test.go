package fyne

import (
	"strings"
	"testing"
)

// A Fyne callback hands its handler the value it reports, and the override's
// handler names it: `Entry(@input(v) { if v != value { value = v; input(v) } })`. So the
// promoted handler takes the callback's parameter under that name, and the
// override's write of its prop is a bound var's write-back reading it.
//
// The second half is the bug a rewrite of the handler's first statement once
// had: a handler that merely began with an assignment had that line replaced
// by a write-back nobody asked for. Both halves are asserted here, because a
// handler that read nothing would pass the second alone.
func TestPromotedHandlerTakesTheCallbackParameter(t *testing.T) {
	t.Run("a bound prop writes back from the callback parameter", func(t *testing.T) {
		out := generateFyneGo(t, `
import . "sngl:ui"
var name string = ""
window {
    vbox {
        input(:value=name)
    }
}
`)
		if !strings.Contains(out, "_input_handler(v string) {\n\tif v != m.name {\n\t\tm.name = v\n") {
			t.Errorf("the write-back does not read the callback parameter\n--- generated ---\n%s", out)
		}
	})

	t.Run("an unbound handler keeps the assignment it opens with", func(t *testing.T) {
		out := generateFyneGo(t, `
import . "sngl:ui"
var hits int = 0
window {
    vbox {
        input(@input { hits = 1 })
    }
}
`)
		if !strings.Contains(out, "m.hits = 1") {
			t.Errorf("the handler's own first statement was dropped\n--- generated ---\n%s", out)
		}
		// The replacement was not merely lossy: `hits` is an int and the
		// callback parameter is a string, so the emitted Go did not compile.
		if strings.Contains(out, "_input_handler(v string) {\n\tm.hits = v") {
			t.Errorf("a write-back was invented for a prop nothing bound\n--- generated ---\n%s", out)
		}
	})
}
