package gtk4

import (
	"strings"
	"testing"
)

// slotInstanceSrc is codegen/platform/fyne/instance_slot_test.go's, over the
// same lowering: a component the build cannot inline, whose own body holds a
// reactive block. fyne carries the runs-the-output half; gtk4 gets the compile
// check, because constructing a widget needs a GTK display.
const slotInstanceSrc = `
import . "sngl:ui"
import app "sngl:app"

component card(name = "") ui {
    var open = true
    button(text=name, @click { open = !open })
    if open {
        text(value="body of " + name)
    }
}

app.window {
    var items list<string> = ["a", "b"]
    for var it = items {
        card(name=it)
    }
}
`

// The widget a factory component's slot renderer creates is resolved from that
// component's funcs, not only from its body.
//
// The ctor's translator was handed collectTagComponents(comp.Body); the
// translator each method got was handed nothing, so the tag inside the slot
// renderer resolved to no declaration and the whole build stopped:
//
//	generate: gtk4: no widget for node "GtkLabel"
//
// The instance's root is the second claim. A reactive slot renders into the
// component's own __root box, which the ctor left nil and then rendered into,
// and which nothing then attached: Root was whatever the body happened to
// leave unparented, so the slot's entire subtree was built into a container no
// window ever held.
func TestInstanceSlotWidgetsAreDiscovered(t *testing.T) {
	skipWithoutGIR(t)
	model := generateGTK4Model(t, slotInstanceSrc)
	ctor := instanceCtorBody(t, model, "newCardInstance")

	// The box exists before the first render appends into it. Which
	// constructor spells it depends on the mode this host emits, so the claim
	// is that it is a box and not nil.
	if strings.Contains(ctor, "c.__root = nil") || !strings.Contains(ctor, "box_new") && !strings.Contains(ctor, "BoxNew(") {
		t.Errorf("the box a slot renders into is not constructed\n--- ctor ---\n%s", ctor)
	}
	// And it is in the tree, at the position the slot was written -- after the
	// button the body declared first.
	// The last mention of each is the parenting: whichever cast the mode
	// wraps it in, nothing names a widget after it is in the tree.
	button := strings.LastIndex(ctor, "c.__n0")
	root := strings.LastIndex(ctor, "c.__root")
	if button < 0 || root < 0 {
		t.Errorf("the instance's root holds neither the button nor the slot box\n--- ctor ---\n%s", ctor)
	} else if button > root {
		t.Errorf("the slot box was parented before the button the body declared first\n--- ctor ---\n%s", ctor)
	}
	buildGTK4Model(t, "gtk4-slot-inst-", model)
}

// instanceCtorBody is the emitted ctor for one component's record.
func instanceCtorBody(t *testing.T, model, ctor string) string {
	t.Helper()
	i := strings.Index(model, "func "+ctor+"(")
	if i < 0 {
		t.Fatalf("%s not found in generated model.go:\n%s", ctor, model)
	}
	rest := model[i:]
	end := strings.Index(rest, "\n}\n")
	if end < 0 {
		end = len(rest)
	}
	return rest[:end]
}
