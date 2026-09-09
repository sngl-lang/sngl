package fyne

import (
	"strings"
	"testing"
)

// TestFlexBuildsTheWeightedLayout: a box whose children carry `style.flex` is
// built with the SNGL weighted layout rather than with Fyne's own box, which
// packs at minimum size and has nowhere for a child to claim a share. The
// weights are the children's flex in append order, and the box's own gap and
// padding travel with them.
func TestFlexBuildsTheWeightedLayout(t *testing.T) {
	out := generateFyneModel(t, `
import . "sngl:ui"
output { go { fyne } }
component main ui {
    hbox(style={gap=4, padding=6}) {
        text(value="a", style={flex=1, margin=3})
        text(value="b", style={flex=2})
    }
}
`)
	for _, want := range []string{
		`"git.duckfam.us/jonathan/sngl/pkg/go/fynelayout"`,
		"container.New(fynelayout.New(true, []float32{1, 2}, []float32{3, 0}, 4, 6))",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("emitted Go missing %q\n--- generated ---\n%s", want, out)
		}
	}
	if strings.Contains(out, "container.NewHBox()") {
		t.Errorf("flexing box still built with the packing constructor\n--- generated ---\n%s", out)
	}
}

// TestNoFlexKeepsTheBoxConstructor: the layout is the children's decision, so
// a box nothing claimed a share of stays exactly what it was -- swapping a
// layout in there would change a tree nothing asked to change.
func TestNoFlexKeepsTheBoxConstructor(t *testing.T) {
	out := generateFyneModel(t, `
import . "sngl:ui"
output { go { fyne } }
component main ui {
    vbox(style={gap=4}) {
        text(value="a")
        text(value="b")
    }
}
`)
	if !strings.Contains(out, "container.NewVBox()") {
		t.Errorf("box with no flexing child should keep NewVBox\n--- generated ---\n%s", out)
	}
	if strings.Contains(out, "fynelayout") {
		t.Errorf("box with no flexing child should not pull in the weighted layout\n--- generated ---\n%s", out)
	}
}
