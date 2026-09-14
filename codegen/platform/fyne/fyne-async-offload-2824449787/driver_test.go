package ui

import (
	"os"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
)

func TestTheClickReturnsWhileTheWorkRuns(t *testing.T) {
	test.NewApp()
	m := New()
	m.BuildUI()

	start := time.Now()
	m.load.OnTapped()
	elapsed := time.Since(start)

	if elapsed > 100*time.Millisecond {
		t.Fatalf("the click took %v; the blocking call ran on the thread that draws", elapsed)
	}
	if !m.busy {
		t.Errorf("busy = false right after the click; the statement before the blocking call did not run where it was written")
	}
	if m.greeting != "idle" {
		t.Errorf("greeting = %q right after the click; the answer was written before it could have arrived", m.greeting)
	}

	// Now let it land. fyne.Do queues onto the driver's goroutine, so the
	// posted closure runs only once something pumps it.
	want, _ := os.Hostname()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		done := make(chan bool, 1)
		fyne.Do(func() { done <- m.greeting == want && !m.busy })
		if <-done {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("the answer never arrived: greeting = %q busy = %v, want %q false", m.greeting, m.busy, want)
}
