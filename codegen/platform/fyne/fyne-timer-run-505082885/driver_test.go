package ui

import (
	"testing"
	"time"

	"fyne.io/fyne/v2/test"
)

// A ticker runs on a goroutine and marshals its body through fyne.Do, so the
// driver needs a real Fyne app for that to be dispatched at all.
func TestTimersTick(t *testing.T) {
	test.NewApp()
	m := New()
	m.BuildUI()
	m.StartTimers()
	defer m.StopTimers()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if m.Seconds() > 0 && m.Beats__inst0() > 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	if m.Seconds() == 0 {
		t.Error("the root component's timer never fired")
	}
	if m.Beats__inst0() == 0 {
		t.Error("the child component's timer never fired")
	}
}
