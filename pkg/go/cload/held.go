package cload

import (
	"sync"

	"github.com/ebitengine/purego"

	"duckfam.us/sngl/pkg/go/cbind"
)

// heldBase is where the pointers Hold hands out start: past NULL, so that
// none reads as nothing, and past the low page no C library takes for an
// object.
const heldBase = 1 << 16

// Hold registers v, a value C holds (sngl:x/c's held), and is the pointer C
// is handed for it: cbind's index, offset by heldBase. It is no address, and
// C does nothing with it but hand it back, to Held or to the function
// Release answers.
func Hold(v any) uintptr { return uintptr(cbind.Register(v)) + heldBase }

// Held is the value Hold handed C p for, nil for NULL or for one let go.
func Held(p uintptr) any {
	if p < heldBase {
		return nil
	}
	return cbind.Lookup(int(p - heldBase))
}

// release is the C function letting a held value go, made once: purego
// gives no callback back, so a program makes one and hands it every time.
var release = sync.OnceValue(func() uintptr {
	return purego.NewCallback(func(p uintptr) {
		if p >= heldBase {
			cbind.Release(int(p - heldBase))
		}
	})
})

// Release is the C function a held value's #[c.release] parameter is
// handed, which C calls with the pointer Hold answered once it lets the
// value go: GLib's GDestroyNotify.
func Release() uintptr { return release() }
