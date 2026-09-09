package android

import (
	"strings"
	"testing"
)

// A component that survives inlining -- one under a dynamic `for`, or in a
// recursive cycle -- keeps a body of its own, and its `var`s are per-instance
// state. On Compose that is `remember`: the cell belongs to the place in the
// composition, so two rows of a list hold two counters.
//
// Before this, a surviving component's composable declared nothing at all. Its
// vars were named but never declared, its computeds were hoisted into
// MainScreen (where the vars they read do not exist), and its funcs were
// emitted as top-level `fun`s writing bare names. None of it compiled.
const surviverSrc = `import . "sngl:ui"
import app "sngl:app"

struct Item {
    name string = ""
}

component card(name = "") node {
    var clicks = 0
    var tags list<string> = ["a"]
    func label() => "{name}:{clicks}:{tags.length()}"
    button(text = label(), @click { bump() })
    func bump() {
        clicks += 1
    }
}

app.window {
    var items list<Item> = [{name = "a"}, {name = "b"}]
    for var item = items {
        card(name = item.name)
    }
    button #add(text = "add", @click { items.push({name = "x"}) })
}
`

// split returns the MainScreen composable and the Card composable separately,
// so an assertion can say which one a declaration landed in.
func splitComposables(t *testing.T, out string) (main, card string) {
	t.Helper()
	i := strings.Index(out, "fun MainScreen(")
	j := strings.Index(out, "fun Card(")
	if i < 0 || j < 0 || j < i {
		t.Fatalf("expected a MainScreen and a Card composable\n--- generated ---\n%s", out)
	}
	return out[i:j], out[j:]
}

func TestSurvivingComponentRemembersItsOwnState(t *testing.T) {
	out := compileSrc(t, surviverSrc, false)
	mainFn, cardFn := splitComposables(t, out)

	for _, want := range []string{
		"var clicks by remember { mutableStateOf(0) }",
		`val tags = remember { mutableStateListOf("a") }`,
		"val label by remember { derivedStateOf {",
		"fun bump() {",
	} {
		if !strings.Contains(cardFn, want) {
			t.Errorf("Card composable is missing %q\n--- Card ---\n%s", want, cardFn)
		}
	}

	// The same declarations must not also be in MainScreen: the vars they read
	// are the instance's, and MainScreen has none of them.
	for _, leak := range []string{"clicks", "tags", "label", "bump"} {
		if strings.Contains(mainFn, leak) {
			t.Errorf("MainScreen names Card's %q\n--- MainScreen ---\n%s", leak, mainFn)
		}
	}

	// Nor at file scope. A component's func reads the component's state, and
	// at top level that state is out of scope.
	if strings.Contains(out, "\nfun bump(") {
		t.Errorf("bump() emitted as a top-level fun\n--- generated ---\n%s", out)
	}
}
