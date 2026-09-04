package fyne

import (
	"regexp"
	"strings"
	"testing"
)

// TestInlinedInstanceMethodDispatchesThroughTheModel compiles the Go emitted
// for testdata/test_named_component_props.sngl, whose three `Pair(...)` call
// sites each inline to a clone of the component's `both()` computed.
//
// The clones are hoisted onto main, so fyne emits them as Model methods --
// `func (m *Model) both__inst0() string`. The call site named the free
// function `PairBoth__inst0()` instead, which nothing declares. What made the
// receiver look free was a name search over pkg.Funcs and the components still
// on the list; a hoisted clone is in neither, and the component it names has
// been inlined away.
func TestInlinedInstanceMethodDispatchesThroughTheModel(t *testing.T) {
	model := generateFyneModel(t, fixtureSource(t, "test_named_component_props.sngl"))

	for _, want := range []string{
		"func (m *Model) both__inst0() string {",
		"m.__n0.SetText(m.both__inst0())",
	} {
		if !strings.Contains(model, want) {
			t.Errorf("emitted Go missing %q\n--- model.go ---\n%s", want, model)
		}
	}
	// The free-function spelling is the bug, and no target emits a definition
	// under that name.
	if free := regexp.MustCompile(`PairBoth__inst\d`).FindString(model); free != "" {
		t.Errorf("call site still names the free function %q, which nothing declares\n--- model.go ---\n%s", free, model)
	}
	buildGeneratedGo(t, "fyne-inlined-method-", model)
}
