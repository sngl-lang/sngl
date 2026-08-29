package html

import (
	"strings"
	"testing"
)

// A reactive parent var passed as a prop to an inlined component must render
// and react inside the component. Regression for the tutorial "composing
// components" lesson: <Stat value=posts> rendered an empty value (and never
// updated) while <Stat value=12> worked. The optimizer specialize-inlined Stat
// because its const props (label/accent) unlocked folding, but only bound the
// const props — the non-const `value` param ref dangled in the inlined body.
func TestComponentNonConstPropReactivity(t *testing.T) {
	src := `
import . "sngl:ui"
output { none { html() } }
component Stat(label = "", value = 0) {
    vbox {
        text(value=label)
        text(value=string(value))
    }
}
component main {
    var posts = 12
    vbox {
        Stat(label="Posts", value=posts)
        button(text="Post", @click { posts += 1 })
    }
}
`
	out := generateMainPage(t, src)

	// The value text must be initialized from the bound parent var...
	if !strings.Contains(out, "String(state.posts)") {
		t.Errorf("component value prop not bound to parent var (dangling param):\n%s", out)
	}
	// ...and updated when the parent var mutates.
	click := out[strings.Index(out, "state.posts += 1"):]
	if i := strings.Index(click, "});"); i >= 0 {
		click = click[:i]
	}
	if !strings.Contains(click, "String(state.posts)") {
		t.Errorf("click handler does not update the component's value text:\n%s", click)
	}
}
