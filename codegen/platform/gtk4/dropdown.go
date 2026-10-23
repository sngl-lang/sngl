package gtk4

import (
	"fmt"

	"duckfam.us/sngl/codegen"
	"duckfam.us/sngl/ir"
)

// offerChoicesIntrinsic is gtk4.sngl's offerChoices, which a select's
// override offers its GtkDropDown its choices through. It is a call of the
// template's snglDropDownOffer, which reaches gtk4rt's DropDownOffer with the
// drop-down as each mode holds it, so a program with a select stays wrapped
// where it can.
const offerChoicesIntrinsic = "gtk4.offerChoices"

func init() {
	codegen.RegisterPlatformIntrinsic("gtk4", offerChoicesIntrinsic, func(args []ir.Expr, tr func(ir.Expr) string) (string, []string) {
		if len(args) != 3 {
			return "", nil
		}
		return fmt.Sprintf("snglDropDownOffer(%s, %s, %s)", tr(args[0]), tr(args[1]), tr(args[2])), nil
	})
}
