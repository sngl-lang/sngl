package fynert

import (
	"sync"
	"testing"
	"time"
)

// Every hands the schedule back, so nothing here keeps a registry: a cancelled
// schedule is garbage once its caller drops it. There is no table left to count
// entries in, so what is pinned is the behaviour the removal had to preserve.
func TestEveryTicksUntilCancelled(t *testing.T) {
	orig := fyneDo
	fyneDo = func(f func()) { f() }
	t.Cleanup(func() { fyneDo = orig })

	var mu sync.Mutex
	var n int
	s := Every(5, func() { mu.Lock(); n++; mu.Unlock() })
	if s == nil {
		t.Fatal("Every armed nothing")
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		got := n
		mu.Unlock()
		if got > 0 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	mu.Lock()
	ticked := n
	mu.Unlock()
	if ticked == 0 {
		t.Fatal("the schedule never fired")
	}

	s.Cancel()
	time.Sleep(50 * time.Millisecond)
	mu.Lock()
	stopped := n
	mu.Unlock()
	time.Sleep(100 * time.Millisecond)
	mu.Lock()
	after := n
	mu.Unlock()
	if after != stopped {
		t.Errorf("a cancelled schedule kept firing: %d then %d", stopped, after)
	}
}

// An unmount may run with nothing armed -- a non-positive period arms nothing,
// and the SNGL var holding the schedule starts nil -- and a teardown after an
// unmount cancels a second time.
func TestCancelIsSafeTwiceAndOnNil(t *testing.T) {
	var s *Schedule
	s.Cancel()

	if got := Every(0, func() {}); got != nil {
		t.Fatal("a non-positive period armed a schedule")
	}

	orig := fyneDo
	fyneDo = func(f func()) { f() }
	t.Cleanup(func() { fyneDo = orig })
	live := Every(5, func() {})
	live.Cancel()
	live.Cancel() // must not panic on a second close
}
