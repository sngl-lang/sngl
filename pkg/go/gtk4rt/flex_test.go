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
// it expands itself, as a GIR widget given `hexpand` does, and none where GTK
// carries the expand up from something it holds: a CSS flex item grows by its
// own flex, never by what is inside it, so a box holding such a widget is its
// content's size, as it is on html.
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
	expander := BoxNew(OrientationHorizontal, 0)
	setHExpand(expander, true)
	holder, inner := BoxNew(OrientationHorizontal, 0), BoxNew(OrientationHorizontal, 0)
	setHExpand(inner, true)
	BoxAppend(holder, inner)
	for _, c := range []Handle{a, b, expander, holder} {
		BoxAppend(row, c)
	}
	allocate(row, 530, 10)
	got := [4]int{allocatedWidth(a), allocatedWidth(b), allocatedWidth(expander), allocatedWidth(holder)}
	if want := [4]int{100, 300, 100, 0}; got != want {
		t.Errorf("weights 1, 3, a widget expanding and a box holding one, of 500: widths %v, want %v", got, want)
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

// A box gtk4rt lays out asks its children the way GTK's own box does: of
// constant size where they all are, width for height where most of them are.
// It always said height for width, so GTK never asked it a width for the
// height it had.
func TestLookAsksItsChildrenAsGTKsBoxWould(t *testing.T) {
	if !hasDisplay() {
		t.Skip("gtk4 needs an X11/Wayland display")
	}
	Init()
	box := func(weighted bool, wide bool) Handle {
		row := BoxNew(OrientationHorizontal, 0)
		Retain(row)
		a, b := BoxNew(OrientationHorizontal, 0), BoxNew(OrientationHorizontal, 0)
		if weighted {
			SetLook(a, Look{Grow: 1})
			SetLook(b, Look{Grow: 3})
		}
		BoxAppend(row, a)
		BoxAppend(row, b)
		if wide {
			BoxAppend(row, widthForHeight())
		}
		return row
	}
	for _, wide := range []bool{false, true} {
		gtks, ours := box(false, wide), box(true, wide)
		if boxLaidOutByGTK(ours) {
			t.Fatal("weights 1 and 3: GTK lays the box out")
		}
		if g, o := requestMode(gtks), requestMode(ours); g != o {
			t.Errorf("holding a width-for-height child %v: GTK's box asks in mode %d, gtk4rt's in %d", wide, g, o)
		}
	}
}

// A box gtk4rt lays out turned to a column is a column. GtkBox sets its
// orientation on its layout manager, which was gtk4rt's and is no
// GtkBoxLayout, so the turn was lost and the box stayed a row.
func TestLookTurnsABoxItLaysOut(t *testing.T) {
	if !hasDisplay() {
		t.Skip("gtk4 needs an X11/Wayland display")
	}
	Init()
	box := BoxNew(OrientationHorizontal, 0)
	Retain(box)
	SetLook(box, Look{Spaced: true, Spacing: 10})
	a, b := BoxNew(OrientationHorizontal, 0), BoxNew(OrientationHorizontal, 0)
	SetLook(a, Look{Grow: 1})
	SetLook(b, Look{Grow: 3})
	BoxAppend(box, a)
	BoxAppend(box, b)
	OrientableSetOrientation(box, OrientationVertical)
	if h, v := expands(a); h || !v {
		t.Errorf("turned to a column: the first child expands h=%v v=%v, want v only", h, v)
	}
	allocate(box, 50, 410)
	if ha, hb := allocatedHeight(a), allocatedHeight(b); ha != 100 || hb != 300 {
		t.Errorf("weights 1 and 3 of 400 down the turned box: heights %d and %d, want 100 and 300", ha, hb)
	}
	OrientableSetOrientation(box, OrientationHorizontal)
	allocate(box, 410, 50)
	if wa, wb := allocatedWidth(a), allocatedWidth(b); wa != 100 || wb != 300 {
		t.Errorf("turned back to a row: widths %d and %d, want 100 and 300", wa, wb)
	}
}

// In a box GTK lays out, a widget a program made grows by its own expand and
// never by what it holds, as a CSS flex item grows by its own flex: a box
// holding a widget given `hexpand` is its content's size, as it is on html,
// and the widget given it takes the room. GTK carries an expand up to every
// ancestor that sets none of its own, so the holder took the row's spare room.
func TestABoxGrowsNotForWhatItHolds(t *testing.T) {
	if !hasDisplay() {
		t.Skip("gtk4 needs an X11/Wayland display")
	}
	Init()
	row, holder, inner := New("GtkBox"), New("GtkBox"), New("GtkBox")
	Retain(row)
	expander := New("GtkBox")
	SetBool(inner, "hexpand", true)
	SetBool(expander, "hexpand", true)
	BoxAppend(holder, inner)
	BoxAppend(row, holder)
	BoxAppend(row, expander)
	allocate(row, 300, 10)
	if wh, we := allocatedWidth(holder), allocatedWidth(expander); wh != 0 || we != 300 {
		t.Errorf("a box holding a widget given hexpand, beside one given it: widths %d and %d, want 0 and 300", wh, we)
	}
	if computesHExpand(holder) {
		t.Error("the holder computes an expand from the widget it holds")
	}
}
