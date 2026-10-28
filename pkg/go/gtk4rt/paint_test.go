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

// A frame draws the theme's border on its own node, so a look's border
// replaces it rather than drawing beside it, and `border-style: none`, what a
// clickable card's inner frame is given under a border of the button's,
// leaves none.
func TestLookReplacesAFramesBorder(t *testing.T) {
	paintable(t)
	edge := func(css string) [3]uint32 {
		f := New("GtkFrame")
		Retain(f)
		SetLook(f, Look{Css: "min-height: 20px; " + css})
		rows := painted(f, 0)
		if len(rows) == 0 {
			t.Fatalf("%q: the frame painted nothing", css)
		}
		mid := rows[len(rows)/2]
		return [3]uint32{mid[0], mid[1], mid[2]}
	}
	if got := edge(""); got[0]>>24 != 0xff {
		t.Fatalf("a frame given no border: left edge %08x, want the theme's opaque border", got)
	}
	red := func(px uint32) bool { return px>>24 > 0xf0 && px&0xffff == 0 }
	if got := edge("border: 2px solid #ff0000; "); !red(got[0]) || !red(got[1]) || got[2] != 0 {
		t.Errorf("a 2px red border: left edge %08x, want two red pixels and nothing beside them", got)
	}
	if got := edge("border-style: none; "); got != [3]uint32{} {
		t.Errorf("border-style none: left edge %08x, want nothing drawn", got)
	}
}

// A flow's run and a label given the same font size are the same height:
// both are pixels, the run's through font_desc and the label's through CSS,
// where a run's `size` is in points.
func TestLookFontSizeAgreesWithARun(t *testing.T) {
	paintable(t)
	col := BoxNew(OrientationVertical, 0)
	Retain(col)
	styled, run, pointed := LabelNew("Hg"), FlowNew(), FlowNew()
	SetLook(styled, Look{Css: "font-size: 32px; "})
	LabelSetMarkup(run, `<span font_desc="32px">Hg</span>`)
	LabelSetMarkup(pointed, `<span size="32768">Hg</span>`)
	for _, l := range []Handle{styled, run, pointed} {
		BoxAppend(col, l)
	}
	painted(col, 0)
	if hs, hr := allocatedHeight(styled), allocatedHeight(run); hs != hr {
		t.Errorf("a label at font-size 32px is %d tall, a run at 32px %d", hs, hr)
	}
	if hr, hp := allocatedHeight(run), allocatedHeight(pointed); hp <= hr {
		t.Errorf("a run at 32pt is %d tall, no taller than one at 32px (%d)", hp, hr)
	}
}
