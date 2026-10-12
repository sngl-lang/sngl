package tui

import (
	"testing"
	"time"
)

func TestDateInputRoundTrip(t *testing.T) {
	d := NewDateInput()
	if d.Date() != nil {
		t.Fatal("an empty field holds no date")
	}
	day := time.Date(2024, 3, 5, 0, 0, 0, 0, time.UTC)
	d.SetDate(&day)
	if got := d.Value(); got != "2024-03-05" {
		t.Fatalf("SetDate showed %q", got)
	}
	if got := d.Date(); got == nil || !got.Equal(day) {
		t.Fatalf("Date read %v", got)
	}

	// Half a date reads as none, and writing that none back keeps the text.
	d.SetValue("2024-0")
	if d.Date() != nil {
		t.Fatal("half a date is no date")
	}
	d.SetDate(nil)
	if got := d.Value(); got != "2024-0" {
		t.Fatalf("writing back none cleared the typing: %q", got)
	}

	// A date that cannot be is none too.
	d.SetValue("2023-02-29")
	if d.Date() != nil {
		t.Fatal("February 29 2023 is no date")
	}
}
