package fyne

import (
	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/internal/lower"
	"git.duckfam.us/jonathan/sngl/ir"
)

// Fyne's widgets belong to the goroutine running the driver, and fyne.Do is
// how anything else reaches it -- the same call the remote store's settle
// already goes through. Do rather than DoAndWait: the goroutine that posts has
// nothing left to do, and waiting for the UI to catch up would only hold it
// open.
func init() {
	codegen.RegisterPlatformIntrinsic("fyne", lower.AsyncPostIntrinsic, func(args []ir.Expr, tr func(ir.Expr) string) (string, []string) {
		if len(args) != 1 {
			return "", nil
		}
		return "fyne.Do(" + tr(args[0]) + ")", []string{"fyne.io/fyne/v2"}
	})
}
