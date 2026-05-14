package html

import (
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/internal/testutil"
)

// TestIntegration_ReactiveIfEmitsRenderSlot verifies that a reactive
// `if` inside a component compiles, via passReactivity + htmlTranslator
// + WalkLowered + JsIRContext, into a JS __renderSlot0 function that
// rebuilds DOM children when the gating state var mutates.
//
// This locks in the Plan D Task 9 wiring: synthesized __slotN vars,
// __renderSlotN funcs, and the initial __renderSlot0(__root) call must
// appear in the emitted <script>, with no untranslated lower.* IR
// intrinsics leaking through.
func TestIntegration_ReactiveIfEmitsRenderSlot(t *testing.T) {
	src := `
component main {
    var visible bool = true
    button(text="toggle", @click { visible = !visible })
    if visible {
        text(value="hi")
    }
}
`
	out := generateHTMLFromSample(t, testutil.Sample{
		Filename: "t.sngl",
		Source:   src,
	})

	for _, snippet := range []string{
		"function __renderSlot0",
		"let __slot0 = []",
		"for (const __entry of __slot0)",
		".removeChild(__entry)",
		"__slot0 = []",
		"if (state.visible)",
		"__slot0.push(",
		"__renderSlot0(",
	} {
		if !strings.Contains(out, snippet) {
			t.Errorf("emitted JS missing snippet %q\n--- generated ---\n%s", snippet, out)
		}
	}

	for _, leak := range []string{
		"lower.CreateNode",
		"lower.AppendChild",
		"lower.RemoveChild",
		"stdlib.ListPush",
	} {
		if strings.Contains(out, leak) {
			t.Errorf("untranslated intrinsic %q leaked into emitted JS", leak)
		}
	}
}

// TestIntegration_ReactiveForEmitsRenderSlot verifies a reactive `for`
// loop emits a slot render fn that iterates the bound list and
// rebuilds DOM children, with each iteration's child appended to the
// slot accumulator for teardown on the next render.
func TestIntegration_ReactiveForEmitsRenderSlot(t *testing.T) {
	src := `
component main {
    var items list<string> = ["a", "b"]
    button(text="add", @click { items = items.push("c") })
    for item = items {
        text(value=item)
    }
}
`
	out := generateHTMLFromSample(t, testutil.Sample{
		Filename: "t.sngl",
		Source:   src,
	})

	for _, snippet := range []string{
		"function __renderSlot0",
		"let __slot0 = []",
		"for (const __entry of __slot0)",
		"for (const item of state.items)",
		"__slot0.push(",
		"__renderSlot0(",
	} {
		if !strings.Contains(out, snippet) {
			t.Errorf("emitted JS missing snippet %q\n--- generated ---\n%s", snippet, out)
		}
	}
}
