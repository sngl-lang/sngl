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
	codegen.RegisterPlatformIntrinsic("gtk4", lower.AsyncPostIntrinsic, func(args []ir.Expr, tr func(ir.Expr) string) (string, []string) {
		if len(args) != 1 {
			return "", nil
		}
		return "gtk4rt.Post(" + tr(args[0]) + ")", []string{gtk4rtPkg}
	})
}
