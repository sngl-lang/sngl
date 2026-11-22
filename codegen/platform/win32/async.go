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
		return "snglPost(" + tr(args[0]) + ")", nil
	})
	// A Go test waiting for what the loop drives runs the loop while it
	// waits, since the test runs on the loop's own thread, as C's does.
	codegen.RegisterPlatformLangIntrinsic(platformName, "go", ir.TestWaitID, func(args []ir.Expr, tr func(ir.Expr) string) (string, []string) {
		if len(args) != 3 {
			return "", nil
		}
		return "for __snglWait, __snglEnd := " + tr(args[1]) + ", time.Now().Add(time.Duration(" + tr(args[2]) + ")*time.Millisecond); !__snglWait() && time.Now().Before(__snglEnd); { snglWin32Step() }", []string{"time"}
	})
}
