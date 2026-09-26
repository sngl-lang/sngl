//go:build !js

package gtk4rt

import (
	"slices"
	"testing"
)

// A render slot re-renders where it was written: its entries go before its
// anchor, not after every sibling appended below it.
func TestSlotAnchorKeepsASlotInPlace(t *testing.T) {
	if !hasDisplay() {
		t.Skip("gtk4 needs an X11/Wayland display")
	}
	Init()

	box := BoxNew(OrientationVertical, 6)
	head, tail := LabelNew("head"), LabelNew("tail")
	BoxAppend(box, head)
	var at Handle
	at = SlotAnchor(box, at)
	BoxAppend(box, tail)

	a := LabelNew("A")
	Retain(a)
	for range 3 {
		if slices.Contains(children(box), a) {
			BoxRemove(box, a)
		}
		at = SlotAnchor(box, at)
		InsertBefore(box, at, a)
	}
	if got, want := children(box), []Handle{head, a, at, tail}; !slices.Equal(got, want) {
		t.Fatalf("children = %v, want head, A, anchor, tail = %v", got, want)
	}
}
