package interp

import (
	"fmt"
	"testing"
)

// applyAndCompare drives a host through one change and checks the invariant
// MemHost exists for: a host that applied every patch holds exactly the tree
// the session holds.
func applyAndCompare(t *testing.T, step string, h *MemHost, s *Session, mutate func()) {
	t.Helper()
	mutate()
	patches, err := s.Sync()
	if err != nil {
		t.Fatalf("%s: Sync: %v", step, err)
	}
	if err := Apply(h, patches); err != nil {
		t.Fatalf("%s: Apply: %v", step, err)
	}
	if want, got := RenderView(s.View()), h.String(); want != got {
		t.Errorf("after %s the host diverged\npatches:\n%s--- session\n%s--- host\n%s",
			step, patchLines(patches), want, got)
	}
}

const oracleSrc = `import . "sngl:ui"

component main ui {
    var items = ["a", "b", "c"]
    vbox {
        for var it = items {
            text(value=it, key=it)
        }
    }
}
`

// TestEveryPermutationOfAKeyedListTracks is the property test the branch was
// missing. Reordering is where a patch list is easiest to get subtly wrong: the
// patches look plausible and the tree ends up in the wrong order.
func TestEveryPermutationOfAKeyedListTracks(t *testing.T) {
	perms := [][]any{
		{"a", "b", "c"}, {"a", "c", "b"}, {"b", "a", "c"},
		{"b", "c", "a"}, {"c", "a", "b"}, {"c", "b", "a"},
	}
	for _, from := range perms {
		for _, to := range perms {
			t.Run(fmt.Sprint(from, "->", to), func(t *testing.T) {
				s := sessionFor(t, oracleSrc, "main")
				setVar(s.Env, "items", from)
				if _, err := s.Sync(); err != nil {
					t.Fatal(err)
				}
				h := NewMemHost()
				if err := s.Attach(h); err != nil {
					t.Fatal(err)
				}
				applyAndCompare(t, "reorder", h, s, func() { setVar(s.Env, "items", to) })
			})
		}
	}
}

// TestInsertionsAndRemovalsTrack mixes creation with movement, which is where
// an index computed against the final tree and applied to an unreordered one
// goes wrong.
func TestInsertionsAndRemovalsTrack(t *testing.T) {
	cases := [][]any{
		{"b", "x", "a", "c"}, {"x", "a", "b", "c"}, {"a", "b", "c", "x"},
		{"c", "x", "a"}, {"a"}, {}, {"z", "y", "x"},
	}
	for _, to := range cases {
		t.Run(fmt.Sprint(to), func(t *testing.T) {
			s := sessionFor(t, oracleSrc, "main")
			h := NewMemHost()
			if err := s.Attach(h); err != nil {
				t.Fatal(err)
			}
			applyAndCompare(t, "change", h, s, func() { setVar(s.Env, "items", to) })
		})
	}
}

// TestReplacingAnElementTakesItsSubtreeWithIt: a node whose element changed at
// one key is removed and re-created, and Host.Remove unmounts everything
// beneath it -- so its surviving children have to be re-created too.
func TestReplacingAnElementTakesItsSubtreeWithIt(t *testing.T) {
	before := `import . "sngl:ui"

component main ui {
    vbox #box {
        text(value="kid")
    }
}
`
	after := `import . "sngl:ui"

component main ui {
    hbox #box {
        text(value="kid")
    }
}
`
	s := sessionFor(t, before, "main")
	h := NewMemHost()
	if err := s.Attach(h); err != nil {
		t.Fatal(err)
	}
	patches, err := s.Reload(check(t, after))
	if err != nil {
		t.Fatalf("Reload: %v", err)
	}
	if err := Apply(h, patches); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if want, got := RenderView(s.View()), h.String(); want != got {
		t.Errorf("a replaced element lost its subtree\npatches:\n%s--- session\n%s--- host\n%s",
			patchLines(patches), want, got)
	}
}
