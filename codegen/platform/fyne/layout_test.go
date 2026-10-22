package fyne

import (
	"strings"
	"testing"
)

// TestFlexBuildsTheWeightedLayout: a box whose children carry `style.flex` is
// built with the SNGL weighted layout rather than with Fyne's own box, which
// packs at minimum size and has nowhere for a child to claim a share. Each
// child's flex and margin are said on the child, and the box's own gap and
// padding travel with the layout.
func TestFlexBuildsTheWeightedLayout(t *testing.T) {
	out := generateFyneModel(t, `
import . "sngl:ui"
output { go { fyne } }
window {
    hbox(style={gap=4px, padding=6px}) {
        text(value="a", style={flex=1, margin=3px})
        text(value="b", style={flex=2})
    }
}
`)
	for _, want := range []string{
		`"duckfam.us/sngl/pkg/go/fynelayout"`,
		"container.New(fynelayout.New(true, 4, 6))",
		"fynelayout.Share(m.__n2, 1, 3)",
		"fynelayout.Share(m.__n3, 2, 0)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("emitted Go missing %q\n--- generated ---\n%s", want, out)
		}
	}
	if strings.Contains(out, "container.NewHBox()") {
		t.Errorf("flexing box still built with the packing constructor\n--- generated ---\n%s", out)
	}
}

// TestNoFlexKeepsTheBoxConstructor: a box whose style and children ask for
// nothing stays exactly what it was -- swapping a layout in there would change
// a tree nothing asked to change. One whose style names a gap is the weighted
// layout, the only one that reads it.
func TestNoFlexKeepsTheBoxConstructor(t *testing.T) {
	out := generateFyneModel(t, `
import . "sngl:ui"
output { go { fyne } }
window {
    vbox {
        text(value="a")
        text(value="b")
    }
}
`)
	if !strings.Contains(out, "container.NewVBox()") {
		t.Errorf("box asking for nothing should keep NewVBox\n--- generated ---\n%s", out)
	}
	// fynelayout is imported for the window's Toplevel either way; the
	// weighted layout is what a flexing child would pull in.
	if strings.Contains(out, "fynelayout.New(") {
		t.Errorf("box asking for nothing should not pull in the weighted layout\n--- generated ---\n%s", out)
	}

	gapped := generateFyneModel(t, `
import . "sngl:ui"
output { go { fyne } }
window {
    vbox(style={gap=4px}) {
        text(value="a")
        text(value="b")
    }
}
`)
	if !strings.Contains(gapped, "container.New(fynelayout.New(false, 4, 0))") {
		t.Errorf("box with a gap should be the weighted layout with that gap\n--- generated ---\n%s", gapped)
	}
}
