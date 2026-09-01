// Package remote is the Go runtime behind SNGL's sngl:remote package: the box
// a query answers with, and the store the boxes live in.
//
// A Value holds three independent facts — the last value that arrived, how the
// most recent attempt failed, and whether an attempt is in flight. They are
// independent because the interesting states are the combinations: a stale
// value beside a failure is a refresh that did not land, and the honest thing
// to render is the old value with a warning. That is also why Refresh keeps the
// value it has until new data replaces it.
//
// The zero Value is the idle box — no value, no failure, nothing in flight — so
// every accessor answers on a box nothing has fetched and no reader can fault.
package remote

import "sync"

// FailureKind classifies a failure by what the program should do next, which is
// the only question a UI asks of one. Request and Server are separate for that
// reason and no other: retrying a Request fails identically, retrying a Server
// may work.
type FailureKind int

const (
	Transport FailureKind = iota // never reached the peer
	Timeout                      // reached it, no answer in time
	Auth                         // a credential is missing, expired or rejected
	Request                      // the peer rejected what was sent; retrying is pointless
	Server                       // the peer failed on its own side; retrying may work
	Decode                       // the answer arrived but did not match the declared type
	Cancelled                    // superseded by a newer attempt, or the owner went away
)

func (k FailureKind) String() string {
	switch k {
	case Transport:
		return "transport"
	case Timeout:
		return "timeout"
	case Auth:
		return "auth"
	case Request:
		return "request"
	case Server:
		return "server"
	case Decode:
		return "decode"
	case Cancelled:
		return "cancelled"
	}
	return "unknown"
}

// Failure is why the most recent attempt failed. Code is the adapter's own —
// an HTTP status, a SQLSTATE, a gRPC status name — carried verbatim because the
// only code that can interpret it is code that knows which adapter produced it.
type Failure struct {
	Kind    FailureKind
	Message string
	Code    string
}

func (f Failure) Error() string {
	if f.Code == "" {
		return f.Kind.String() + ": " + f.Message
	}
	return f.Kind.String() + " (" + f.Code + "): " + f.Message
}

// Value is the three-state box. Its zero value is the idle box, which is what
// makes every accessor total.
//
// The mutex is the box's own because a settle races the render that reads it:
// the fetch completes on whatever goroutine ran it, while the UI thread is
// asking whether there is a value yet.
type Value[T any] struct {
	mu       sync.Mutex
	value    *T
	failure  *Failure
	inFlight bool

	// refresh re-runs the fetch behind this box. Nil for a box built by Of,
	// FailedWith or Pending, which have no fetch to re-run.
	refresh func()
}

// Of is a settled box: the value arrived, nothing failed, nothing is in flight.
// The identity query — what a fetch that already has its answer returns.
func Of[T any](v T) *Value[T] { return &Value[T]{value: &v} }

// FailedWith is a box whose most recent attempt failed, holding no value.
func FailedWith[T any](f Failure) *Value[T] { return &Value[T]{failure: &f} }

// Pending is a box with an attempt in flight and nothing to show yet.
func Pending[T any]() *Value[T] { return &Value[T]{inFlight: true} }

// Get is the last value that arrived, or nil if none ever has. A failure does
// not clear it: a refresh that fails leaves the previous value in place, which
// is what lets a screen keep rendering while it warns.
//
// Named Get rather than Value because the type is already called Value.
func (v *Value[T]) Get() *T {
	if v == nil {
		return nil
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.value
}

// Err is how the most recent attempt failed, or nil if it succeeded.
func (v *Value[T]) Err() *Failure {
	if v == nil {
		return nil
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.failure
}

// InFlight reports whether an attempt is running. Orthogonal to the other two:
// a refresh runs over a value that is already there, and over a failure still
// worth showing.
func (v *Value[T]) InFlight() bool {
	if v == nil {
		return false
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.inFlight
}

// Or is the value if one has arrived, otherwise fallback.
func (v *Value[T]) Or(fallback T) T {
	if got := v.Get(); got != nil {
		return *got
	}
	return fallback
}

// Refresh re-runs the fetch behind this box, bypassing any cached answer.
//
// It invalidates freshness rather than the box: the value stays and InFlight
// goes true, and new data replaces the old only once it arrives. Calling it
// while an attempt is in flight is not an error and starts no second attempt.
func (v *Value[T]) Refresh() {
	if v == nil {
		return
	}
	v.mu.Lock()
	run := v.refresh
	busy := v.inFlight
	v.mu.Unlock()
	if run != nil && !busy {
		run()
	}
}

// begin marks an attempt started, and reports whether this caller started it.
// A second caller finding one already in flight is the dedup: one fetch per key,
// however many readers ask.
func (v *Value[T]) begin() bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.inFlight {
		return false
	}
	v.inFlight = true
	return true
}

// settle lands an answer. A value replaces whatever was there and clears the
// failure; a failure is recorded and leaves the value alone.
func (v *Value[T]) settle(got *T, f *Failure) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.inFlight = false
	if f != nil {
		v.failure = f
		return
	}
	v.value = got
	v.failure = nil
}
