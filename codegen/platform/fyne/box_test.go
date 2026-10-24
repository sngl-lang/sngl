package fyne

import (
	"strings"
	"testing"
)

// A box costs a widget in the tree and a theme override around it, so a node
// whose Style folds to asking for nothing is appended as itself.
func TestUnstyledWidgetsAreNotBoxed(t *testing.T) {
	out := generateFyneModel(t, `
import . "sngl:ui"
output { go { fyne } }
window {
    hbox {
        button(text="a")
        text(value="b")
    }
}
`)
	if strings.Contains(out, "NewBox") {
		t.Errorf("unstyled widgets should not be boxed\n--- generated ---\n%s", out)
	}
}
