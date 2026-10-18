package gtk4

import (
	"duckfam.us/sngl/codegen"
	"duckfam.us/sngl/internal/lower"
	"duckfam.us/sngl/ir"
)

// GTK owns its widgets from the thread running the main loop, and a GLib idle
// source is the supported way in from another. gtk4rt.Post wraps
// g_idle_add_full; it had no caller until this.
func init() {
	codegen.RegisterPlatformIntrinsic("gtk4", lower.AsyncDoIntrinsic, func(args []ir.Expr, tr func(ir.Expr) string) (string, []string) {
		if len(args) != 1 {
			return "", nil
		}
		return "gtk4rt.Post(" + tr(args[0]) + ")", []string{gtk4rtPkg}
	})
	// A test waiting for what the loop drives -- a request's answer posted
	// back through Post -- runs the loop while it waits, since the test runs
	// on the loop's own thread and a sleep there would starve it.
	codegen.RegisterPlatformIntrinsic("gtk4", ir.TestWaitID, func(args []ir.Expr, tr func(ir.Expr) string) (string, []string) {
		if len(args) != 3 {
			return "", nil
		}
		return "for __snglWait, __snglEnd := " + tr(args[1]) + ", time.Now().Add(time.Duration(" + tr(args[2]) + ")*time.Millisecond); !__snglWait() && time.Now().Before(__snglEnd); { gtk4rt.PumpFor(5) }", []string{gtk4rtPkg, "time"}
	})
}
