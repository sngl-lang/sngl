package fyne

import (
	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/ir"
)

// A window's `open` and `close` show and hide the fyne.Window the Model keeps
// under the window's `#id`: the receiver is the handle, which the Go context
// already spells as that field.
func init() {
	codegen.RegisterPlatformIntrinsic("fyne", codegen.WindowOpenIntrinsic, func(args []ir.Expr, tr func(ir.Expr) string) (string, []string) {
		if len(args) != 1 {
			return "", nil
		}
		return tr(args[0]) + ".Show()", nil
	})
	codegen.RegisterPlatformIntrinsic("fyne", codegen.WindowCloseIntrinsic, func(args []ir.Expr, tr func(ir.Expr) string) (string, []string) {
		if len(args) != 1 {
			return "", nil
		}
		return tr(args[0]) + ".Hide()", nil
	})
}
