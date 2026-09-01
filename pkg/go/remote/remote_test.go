package remote

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// settled waits for a box to stop being in flight, so a test asserts on an
// answer rather than on a race.
func settled[T any](t *testing.T, v *Value[T]) *Value[T] {
	t.Helper()
	for range 500 {
		if !v.InFlight() {
			return v
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("box never settled")
	return nil
}

// The zero box answers every accessor. That is what makes a Value total: a read
// landing before the first fetch is not a special case, which is why value is an
// option rather than a bare T.
func TestZeroBoxAnswersEverything(t *testing.T) {
	var v Value[int]
	if v.Get() != nil || v.Err() != nil || v.InFlight() {
		t.Error("the zero box is not idle")
	}
	if got := v.Or(7); got != 7 {
		t.Errorf("Or on an idle box = %d; want the fallback", got)
	}
	// And a nil box too: a reader holding one that was never built still reads.
	var nilBox *Value[int]
	if nilBox.Or(7) != 7 || nilBox.InFlight() {
		t.Error("a nil box faulted")
	}
}

// Reading a key with no attempt behind it starts one. This is the whole model:
// nothing is kicked by a dependency changing, because a key is routinely a loop
// variable with no setter to hang a kicker on.
func TestReadStartsTheFetch(t *testing.T) {
	s := New()
	var calls atomic.Int32
	box := Query(s, "q", []any{1}, func() (int, error) {
		calls.Add(1)
		return 42, nil
	})
	settled(t, box)
	if got := box.Or(0); got != 42 {
		t.Errorf("value = %d; want 42", got)
	}
	if n := calls.Load(); n != 1 {
		t.Errorf("fetch ran %d times; want once", n)
	}
}

// One box per key, and reading it again does not re-fetch. Idempotence is what
// makes the call safe in a prop expression evaluated on every render.
func TestSameKeyIsOneBoxAndOneFetch(t *testing.T) {
	s := New()
	var calls atomic.Int32
	fetch := func() (int, error) { calls.Add(1); return 1, nil }

	a := Query(s, "q", []any{1}, fetch)
	settled(t, a)
	b := Query(s, "q", []any{1}, fetch)
	if a != b {
		t.Error("the same key gave two boxes; nothing would be shared")
	}
	if n := calls.Load(); n != 1 {
		t.Errorf("re-reading a settled box re-fetched (%d calls); a render would fetch forever", n)
	}

	c := Query(s, "q", []any{2}, fetch)
	if c == a {
		t.Error("a different key shared a box")
	}
}

// Concurrent readers of one key get one fetch. The dedup is the box's, so it
// holds however many goroutines ask at once.
func TestConcurrentReadersShareOneFetch(t *testing.T) {
	s := New()
	var calls atomic.Int32
	release := make(chan struct{})
	fetch := func() (int, error) {
		calls.Add(1)
		<-release
		return 1, nil
	}

	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() { Query(s, "q", []any{1}, fetch) })
	}
	wg.Wait()
	close(release)
	settled(t, Query(s, "q", []any{1}, fetch))

	if n := calls.Load(); n != 1 {
		t.Errorf("20 readers caused %d fetches; want 1", n)
	}
}

// A failure records itself and leaves any value alone. The combination — a value
// beside a failure — is the case the three facts exist to express, and it is what
// lets a screen render stale data with a warning instead of blanking.
func TestFailureKeepsThePreviousValue(t *testing.T) {
	s := New()
	var fail atomic.Bool
	box := Query(s, "q", []any{1}, func() (int, error) {
		if fail.Load() {
			return 0, Failure{Kind: Server, Message: "down", Code: "503"}
		}
		return 9, nil
	})
	settled(t, box)
	if box.Or(0) != 9 {
		t.Fatalf("first fetch = %d; want 9", box.Or(0))
	}

	fail.Store(true)
	box.Refresh()
	settled(t, box)

	if got := box.Or(0); got != 9 {
		t.Errorf("value after a failed refresh = %d; want the previous 9 kept", got)
	}
	f := box.Err()
	if f == nil {
		t.Fatal("a failed refresh recorded no failure")
	}
	if f.Kind != Server || f.Code != "503" {
		t.Errorf("failure = %+v; want the adapter's own kind and code", *f)
	}
}

// A plain error is a transport failure — the honest default for one carrying no
// other information. An adapter that knows better returns a Failure.
func TestPlainErrorBecomesATransportFailure(t *testing.T) {
	s := New()
	box := Query(s, "q", []any{1}, func() (int, error) { return 0, errors.New("boom") })
	settled(t, box)
	f := box.Err()
	if f == nil || f.Kind != Transport || f.Message != "boom" {
		t.Errorf("failure = %+v; want a transport failure carrying the message", f)
	}
}

// A success after a failure clears it. Otherwise a screen would warn forever
// about an attempt that has since been superseded.
func TestSuccessClearsTheFailure(t *testing.T) {
	s := New()
	var fail atomic.Bool
	fail.Store(true)
	box := Query(s, "q", []any{1}, func() (int, error) {
		if fail.Load() {
			return 0, errors.New("boom")
		}
		return 5, nil
	})
	settled(t, box)
	if box.Err() == nil {
		t.Fatal("no failure recorded")
	}
	fail.Store(false)
	box.Refresh()
	settled(t, box)
	if box.Err() != nil {
		t.Error("a success left the previous failure in place")
	}
	if box.Or(0) != 5 {
		t.Error("the new value did not land")
	}
}

// The store tells the platform when to look again. Read-triggered fetching means
// the render that started the fetch is long over by the time an answer arrives.
func TestSettleNotifies(t *testing.T) {
	s := New()
	var notified atomic.Int32
	s.OnSettle(func() { notified.Add(1) })
	settled(t, Query(s, "q", []any{1}, func() (int, error) { return 1, nil }))
	// The notify runs after settle, so allow it a moment to be observed.
	for i := 0; i < 200 && notified.Load() == 0; i++ {
		time.Sleep(time.Millisecond)
	}
	if notified.Load() == 0 {
		t.Error("a settled fetch notified nobody; the screen would never update")
	}
}

// And again for each attempt after the first. A refresh button is the case
// where nobody is watching for any other reason: the click renders `inFlight`
// true, and without a second notification that is the last thing the screen
// ever draws.
func TestRefreshNotifies(t *testing.T) {
	s := New()
	box := settled(t, Query(s, "q", []any{1}, func() (int, error) { return 1, nil }))

	var notified atomic.Int32
	s.OnSettle(func() { notified.Add(1) })
	box.Refresh()
	settled(t, box)
	for i := 0; i < 200 && notified.Load() == 0; i++ {
		time.Sleep(time.Millisecond)
	}
	if notified.Load() == 0 {
		t.Error("a refreshed fetch notified nobody; the screen would sit on `loading` forever")
	}
}

// Keys are built by walking values, so two calls with equal arguments are one
// key. Length prefixing is what keeps ("a","b") and ("a|b") apart.
func TestKeyDistinguishesArgumentBoundaries(t *testing.T) {
	if key("q", []any{"a", "b"}) == key("q", []any{"a|b"}) {
		t.Error(`("a","b") and ("a|b") share a key`)
	}
	if key("q", []any{1}) != key("q", []any{1}) {
		t.Error("equal arguments gave different keys")
	}
	if key("q", []any{1}) == key("other", []any{1}) {
		t.Error("two queries share a key space")
	}
	// A map's iteration order must not reach the key.
	m1 := map[string]any{"a": 1, "b": 2}
	m2 := map[string]any{"b": 2, "a": 1}
	if key("q", []any{m1}) != key("q", []any{m2}) {
		t.Error("map ordering leaked into the key")
	}
}

// The three constructors build the three states directly, for an adapter that
// already holds its answer and for a test rendering one branch.
func TestConstructors(t *testing.T) {
	if got := Of(3).Or(0); got != 3 {
		t.Errorf("Of(3).Or(0) = %d; want 3", got)
	}
	f := FailedWith[int](Failure{Kind: Auth})
	if f.Err() == nil || f.Err().Kind != Auth || f.Get() != nil {
		t.Error("FailedWith is not a failure holding no value")
	}
	if p := Pending[int](); !p.InFlight() || p.Get() != nil {
		t.Error("Pending is not an attempt with nothing to show")
	}
}
