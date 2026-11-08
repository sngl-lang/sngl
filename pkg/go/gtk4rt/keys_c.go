//go:build !js

package gtk4rt

/*
#cgo pkg-config: gtk4
#cgo CFLAGS: -I${SRCDIR}/../../../lib/platform/gtk4/rt

// A window's keys are C shipped with the platform, which a C program
// compiles too: what each key is called, the controllers that hear them,
// and where a key a test presses goes on to. Included here rather than in
// keys.go, whose //export has cgo compile its preamble twice.
#include "keys.c"
*/
import "C"
