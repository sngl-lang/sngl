package html

import (
	"strings"
	"testing"
)

// A reactive `if` whose condition is a zero-arg derived func must re-fire when
// a var that func transitively reads is mutated. Regression for the tutorial
// "derived state" lesson: `func isLong() => name.length > 3` gating
// `if isLong { ... }`, with an input bound to `name`. Two bugs combined here:
//   - the checker analysed component methods against package-only vars, so
//     isLong was mis-typed PurityPure with empty Reads — the optimizer then
//     const-folded `if isLong()` to a static node that never updated;
//   - reactivity's dep gather didn't follow a call into the func's reads, so
//     even un-folded the slot had no dep on `name` and never re-fired.
func TestDerivedFuncGatesReactiveSlot(t *testing.T) {
	src := `
import . "sngl:ui"
output { none { html() } }
component main {
    var name = "world"
    func isLong() => name.length > 3
    vbox {
        input(:value=name, placeholder="Name")
        if isLong { text(value="That's a long name.") }
    }
}
`
	out := generateMainPage(t, src)

	// The conditional must be lowered to a reactive slot, not const-folded.
	if !strings.Contains(out, "function __renderSlot0") {
		t.Fatalf("derived-func `if` was not lowered to a reactive slot (likely const-folded):\n%s", out)
	}
	// The slot must re-evaluate the condition on each render (not bake the
	// initial result).
	if !strings.Contains(out, "if (main_isLong(state))") {
		t.Errorf("slot body does not re-evaluate the derived func:\n%s", out)
	}
	// Mutating `name` in the input handler must re-fire the slot.
	handler := out[strings.Index(out, "state.name ="):]
	if i := strings.Index(handler, "});"); i >= 0 {
		handler = handler[:i]
	}
	if !strings.Contains(handler, "__renderSlot0(__slotAnchor_0)") {
		t.Errorf("input handler does not re-fire the derived-func slot after mutating name:\n%s", handler)
	}
}

// A two-level derived-func chain must still propagate the underlying dep: the
// slot condition calls a func that calls another func that reads the var.
func TestDerivedFuncChainGatesReactiveSlot(t *testing.T) {
	src := `
import . "sngl:ui"
output { none { html() } }
component main {
    var name = "world"
    func longish() => name.length > 3
    func isLong() => longish()
    vbox {
        input(:value=name, placeholder="Name")
        if isLong { text(value="long") }
    }
}
`
	out := generateMainPage(t, src)
	handler := out[strings.Index(out, "state.name ="):]
	if i := strings.Index(handler, "});"); i >= 0 {
		handler = handler[:i]
	}
	if !strings.Contains(handler, "__renderSlot0(__slotAnchor_0)") {
		t.Errorf("transitive derived-func slot not re-fired on dep mutation:\n%s", handler)
	}
}

// A `!!` toggle of a reactive var must splice the dependent updaters, exactly
// as a plain assignment does. Regression for the tutorial "comparison and
// logical operators" lesson: `button(@click { enabled!! })` left the
// `text(value="enabled: {enabled}")` stale because updatersFor only handled
// *ir.Assign, not *ir.Toggle.
func TestToggleSplicesReactiveUpdaters(t *testing.T) {
	src := `
import . "sngl:ui"
output { none { html() } }
component main {
    var enabled = true
    button(text="Toggle", @click { enabled!! })
    text(value="enabled: {enabled}")
}
`
	out := generateMainPage(t, src)
	handler := out[strings.Index(out, `addEventListener("click"`):]
	if i := strings.Index(handler, "});"); i >= 0 {
		handler = handler[:i]
	}
	if !strings.Contains(handler, "state.enabled = !state.enabled") {
		t.Fatalf("toggle did not flip the var:\n%s", handler)
	}
	if !strings.Contains(handler, "textContent") {
		t.Errorf("toggle handler did not splice the dependent text updater:\n%s", handler)
	}
}

// Mutating a struct field of a reactive var (`u.score += 10`) must update
// props that read the var. Regression for the tutorial "structs" lesson
// ("button causes no change"): the mutation target `u.score` is a Select
// rooted at `u`, which assignTargetVar didn't peel to the reactive var, so no
// dependent updater was spliced.
func TestStructFieldMutationSplicesUpdaters(t *testing.T) {
	src := `
import . "sngl:ui"
output { none { html() } }
struct User {
    name string = "anon"
    score int = 0
}
component main {
    var u User = User{name = "Ada", score = 42}
    text(value="Score: {u.score}")
    button(text="+10", @click { u.score += 10 })
}
`
	out := generateMainPage(t, src)
	handler := out[strings.Index(out, `addEventListener("click"`):]
	if i := strings.Index(handler, "});"); i >= 0 {
		handler = handler[:i]
	}
	if !strings.Contains(handler, "state.u.score += 10") {
		t.Fatalf("struct-field mutation missing:\n%s", handler)
	}
	if !strings.Contains(handler, "textContent") {
		t.Errorf("struct-field mutation did not splice the dependent text updater:\n%s", handler)
	}
}

// A statement-level in-place list method (`tasks.push(x)`) mutates its
// receiver, so dependent props and reactive for-slots must update. Regression
// for the tutorial "lists" lesson ("button does nothing"): push/remove are a
// *ir.CallStmt, which updatersFor ignored, so neither the "{tasks.length}"
// text nor the `for var t = tasks` loop refreshed when the Add button pushed.
func TestListPushSplicesUpdatersAndRefiresSlot(t *testing.T) {
	src := `
import . "sngl:ui"
output { none { html() } }
struct Task {
    label string = ""
}
component main {
    var tasks list<Task> = [Task{label = "A"}]
    text(value="{tasks.length} tasks")
    for var t = tasks {
        text(value=t.label)
    }
    button(text="Add", @click { tasks.push(Task{label = "B"}) })
}
`
	out := generateMainPage(t, src)
	// Bound the click handler at the init function rather than the first
	// "});" — `tasks.push({...})` ends in "})", so a "});" cut would truncate
	// the handler right after the push, before the spliced updaters.
	handler := out[strings.Index(out, `addEventListener("click"`):]
	if i := strings.Index(handler, "__sngl_init"); i >= 0 {
		handler = handler[:i]
	}
	if !strings.Contains(handler, ".push(") {
		t.Fatalf("push call missing:\n%s", handler)
	}
	if !strings.Contains(handler, "textContent") {
		t.Errorf("push did not splice the dependent length-text updater:\n%s", handler)
	}
	if !strings.Contains(handler, "__renderSlot0(__slotAnchor_0)") {
		t.Errorf("push did not re-fire the `for tasks` reactive slot:\n%s", handler)
	}
}

// String/math methods are intrinsic-backed and inlined to a direct
// stdlib.<Intrinsic> call before codegen. Dispatching intrinsics by ID (the
// registry) emits the native form regardless — guarding against the prior bug
// where an inlined `s.upper()` emitted a bare, undefined `stdlib.StrUpper(...)`
// call instead of `.toUpperCase()`.
func TestStringIntrinsicEmitsNativeAfterInlining(t *testing.T) {
	src := `
import . "sngl:ui"
output { none { html() } }
component main {
    var s = "hi"
    text(value="{s.upper()} {s.length}")
    button(text="x", @click { s = s.upper() })
}
`
	out := generateMainPage(t, src)
	if !strings.Contains(out, ".toUpperCase()") {
		t.Errorf("string.upper did not emit .toUpperCase():\n%s", out)
	}
	if strings.Contains(out, "stdlib.StrUpper") {
		t.Errorf("string.upper emitted an undefined stdlib.StrUpper call:\n%s", out)
	}
	if !strings.Contains(out, ".length") {
		t.Errorf("string.length did not emit .length:\n%s", out)
	}
}
