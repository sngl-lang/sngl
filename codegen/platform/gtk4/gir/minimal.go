package gir

import (
	_ "embed"
	"sync"
)

// minimalGIR is introspection data for the widgets lib/platforms/gtk4 wraps,
// and nothing else. It is here so the platform has a widget vocabulary without
// GTK 4 development files on the host: the declarations type-check, `sngl doc`
// renders them, the LSP resolves them, and a test asserting what a setter is
// named asserts against a file in this repository rather than against whichever
// GTK the machine happens to carry.
//
// It is not a substitute for the real thing. A program naming a widget outside
// the set below needs the installed Gtk-4.0.gir, which is what the probe finds
// when it is there and what MinimalReason says is missing when it is not.
//
//go:embed minimal/Gtk-4.0.gir
var minimalGIR []byte

var (
	minimalOnce sync.Once
	minimalReg  *TypeRegistry
	minimalErr  error
)

// Minimal parses the bundled introspection data, memoized. The result is
// shared, so callers must treat it as read-only.
func Minimal() (*TypeRegistry, error) {
	minimalOnce.Do(func() {
		minimalReg, minimalErr = ParseGIRBytes(minimalGIR)
	})
	return minimalReg, minimalErr
}
