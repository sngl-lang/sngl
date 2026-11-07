package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func rows(n int) string {
	var lines []string
	for i := range n {
		lines = append(lines, fmt.Sprintf("row %02d", i))
	}
	return strings.Join(lines, "\n")
}

// draw is one view of a scroll keyed "s" of rows(n), 4 lines tall, with
// nothing focused.
func draw(s *Scrolls, n int) string {
	s.Reset()
	v := s.Scan(s.View("s", rows(n), 0, 4, ScrollVertical, Box{}, false, true), 0, 0)
	s.Settle()
	return v
}

// A scroll shows its viewport's worth of its content, and the wheel over it
// moves it, never past either end.
func TestAScrollCutsItsContentAndTheWheelMovesIt(t *testing.T) {
	s := NewScrolls()
	if v := draw(s, 20); v != rows(4) {
		t.Fatalf("a scroll at its top drew:\n%s", v)
	}
	if !s.Wheel(tea.MouseWheelMsg{X: 1, Y: 1, Button: tea.MouseWheelDown}) {
		t.Fatal("the wheel over the scroll moved nothing")
	}
	if v := draw(s, 20); !strings.HasPrefix(v, "row 03") {
		t.Fatalf("a notch down drew:\n%s", v)
	}
	if s.Wheel(tea.MouseWheelMsg{X: 1, Y: 9, Button: tea.MouseWheelDown}) {
		t.Fatal("the wheel below the scroll moved it")
	}
	for range 10 {
		s.Wheel(tea.MouseWheelMsg{X: 1, Y: 1, Button: tea.MouseWheelDown})
	}
	if v := draw(s, 20); !strings.HasSuffix(v, "row 19") || !strings.HasPrefix(v, "row 16") {
		t.Fatalf("a scroll wheeled past its end drew:\n%s", v)
	}
}

// The end in view is reported once at each content height, so a page more
// is asked for, and again once it has arrived and the end still shows.
func TestAScrollReportsItsEndOncePerSize(t *testing.T) {
	s := NewScrolls()
	draw(s, 20)
	if s.Waiting() || s.Due("s") {
		t.Fatal("a scroll at its top reported its end")
	}
	for range 10 {
		s.Wheel(tea.MouseWheelMsg{X: 1, Y: 1, Button: tea.MouseWheelDown})
	}
	draw(s, 20)
	if !s.Waiting() || !s.Due("s") {
		t.Fatal("a scroll at its end did not report it")
	}
	if s.Waiting() || s.Due("s") {
		t.Fatal("the end was reported twice at one size")
	}
	draw(s, 22)
	if !s.Due("s") {
		t.Fatal("the end still in view after the content grew was not reported")
	}
	// Content shorter than the viewport shows its end on arrival.
	short := NewScrolls()
	draw(short, 2)
	if !short.Due("s") {
		t.Fatal("content too short to scroll did not report its end")
	}
}

// A key that moves focus brings the stop it moved to into view.
func TestAScrollFollowsFocus(t *testing.T) {
	s := NewScrolls()
	draw(s, 20)
	s.Follow()
	s.Reset()
	v := s.Scan(s.View("s", rows(20), 0, 4, ScrollVertical, Box{0, 10, 5, 10}, true, false), 0, 0)
	s.Settle()
	if !strings.HasSuffix(v, "row 10") {
		t.Fatalf("a scroll following a stop on row 10 drew:\n%s", v)
	}
	if !s.Key(tea.KeyPressMsg{Code: tea.KeyPgUp}) {
		t.Fatal("PgUp in the scroll focus is in moved nothing")
	}
}
