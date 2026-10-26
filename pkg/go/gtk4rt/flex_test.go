//go:build !js

package gtk4rt

import "testing"

// sized is an empty box the size its look asks, which is its whole minimum
// and natural size.
func sized(width, height float64) Handle {
	b := BoxNew(OrientationHorizontal, 0)
	SetLook(b, Look{Width: width, Height: height})
	return b
}

// A box's justifyContent places its children in the room they leave, as
// CSS's does, where a GtkBox packs them from the start.
func TestLookJustifiesABox(t *testing.T) {
	if !hasDisplay() {
		t.Skip("gtk4 needs an X11/Wayland display")
	}
	Init()
	for _, c := range []struct {
		justify int
		xs      [2]int
	}{
		{0, [2]int{0, 50}},
		{1, [2]int{105, 155}},
		{2, [2]int{210, 260}},
		{3, [2]int{0, 260}},
		{4, [2]int{52, 207}},
		{5, [2]int{70, 190}},
	} {
		row := BoxNew(OrientationHorizontal, 0)
		Retain(row)
		a, b := sized(40, 10), sized(40, 10)
		BoxAppend(row, a)
		BoxAppend(row, b)
		SetLook(row, Look{Justify: c.justify, Spaced: true, Spacing: 10})
		allocate(row, 300, 10)
		if xa, xb := allocatedX(a), allocatedX(b); xa != c.xs[0] || xb != c.xs[1] {
			t.Errorf("justify %d: children at %d and %d, want %d and %d", c.justify, xa, xb, c.xs[0], c.xs[1])
		}
	}
}

// Growing children share the free space by their weights, where a GtkBox
// splits it evenly between the children that expand.
func TestLookSharesByWeight(t *testing.T) {
	if !hasDisplay() {
		t.Skip("gtk4 needs an X11/Wayland display")
	}
	Init()
	row := BoxNew(OrientationHorizontal, 0)
	Retain(row)
	SetLook(row, Look{Spaced: true, Spacing: 10})
	a, b := BoxNew(OrientationHorizontal, 0), BoxNew(OrientationHorizontal, 0)
	SetLook(a, Look{Grow: 1})
	SetLook(b, Look{Grow: 3})
	BoxAppend(row, a)
	BoxAppend(row, b)
	allocate(row, 410, 10)
	if wa, wb := allocatedWidth(a), allocatedWidth(b); wa != 100 || wb != 300 {
		t.Errorf("weights 1 and 3 of 400: widths %d and %d, want 100 and 300", wa, wb)
	}
	if x := allocatedX(b); x != 110 {
		t.Errorf("the second child at %d, want 110", x)
	}
	// Even again, the box is GTK's own.
	SetLook(b, Look{Grow: 1})
	if !boxLaidOutByGTK(row) {
		t.Error("even weights: the box kept gtk4rt's layout, where GTK's splits them the same")
	}
}

// Beside weighted children, one with no look of its own takes a share where
// it expands itself, as `ui.spacer` does and as html's `flex: 1` spacer
// does, and none where GTK carries the expand up from something it holds: a
// CSS flex item grows by its own flex, never by what is inside it, so a
// scroll holding a spacer is its content's size, as it is on html.
func TestLookSharesNoneForAnExpandItHolds(t *testing.T) {
	if !hasDisplay() {
		t.Skip("gtk4 needs an X11/Wayland display")
	}
	Init()
	row := BoxNew(OrientationHorizontal, 0)
	Retain(row)
	SetLook(row, Look{Spaced: true, Spacing: 10})
	a, b := BoxNew(OrientationHorizontal, 0), BoxNew(OrientationHorizontal, 0)
	SetLook(a, Look{Grow: 1})
	SetLook(b, Look{Grow: 3})
	spacer := BoxNew(OrientationHorizontal, 0)
	setHExpand(spacer, true)
	holder, inner := BoxNew(OrientationHorizontal, 0), BoxNew(OrientationHorizontal, 0)
	setHExpand(inner, true)
	BoxAppend(holder, inner)
	for _, c := range []Handle{a, b, spacer, holder} {
		BoxAppend(row, c)
	}
	allocate(row, 530, 10)
	got := [4]int{allocatedWidth(a), allocatedWidth(b), allocatedWidth(spacer), allocatedWidth(holder)}
	if want := [4]int{100, 300, 100, 0}; got != want {
		t.Errorf("weights 1, 3, a spacer and a box holding one, of 500: widths %v, want %v", got, want)
	}
}

// A column gtk4rt lays out shares its height. GtkBox keeps its orientation in
// its layout manager, so once gtk4rt's replaced GTK's, asking the box answered
// a row (and GTK said so on stderr), and a column's weights split its width.
func TestLookSharesAColumnsHeight(t *testing.T) {
	if !hasDisplay() {
		t.Skip("gtk4 needs an X11/Wayland display")
	}
	Init()
	col := BoxNew(OrientationVertical, 0)
	Retain(col)
	SetLook(col, Look{Spaced: true, Spacing: 10})
	a, b := BoxNew(OrientationHorizontal, 0), BoxNew(OrientationHorizontal, 0)
	SetLook(a, Look{Grow: 1})
	SetLook(b, Look{Grow: 3})
	BoxAppend(col, a)
	BoxAppend(col, b)
	allocate(col, 50, 410)
	if ha, hb := allocatedHeight(a), allocatedHeight(b); ha != 100 || hb != 300 {
		t.Errorf("weights 1 and 3 of 400 down a column: heights %d and %d, want 100 and 300", ha, hb)
	}
	if wa := allocatedWidth(a); wa != 50 {
		t.Errorf("a column's child is %d wide, want the column's 50", wa)
	}
}
