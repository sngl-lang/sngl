package interp

import "testing"

// TestSuppliedSlotContentIsMounted covers what a call site puts inside a user
// component. The tree used to expand the component's own body and stop, so
// everything the caller supplied was invisible -- the default slot's ordinary
// children as much as a named slot's block.
func TestSuppliedSlotContentIsMounted(t *testing.T) {
	src := `import . "sngl:ui"

component frame(slot _, slot header) {
    vbox {
        header {}
        slot
    }
}

component main {
    var title = "Hello"
    frame {
        text #body(value="in the default slot")
        slot header {
            text #head(value=title)
        }
    }
}
`
	env, _ := envFor(t, src, "main")
	v, err := Mount(env)
	if err != nil {
		t.Fatalf("Mount: %v", err)
	}

	if got := len(v.Find("body")); got != 1 {
		t.Errorf("#body (default slot) resolved to %d nodes, want 1", got)
	}
	if got := len(v.Find("head")); got != 1 {
		t.Errorf("#head (named slot) resolved to %d nodes, want 1", got)
	}

	// Slot content is written at the call site, so it reads the caller's scope.
	if nodes := v.Find("head"); len(nodes) == 1 {
		if got := nodes[0].Props["value"]; got != "Hello" {
			t.Errorf("#head value = %v, want the caller's title %q", got, "Hello")
		}
	}
}

// TestSlotFallbackRendersWhenNothingIsSupplied is the other half of the rule:
// an insertion point's own block is what renders when the call site supplied
// nothing for it.
func TestSlotFallbackRendersWhenNothingIsSupplied(t *testing.T) {
	src := `import . "sngl:ui"

component frame(slot _) {
    vbox {
        slot {
            text #fallback(value="nothing supplied")
        }
    }
}

component main {
    frame()
}
`
	env, _ := envFor(t, src, "main")
	v, err := Mount(env)
	if err != nil {
		t.Fatalf("Mount: %v", err)
	}
	if got := len(v.Find("fallback")); got != 1 {
		t.Errorf("#fallback resolved to %d nodes, want 1", got)
	}
}
