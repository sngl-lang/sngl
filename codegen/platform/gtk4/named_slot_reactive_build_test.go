package gtk4

import "testing"

// namedSlotReactiveSrc is a named slot inside a reactive `if`, and
// scopedNamedSlotReactiveSrc the scoped form of the same.
//
// `q` holds state on purpose: a pure component is substituted into its caller
// before passInlinePure looks at any slot, so its content is walked as ordinary
// children and the defect does not appear.
const namedSlotReactiveSrc = `
import . "sngl:ui"

component q(body component) ui {
    var on = false

    button #t(text="toggle", @click { on = !on })
    vbox {
        if on {
            body {}
        }
    }
}

component main ui {
    q {
        component body ui {
            text(value="SUPPLIED")
        }
    }
}
`

const scopedNamedSlotReactiveSrc = `
import . "sngl:ui"

component q(ready component(int)) ui {
    var n = 0

    button #b(text="bump", @click { n = n + 1 })
    vbox {
        if n > 0 {
            ready(n) {}
        }
    }
}

component main ui {
    q {
        component ready(v) ui {
            text(value="N=" + string(v))
        }
    }
}
`

// A named slot inside a reactive `if` compiles.
//
// Builds rather than runs, for the reason TestAnEffectCompiles builds:
// constructing a widget needs a GTK display. What it catches is the whole
// defect anyway -- passInlinePure walked a node's Children and Handlers but not
// its Slots, so the content reached passReactivity still spelled as the wrapper
// the caller wrote, and lowering refused the program before any code was
// emitted at all.
func TestANamedSlotInAReactiveIfCompiles(t *testing.T) {
	skipWithoutGIR(t)
	for name, src := range map[string]string{
		"bare":   namedSlotReactiveSrc,
		"scoped": scopedNamedSlotReactiveSrc,
	} {
		t.Run(name, func(t *testing.T) {
			buildGTK4Model(t, "gtk4-named-slot-", generateGTK4ModelBuilt(t, src))
		})
	}
}
