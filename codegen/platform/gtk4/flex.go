package gtk4

import (
	"duckfam.us/sngl/codegen"
	"duckfam.us/sngl/ir"
)

// styleFlexes reports whether a `style` value asks for a share of its box. A
// flex that is not a literal is none, as on fyne: the expand is set where the
// widget is built.
func styleFlexes(style ir.Expr) bool {
	lit, ok := style.(*ir.StructLit)
	if !ok {
		return false
	}
	for _, f := range lit.Fields {
		if f.Name == "flex" {
			v, ok := codegen.MeasurementPx(f.Value, 0)
			return ok && v > 0
		}
	}
	return false
}
