package bubbletea

import (
	"strings"
	"testing"
)

// A narrowed option compiles as Go.
//
// Go's option<T> is *T, so reading one as the T a null test proved it to be is
// a dereference. The generated program is where that is decided: a cast --
// `int(m.maybe)` -- is what the conversion path emits for every other target
// type, type-checks in the compiler, and does not compile as Go. Nothing short
// of running go build over the output says which one came out.
func TestNarrowedOptionCompilesAsGo(t *testing.T) {
	src := `
import . "sngl:ui"

component main ui {
    var maybe option<int>

    button(text="clear", @click {
        maybe = null
    })
    if maybe != null {
        text(value=string(maybe * 2))
    }
    if maybe != null && maybe > 2 {
        text(value="big")
    }
}
`
	model := generateBubbleteaModel(t, src)
	if !strings.Contains(model, "*(m.maybe)") {
		t.Errorf("expected a dereference of the narrowed option; got:\n%s", model)
	}
	buildGeneratedGo(t, "optnarrow", model)
}
