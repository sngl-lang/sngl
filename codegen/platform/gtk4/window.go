package gtk4

import (
	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/ir"
)

// A window's `open` and `close` present and hide the GtkWindow the Model
// keeps under the window's `#id`: the receiver is the handle, which the Go
// context already spells as that field.
func init() {
	codegen.RegisterPlatformIntrinsic("gtk4", codegen.WindowOpenIntrinsic, func(args []ir.Expr, tr func(ir.Expr) string) (string, []string) {
		if len(args) != 1 {
			return "", nil
		}
		return "gtk4rt.WindowPresent(" + tr(args[0]) + ")", []string{gtk4rtPkg}
	})
	codegen.RegisterPlatformIntrinsic("gtk4", codegen.WindowCloseIntrinsic, func(args []ir.Expr, tr func(ir.Expr) string) (string, []string) {
		if len(args) != 1 {
			return "", nil
		}
		return "gtk4rt.WindowHide(" + tr(args[0]) + ")", []string{gtk4rtPkg}
	})
}
