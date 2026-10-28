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
