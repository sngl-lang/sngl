package fyne

import (
	"strings"
	"testing"
)

// TestSnglColorHelperInSync guards the fyne-specific _snglColor helper (which
// maps a SNGL Color struct to color.RGBA). The shared canvas stdlib struct
// drift is covered by codegen/canvasutil's TestStructDeclsInSync; here we only
// assert the helper references the four Color fields it depends on, so a future
// rename of Color's fields can't silently break the gg color path.
func TestSnglColorHelperInSync(t *testing.T) {
	for _, field := range []string{"c.R", "c.G", "c.B", "c.A"} {
		if !strings.Contains(snglColorHelper, field) {
			t.Errorf("_snglColor helper missing reference to %q:\n%s", field, snglColorHelper)
		}
	}
}
