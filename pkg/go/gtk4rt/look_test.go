//go:build !js

package gtk4rt

import "testing"

func TestLookGrowsAlongItsBoxsAxis(t *testing.T) {
	if !hasDisplay() {
		t.Skip("gtk4 needs an X11/Wayland display")
	}
	Init()
	row, col := BoxNew(OrientationHorizontal, 0), BoxNew(OrientationVertical, 0)
	Retain(row)
	Retain(col)
	b := LabelNew("b")
	Retain(b)
	SetLook(b, Look{Grow: 1})
	if h, v := expands(b); h || v {
		t.Fatalf("expands h=%v v=%v before it has a box; want neither", h, v)
	}
	BoxAppend(row, b)
	if h, v := expands(b); !h || v {
		t.Fatalf("in a row: expands h=%v v=%v; want h only", h, v)
	}
	BoxRemove(row, b)
	BoxAppend(col, b)
	if h, v := expands(b); h || !v {
		t.Fatalf("moved to a column: expands h=%v v=%v; want v only", h, v)
	}
	SetLook(b, Look{})
	if h, v := expands(b); h || v {
		t.Fatalf("flex unset: expands h=%v v=%v; want neither", h, v)
	}
}

func TestLookAlignsWhatABoxHoldsLater(t *testing.T) {
	if !hasDisplay() {
		t.Skip("gtk4 needs an X11/Wayland display")
	}
	Init()
	col := BoxNew(OrientationVertical, 0)
	Retain(col)
	SetLook(col, Look{Cross: 2})
	a := LabelNew("a")
	BoxAppend(col, a)
	if h, _ := aligns(a); h != 3 {
		t.Fatalf("a child appended after alignItems=center: halign %d, want GTK_ALIGN_CENTER (3)", h)
	}
	fixed := LabelNew("fixed")
	SetLook(col, Look{})
	SetLook(fixed, Look{Width: 40})
	BoxAppend(col, fixed)
	if h, _ := aligns(a); h != 0 {
		t.Fatalf("alignItems unset: halign %d, want GTK_ALIGN_FILL (0)", h)
	}
	if h, _ := aligns(fixed); h != 1 {
		t.Fatalf("a child of fixed width in a stretching column: halign %d, want GTK_ALIGN_START (1)", h)
	}
}

func TestLookGivesBackWhatItTook(t *testing.T) {
	if !hasDisplay() {
		t.Skip("gtk4 needs an X11/Wayland display")
	}
	Init()
	l := LabelNew("l")
	Retain(l)
	before := labelXAlign(l)
	SetLook(l, Look{TextAlign: 2, Hidden: true, Css: "color: #ff0000;"})
	if labelXAlign(l) != 1 || shown(l) {
		t.Fatalf("textAlign=right, display=none: xalign %v shown %v", labelXAlign(l), shown(l))
	}
	SetLook(l, Look{})
	if labelXAlign(l) != before || !shown(l) {
		t.Fatalf("unset again: xalign %v shown %v; want %v and shown", labelXAlign(l), shown(l), before)
	}
}

func TestLookGrowsNoBoxAroundIt(t *testing.T) {
	if !hasDisplay() {
		t.Skip("gtk4 needs an X11/Wayland display")
	}
	Init()
	outer, inner := BoxNew(OrientationVertical, 0), BoxNew(OrientationVertical, 0)
	Retain(outer)
	BoxAppend(outer, inner)
	b := LabelNew("b")
	SetLook(b, Look{Grow: 1})
	BoxAppend(inner, b)
	if _, v := expands(b); !v {
		t.Fatal("a growing child does not expand along its column")
	}
	if computesVExpand(inner) {
		t.Error("the column holding a growing child expands in its own parent; a CSS flex item grows no box")
	}
}

func TestLookGivesBackAChildsOwnAlignment(t *testing.T) {
	if !hasDisplay() {
		t.Skip("gtk4 needs an X11/Wayland display")
	}
	Init()
	col := BoxNew(OrientationVertical, 0)
	Retain(col)
	a := LabelNew("a")
	setHAlign(a, 2)
	BoxAppend(col, a)
	SetLook(col, Look{Cross: 2})
	SetLook(col, Look{})
	if h, _ := aligns(a); h != 2 {
		t.Errorf("alignItems set and unset: halign %d, want the GTK_ALIGN_END (2) the child had", h)
	}
}

func TestLookGivesBackVisibilityAPropSet(t *testing.T) {
	if !hasDisplay() {
		t.Skip("gtk4 needs an X11/Wayland display")
	}
	Init()
	l := LabelNew("l")
	Retain(l)
	setShown(l, false)
	SetLook(l, Look{Hidden: true})
	SetLook(l, Look{})
	if shown(l) {
		t.Error("display=none then unset showed a widget its own visible kept hidden")
	}
}

func TestLookAlignsAButtonsText(t *testing.T) {
	if !hasDisplay() {
		t.Skip("gtk4 needs an X11/Wayland display")
	}
	Init()
	b := ButtonNew()
	Retain(b)
	ButtonSetLabel(b, "go")
	SetLook(b, Look{TextAlign: 2})
	if x := labelXAlign(firstChild(b)); x != 1 {
		t.Errorf("textAlign=right on a button: its label's xalign %v, want 1", x)
	}
}

func TestLookGivesTheBoxBackItsExpand(t *testing.T) {
	if !hasDisplay() {
		t.Skip("gtk4 needs an X11/Wayland display")
	}
	Init()
	col := BoxNew(OrientationVertical, 0)
	Retain(col)
	b := LabelNew("b")
	SetLook(b, Look{Grow: 1})
	BoxAppend(col, b)
	spacer := LabelNew("spacer")
	setVExpand(spacer, true)
	BoxAppend(col, spacer)
	SetLook(b, Look{})
	if !computesVExpand(col) {
		t.Error("the growing child let go and the box still refuses its other child's expand")
	}
}

func TestLookUnalignsAChildMovedOut(t *testing.T) {
	if !hasDisplay() {
		t.Skip("gtk4 needs an X11/Wayland display")
	}
	Init()
	centered, plain := BoxNew(OrientationVertical, 0), BoxNew(OrientationVertical, 0)
	Retain(centered)
	Retain(plain)
	SetLook(centered, Look{Cross: 2})
	a := LabelNew("a")
	Retain(a)
	BoxAppend(centered, a)
	BoxRemove(centered, a)
	BoxAppend(plain, a)
	if h, _ := aligns(a); h != 0 {
		t.Errorf("moved to a box that aligns nothing: halign %d, want GTK_ALIGN_FILL (0)", h)
	}
}
