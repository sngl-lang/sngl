//go:build !js

package gtk4rt

/*
#cgo pkg-config: gtk4
#cgo CFLAGS: -I${SRCDIR}/../../../lib/platform/gtk4/rt

// A calendar's day and a select's drop-down are C shipped with the platform,
// which a C program compiles too.
#include "choose.c"
*/
import "C"
