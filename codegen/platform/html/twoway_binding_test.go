package html

import (
	"strings"
	"testing"
)

// TestTwoWayBindingUsesEventTargetValue guards two regressions in the hello
// example: (1) the `:value=name` two-way binding's synthesized input handler
// must read the DOM value via `e.target.value`, not a bare `e.value` (which is
// undefined — typing set the bound var to undefined); (2) the initial state
// value must seed the input (`.value = state.name`) so the field shows its
// initial value rather than being empty.
func TestTwoWayBindingUsesEventTargetValue(t *testing.T) {
	src := `
import . "sngl://std"
output { none { html() } }
component main {
    var name = "World"
    text(value="Hello, {name}!")
    input(:value=name, placeholder="Enter your name")
}
`
	out := generateMainPage(t, src)

	if !strings.Contains(out, "e.target.value") {
		t.Errorf("two-way binding handler does not read e.target.value:\n%s", out)
	}
	if strings.Contains(out, "= e.value") {
		t.Errorf("two-way binding handler reads undefined e.value instead of e.target.value:\n%s", out)
	}
	if !strings.Contains(out, ".value = state.name") {
		t.Errorf("input is not seeded with its initial bound value:\n%s", out)
	}
}
