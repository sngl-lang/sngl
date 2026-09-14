package ui

import (
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
)

func texts(o fyne.CanvasObject, out *[]string) {
	switch w := o.(type) {
	case *widget.Label:
		*out = append(*out, w.Text)
	case *widget.Button:
		*out = append(*out, w.Text)
	case *fyne.Container:
		for _, c := range w.Objects {
			texts(c, out)
		}
	}
}

func TestTheSlotSubtreeIsReachableFromTheInstanceRoot(t *testing.T) {
	test.NewApp()
	m := New()
	m.BuildUI()

	if got := len(m.__inst0_live); got != 2 {
		t.Fatalf("expected 2 live instances, got %d", got)
	}
	var got []string
	texts(m.__inst0_live[0].Root, &got)
	want := []string{"a", "body of a"}
	if len(got) != len(want) {
		t.Fatalf("instance tree = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("instance tree = %v, want %v", got, want)
		}
	}
}
