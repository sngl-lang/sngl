package gtk4

import (
	"strings"
	"testing"
)

// loopComponentSrc is testdata/test_html_loop_component.sngl: two rows of a
// component that holds its own `var clicks`, under a dynamic `for`.
const loopComponentSrc = `
import . "sngl:ui"

struct Item {
    name string = ""
}

component card(name = "") node {
    var clicks = 0
    button(text="{name} ({clicks})", @click { clicks += 1 })
}

window {
    var items list<Item> = [
        {name="a"},
        {name="b"},
    ]
    for var item = items {
        card(name=item.name)
    }
}
`

// TestInstanceRecordIsPerRow asserts the emitted Go gives each row its own
// state, and compiles.
//
// gtk4 gets the compile check rather than a run: constructing a widget needs a
// GTK display, and what was broken here was the types -- a component's state
// on the one Model, an instance handle spelled `any`, a registry typed as a
// list of widgets. None of that survives `go build`. fyne carries the
// runs-the-output half, over the same lowering (see
// codegen/platform/fyne/instance_test.go).
func TestInstanceRecordIsPerRow(t *testing.T) {
	model := generateGTK4Model(t, loopComponentSrc)

	for _, want := range []string{
		// The state cell belongs to the record.
		"type CardInstance struct {",
		"clicks      int",
		// A row is allocated, not rendered into the Model.
		"func newCardInstance(__model *Model, __a_name string) *CardInstance {",
		"c := &CardInstance{}",
		"__n2__new := newCardInstance(m, item.Name)",
		// The registry holds instances, and the reconcile reaches them
		// through the record's own surface.
		"m.__inst0_live = []*CardInstance{}",
		"__n2.SetName(item.Name)",
		"func (c *CardInstance) SetName(",
		"func (c *CardInstance) Destroy() {",
	} {
		if !strings.Contains(model, want) {
			t.Errorf("emitted Go missing %q\n--- model.go ---\n%s", want, model)
		}
	}
	// A component's own state on the Model is the bug this replaces.
	for _, leak := range []string{"m.clicks", "m.__prop_name", "m.renderCard("} {
		if strings.Contains(model, leak) {
			t.Errorf("a card's own state leaked onto the Model as %q\n--- model.go ---\n%s", leak, model)
		}
	}

	buildGTK4Model(t, "gtk4-inst-", model)
}
