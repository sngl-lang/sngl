package fyne

import (
	"strings"
	"testing"
)

// A Fyne callback receives the unwrapped value rather than the SNGL event
// struct, so the emitter rewrites the synthesized `<var> = <event>.<field>`
// two-way bind into a read of the callback's own parameter.
//
// Recognising that statement by "the handler opens with an assignment" was too
// loose: a handler that merely *began* with one had that line replaced by a
// write-back nobody asked for. Both halves are asserted here, because a fix
// that stopped rewriting altogether would pass the first alone.
func TestPromotedHandlerRewritesOnlyTheSynthesizedBind(t *testing.T) {
	t.Run("a bound prop still writes back from the callback parameter", func(t *testing.T) {
		out := generateFyneGo(t, `
import . "sngl:ui"
var name string = ""
component main {
    vbox {
        input(:value=name)
    }
}
`)
		if !strings.Contains(out, "m.name = s") {
			t.Errorf("the two-way bind does not read the callback parameter\n--- generated ---\n%s", out)
		}
		// Reading it off an event struct the closure never receives would not
		// compile.
		if strings.Contains(out, ".value") {
			t.Errorf("the bind still reads an event field\n--- generated ---\n%s", out)
		}
	})

	t.Run("an unbound handler keeps the assignment it opens with", func(t *testing.T) {
		out := generateFyneGo(t, `
import . "sngl:ui"
var hits int = 0
component main {
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
		if strings.Contains(out, "m.hits = s") {
			t.Errorf("a write-back was invented for a prop nothing bound\n--- generated ---\n%s", out)
		}
	})
}
