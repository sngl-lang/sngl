// Package cbind provides library-agnostic machinery for Go wrappers around C
// libraries. It offers an opaque native Handle and a registry that lets C
// callbacks dispatch back into Go closures by integer index.
//
// A per-library cgo package (e.g. pkg/go/gtk4rt) builds its typed bindings on
// top of cbind, so the callback plumbing and handle representation are written
// once and reused across libraries. cbind itself contains no cgo: the C side
// lives in the per-library package, which forwards its //export'd trampolines
// to Dispatch here.
package cbind

import (
	"sync"
	"unsafe"
)

// Handle is an opaque pointer to a native (C) object. Its underlying type is
// unsafe.Pointer, so the zero value is nil and handles compare against nil.
// Callers outside a binding package treat it as opaque; the owning cgo wrapper
// converts it to/from a concrete C pointer via the ordinary unsafe.Pointer
// conversions (Go forbids methods on a pointer-underlying type, so there are no
// accessors here).
type Handle unsafe.Pointer

var (
	mu    sync.Mutex
	next  int
	slots = map[int]func(){}
)

// Register stores fn and returns its dispatch index. Indices are stable and
// never reused, so a C callback wired to an index keeps firing the same fn
// until that index is Released -- and a stray one arriving after a Release
// finds nothing rather than something else's closure.
//
// A map rather than a slice because Release has to be able to drop an entry:
// an index is handed to C and may not be reused, so a freed slot cannot be
// filled and a slice would grow by one dead element per registration. That is
// what a caller registering per callback rather than per program needs --
// gtk4rt.Post does it once per idle tick.
func Register(fn func()) int {
	mu.Lock()
	defer mu.Unlock()
	idx := next
	next++
	slots[idx] = fn
	return idx
}

// Release drops the fn at idx, so whatever it closed over can be collected.
// The index is not reused. Releasing one twice, or one that was never
// registered, is a no-op.
//
// Registering without releasing retains the closure for the life of the
// process, and through it everything the closure captured -- a generated
// program's whole Model, in the case a timer's tick is.
func Release(idx int) {
	mu.Lock()
	delete(slots, idx)
	mu.Unlock()
}

// Dispatch invokes the fn registered at idx. An unknown index is ignored (a
// stray callback after teardown is a no-op rather than a panic). The fn runs
// without cbind's lock held, so a handler may itself Register more callbacks.
func Dispatch(idx int) {
	mu.Lock()
	fn := slots[idx]
	mu.Unlock()
	if fn != nil {
		fn()
	}
}

// DispatchOnce is Dispatch for a callback C will not call again: the fn runs
// and its slot is dropped. A one-shot source that registered per call would
// otherwise retain every closure it ever scheduled.
func DispatchOnce(idx int) {
	mu.Lock()
	fn := slots[idx]
	delete(slots, idx)
	mu.Unlock()
	if fn != nil {
		fn()
	}
}
