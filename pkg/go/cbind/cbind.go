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
	slots []func()
)

// Register stores fn and returns its dispatch index. Indices are stable and
// never reused, so a C callback wired to an index keeps firing the same fn for
// the lifetime of the process.
func Register(fn func()) int {
	mu.Lock()
	defer mu.Unlock()
	slots = append(slots, fn)
	return len(slots) - 1
}

// Dispatch invokes the fn registered at idx. Out-of-range indices are ignored
// (a stray callback after teardown is a no-op rather than a panic). The fn runs
// without cbind's lock held, so a handler may itself Register more callbacks.
func Dispatch(idx int) {
	mu.Lock()
	var fn func()
	if idx >= 0 && idx < len(slots) {
		fn = slots[idx]
	}
	mu.Unlock()
	if fn != nil {
		fn()
	}
}
