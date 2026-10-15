package fynelayout_test

import (
	"testing"

	"fyne.io/fyne/v2"
	fynetest "fyne.io/fyne/v2/test"

	"duckfam.us/sngl/pkg/go/fynelayout"
)

func TestTapReachesAnObjectThatIsNotTappable(t *testing.T) {
	tap := fynelayout.NewTap(box(40, 40))
	n := 0
	tap.OnTapped = func() { n++ }
	var _ fyne.Tappable = tap
	fynetest.Tap(tap)
	if n != 1 {
		t.Fatalf("tapped %d times, want 1", n)
	}
	if got := tap.MinSize(); got != fyne.NewSize(40, 40) {
		t.Errorf("MinSize %v, want the content's 40x40", got)
	}
}
