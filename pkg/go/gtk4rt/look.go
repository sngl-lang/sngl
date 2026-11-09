//go:build !js

package gtk4rt

/*
#cgo pkg-config: gtk4
#cgo CFLAGS: -I${SRCDIR}/../../../lib/platform/gtk4/rt
#include <gtk/gtk.h>
#include <stdlib.h>

// The look is C shipped with the platform, which a C program compiles too.
#include "look.c"
*/
import "C"

import (
	"math"
	"unsafe"
)

// Look is what a ui.Style asks of a GTK widget, built by gtk4's style.sngl.
// Zero is unset in every field, and a field a later Look leaves unset gives
// the widget back what it had when a Look first set it.
type Look struct {
	// Css is declarations, without a selector.
	Css string
	// Width and Height are the size the widget is requested at, in pixels.
	// GTK may allocate more, never less.
	Width, Height float64
	// Grow is the widget's flex: above zero it expands along the axis of the
	// box it sits in.
	Grow float64
	// Cross is a box's alignItems, which it puts on each child: 1 start,
	// 2 center, 3 end, 4 baseline; zero stretches.
	Cross int
	// TextAlign is a label's, an entry's or a button's: 1 center, 2 right,
	// 3 justify, 4 left; zero leaves the widget's own.
	TextAlign int
	// Lines ellipsizes a label at that many lines.
	Lines int
	// Wrap is a label's: 1 one line, 2 wrapped.
	Wrap   int
	Hidden bool
	Cursor string
	// Justify is a box's justifyContent: 1 center, 2 end, 3 space between,
	// 4 around, 5 evenly; zero packs its children from the start.
	Justify int
	// Spacing is a box's, where Spaced says the look holds one.
	Spaced  bool
	Spacing int
	// Grid lays a box out as sngl:ui's grid, Columns to a row, or flowing
	// where Columns is zero.
	Grid    bool
	Columns int
}

// SetLook gives w what l asks for, and back what an earlier Look took that l
// leaves unset.
func SetLook(w Handle, l Look) {
	css, freeCSS := cstr(l.Css)
	defer freeCSS()
	cursor, freeCursor := cstr(l.Cursor)
	defer freeCursor()
	C.sngl_set_look(widget(w), css, C.int(math.Round(l.Width)), C.int(math.Round(l.Height)), C.double(l.Grow),
		C.int(l.Cross), C.int(l.TextAlign), C.int(l.Lines), C.int(l.Wrap), C.int(bool01(l.Hidden)), cursor,
		C.int(l.Justify), C.int(bool01(l.Spaced)), C.int(l.Spacing), C.int(bool01(l.Grid)), C.int(l.Columns))
}

// ContentsNew is the box an instance of a component holds its widgets in
// (sngl_contents_new).
func ContentsNew() Handle {
	return Handle(unsafe.Pointer(C.sngl_contents_new()))
}

func bool01(b bool) int {
	if b {
		return 1
	}
	return 0
}

// The probes below are look_test.go's: a _test.go file cannot use cgo.

func expands(w Handle) (h, v bool) {
	return C.gtk_widget_get_hexpand(widget(w)) != 0, C.gtk_widget_get_vexpand(widget(w)) != 0
}

func aligns(w Handle) (h, v C.GtkAlign) {
	return C.gtk_widget_get_halign(widget(w)), C.gtk_widget_get_valign(widget(w))
}

func labelXAlign(w Handle) float64 {
	return float64(C.gtk_label_get_xalign((*C.GtkLabel)(p(w))))
}

func shown(w Handle) bool { return C.gtk_widget_get_visible(widget(w)) != 0 }

func setShown(w Handle, v bool) { C.gtk_widget_set_visible(widget(w), gbool(v)) }

func setHAlign(w Handle, a C.GtkAlign) { C.gtk_widget_set_halign(widget(w), a) }

func computesVExpand(w Handle) bool {
	return C.gtk_widget_compute_expand(widget(w), C.GTK_ORIENTATION_VERTICAL) != 0
}

func computesHExpand(w Handle) bool {
	return C.gtk_widget_compute_expand(widget(w), C.GTK_ORIENTATION_HORIZONTAL) != 0
}

func firstChild(w Handle) Handle {
	return Handle(unsafe.Pointer(C.gtk_widget_get_first_child(widget(w))))
}

func setVExpand(w Handle, v bool) { C.gtk_widget_set_vexpand(widget(w), gbool(v)) }

func setHExpand(w Handle, v bool) { C.gtk_widget_set_hexpand(widget(w), gbool(v)) }

func minWidth(w Handle) int {
	var min, nat C.int
	C.gtk_widget_measure(widget(w), C.GTK_ORIENTATION_HORIZONTAL, -1, &min, &nat, nil, nil)
	return int(min)
}

func naturalWidth(w Handle) int {
	var min, nat C.int
	C.gtk_widget_measure(widget(w), C.GTK_ORIENTATION_HORIZONTAL, -1, &min, &nat, nil, nil)
	return int(nat)
}

func allocate(w Handle, width, height int) {
	var min, nat C.int
	C.gtk_widget_measure(widget(w), C.GTK_ORIENTATION_HORIZONTAL, -1, &min, &nat, nil, nil)
	C.gtk_widget_measure(widget(w), C.GTK_ORIENTATION_VERTICAL, C.int(width), &min, &nat, nil, nil)
	a := C.GtkAllocation{width: C.int(width), height: C.int(height)}
	C.gtk_widget_size_allocate(widget(w), &a, -1)
}

func allocation(w Handle) C.GtkAllocation {
	return C.sngl_allocation(widget(w))
}

func allocatedX(w Handle) int { return int(allocation(w).x) }

func allocatedWidth(w Handle) int { return int(allocation(w).width) }

func allocatedHeight(w Handle) int { return int(allocation(w).height) }

func boxLaidOutByGTK(w Handle) bool {
	return C.sngl_is_box_layout(widget(w)) != 0
}

func requestMode(w Handle) int { return int(C.gtk_widget_get_request_mode(widget(w))) }

func widthForHeight() Handle { return Handle(unsafe.Pointer(C.sngl_probe_width_for_height())) }

// State flags painted takes, as GtkStateFlags has them.
const (
	stateActive  = 1 << 0
	stateHover   = 1 << 1
	stateChecked = 1 << 11
)

// painted is what GTK paints for w in state, a row at a time, each pixel
// 0xAARRGGBB.
func painted(w Handle, state uint32) [][]uint32 {
	var width, height C.int
	px := C.sngl_probe_paint(widget(w), C.GtkStateFlags(state), &width, &height)
	if px == nil {
		return nil
	}
	defer C.g_free(C.gpointer(px))
	flat := unsafe.Slice((*uint32)(unsafe.Pointer(px)), int(width)*int(height))
	rows := make([][]uint32, height)
	for y := range rows {
		rows[y] = append([]uint32(nil), flat[y*int(width):(y+1)*int(width)]...)
	}
	return rows
}
