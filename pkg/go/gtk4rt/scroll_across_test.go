//go:build !js

package gtk4rt

import (
	"strings"
	"testing"
)

// A vertical scroll is not as wide as its content: what is wider is cut off
// at its edge, as html and android cut it.
func TestAVerticalScrollIsNotAsWideAsItsContent(t *testing.T) {
	if !hasDisplay() {
		t.Skip("gtk4 needs an X11/Wayland display")
	}
	Init()
	sw := ScrollNew()
	Retain(sw)
	wide := LabelNew(strings.Repeat("wide ", 40))
	ScrolledWindowSetChild(sw, wide)
	content := minWidth(wide)
	if w := minWidth(sw); w >= content {
		t.Errorf("a vertical scroll asks for %dpx across, the width of its content", w)
	}
	if w := naturalWidth(sw); w < content {
		t.Errorf("a vertical scroll would be %dpx across given the room, narrower than its content's %dpx", w, content)
	}
}
