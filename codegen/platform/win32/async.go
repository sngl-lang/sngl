package win32

import (
	"duckfam.us/sngl/codegen"
	"duckfam.us/sngl/internal/lower"
	"duckfam.us/sngl/ir"
)

// A Go program's `async.do` posts its closure to the message loop through
// the runtime's sngl_post, as C's does itself: Win32 windows are their
// thread's, and the goroutine the work ran on is another.
func init() {
	codegen.RegisterPlatformIntrinsic(platformName, lower.AsyncDoIntrinsic, func(args []ir.Expr, tr func(ir.Expr) string) (string, []string) {
		if len(args) != 1 {
			return "", nil
		}
		return "cgo.SnglPost(" + tr(args[0]) + ")", []string{wrappers}
	})
}
