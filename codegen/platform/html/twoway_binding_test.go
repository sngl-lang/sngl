package html

import (
	"strings"
	"testing"
)

// TestTwoWayBindingUsesEventTargetValue guards two regressions in the hello
// example: (1) the `:value=name` two-way binding's write-back, which html's
// input override makes from the DOM event, must read the element's value via
// `e.currentTarget.value`, not a bare `e.value` (which is undefined — typing
// set the bound var to undefined); (2) the initial state
// value must seed the input (`.value = state.name`) so the field shows its
// initial value rather than being empty.
func TestTwoWayBindingUsesEventTargetValue(t *testing.T) {
	src := `
import . "sngl:ui"
output { none { html() } }
window {
    var name = "World"
    text(value="Hello, {name}!")
    input(:value=name, placeholder="Enter your name")
}
`
	out := generateMainPage(t, src)

	if !strings.Contains(out, "state.name = e.currentTarget.value") {
		t.Errorf("two-way binding handler does not read e.currentTarget.value:\n%s", out)
	}
	if strings.Contains(out, "= e.value") {
		t.Errorf("two-way binding handler reads undefined e.value instead of e.currentTarget.value:\n%s", out)
	}
	if !strings.Contains(out, ".value = state.name") {
		t.Errorf("input is not seeded with its initial bound value:\n%s", out)
	}
}
