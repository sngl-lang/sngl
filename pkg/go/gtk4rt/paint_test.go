//go:build !js

package gtk4rt

import (
	"testing"

	"duckfam.us/sngl/internal/testutil"
)

func paintable(t *testing.T) {
	t.Helper()
	if !hasDisplay() {
		t.Skip("gtk4 needs an X11/Wayland display")
	}
	if reason := testutil.GUIRenderSkipReason(); reason != "" {
		t.Skip(reason)
	}
	Init()
}

func center(rows [][]uint32) uint32 {
	if len(rows) == 0 {
		return 0
	}
	return rows[len(rows)/2][len(rows[0])/2]
}

// A button given a background still answers the pointer: the look's rule is
// at application priority on the button's node, so it outranked the theme's
// :hover and :active backgrounds and the button looked the same in all three.
func TestLookKeepsAButtonsFeedback(t *testing.T) {
	paintable(t)
	b := ButtonNew()
	Retain(b)
	SetLook(b, Look{Css: "background: #3366cc; "})
	normal := center(painted(b, 0))
	hover := center(painted(b, stateHover))
	active := center(painted(b, stateHover|stateActive))
	if normal != 0xff3366cc {
		t.Fatalf("the button's own background is %08x, want ff3366cc", normal)
	}
	if hover == normal || active == normal || active == hover {
		t.Errorf("normal %08x, hovered %08x, pressed %08x: want three different colors", normal, hover, active)
	}
}

// A popover's background is its body's and its arrow's, as the theme paints
// both. The look's provider reached only the popover's own node, and its body
// is a widget of its own, so neither was painted.
func TestLookPaintsAPopoversBodyAndArrow(t *testing.T) {
	paintable(t)
	po := New("GtkPopover")
	Retain(po)
	SetLook(po, Look{Css: "background: #3366cc; "})
	rows := painted(po, 0)
	if len(rows) < 20 {
		t.Fatalf("the popover painted %d rows", len(rows))
	}
	body, arrow := center(rows), rows[8][len(rows[0])/2]
	if body != 0xff3366cc || arrow != 0xff3366cc {
		t.Errorf("body %08x, arrow %08x: want both ff3366cc", body, arrow)
	}
}
