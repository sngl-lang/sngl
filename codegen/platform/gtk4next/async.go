package gtk4next

import (
	"duckfam.us/sngl/codegen"
	"duckfam.us/sngl/ir"
)

// A Go test waiting for what the loop drives runs the loop while it waits,
// since the test runs on the loop's own thread, as C's does.
func init() {
	codegen.RegisterPlatformIntrinsic(platformName, ir.TestWaitID, func(args []ir.Expr, tr func(ir.Expr) string) (string, []string) {
		if len(args) != 3 {
			return "", nil
		}
		return "for __snglWait, __snglEnd := " + tr(args[1]) + ", time.Now().Add(time.Duration(" + tr(args[2]) + ")*time.Millisecond); !__snglWait() && time.Now().Before(__snglEnd); { cgo.SnglGtkStep() }", []string{wrappers, "time"}
	})
}
