package android

import (
	"strings"
	"testing"
)

// A package func that reads only package state is a computed, which
// MainScreen remembered as a derivedStateOf local. A composable of its own --
// a component under a `for`, built at run time -- reaches the page only
// through the file, and named a local of MainScreen's nothing there declares:
// the Pokédex's `failing()`, read by the sign a page draws, was
// "unresolved reference 'failing'" at kotlinc.
const sharedComputedSrc = `import ui "sngl:ui"

var misses list[string] = []

func missing() => misses.filter(func(m string) => m != "")

component sign ui.node {
    var shown = true
    if shown && missing().length() > 0 {
        ui.button(text = missing()[0], @click { shown = false })
    }
}

ui.window {
    var rows list[string] = ["a", "b"]
    for var r = rows {
        sign()
        ui.button(text = r, @click { misses.push(r) })
    }
}
`

func TestAComputedAComposableReadsIsDeclaredInTheFile(t *testing.T) {
	out := compileSrc(t, sharedComputedSrc, false)
	main, rest, ok := strings.Cut(out, "fun MainScreen(")
	if !ok {
		t.Fatalf("no MainScreen\n%s", out)
	}
	if strings.Contains(rest[:strings.Index(rest, "\n}\n")], "val missing") {
		t.Errorf("missing is a local of MainScreen, which the sign's composable cannot name\n%s", out)
	}
	if !strings.Contains(main, "val missing: List<String> get() = ") {
		t.Errorf("missing is not a getter at file level\n%s", out)
	}
	if !strings.Contains(out, "fun Sign(") {
		t.Errorf("the sign is no composable of its own, so this test pins nothing\n%s", out)
	}
}
