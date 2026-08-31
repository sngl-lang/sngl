package fyne

import (
	"fmt"
	"strconv"
	"strings"

	"git.duckfam.us/jonathan/sngl/ir"
)

// fyneLayoutImportPath is the SNGL runtime package holding the weighted
// layout. Fyne's own boxes pack their children at minimum size, so `flex` --
// a child asking for a share of its parent -- has no layout in the toolkit
// that answers it.
const fyneLayoutImportPath = "git.duckfam.us/jonathan/sngl/pkg/go/fynelayout"

// flexLayoutCall returns the `container.New(fynelayout.New(...))` constructor
// for a box some child asked for a share of, and nil when the box's own
// constructor will do.
//
// The decision is the children's rather than the box's: a box of fixed
// children is exactly what container.NewVBox already does, and swapping a
// layout in there would change a tree nothing asked to change.
func (t *fyneTranslator) flexLayoutCall(sp *fyneSpec) ir.Expr {
	if sp == nil || sp.Axis == "" || !t.anyChildFlexes(sp) {
		return nil
	}
	weights := make([]string, 0, len(sp.Children))
	margins := make([]string, 0, len(sp.Children))
	for _, id := range sp.Children {
		var flex, margin float64
		if child := t.specs[id]; child != nil {
			flex, margin = child.Style.Flex, child.Style.Margin
		}
		weights = append(weights, float32Lit(flex))
		margins = append(margins, float32Lit(margin))
	}

	newLayout := fyneNative{Path: fyneLayoutImportPath, Name: "New"}.qualify(t.gc)
	newContainer := fyneNative{Path: "fyne.io/fyne/v2/container", Name: "New"}.qualify(t.gc)
	layout := fmt.Sprintf("%s(%t, []float32{%s}, []float32{%s}, %s, %s)",
		newLayout,
		sp.Axis == "horizontal",
		strings.Join(weights, ", "),
		strings.Join(margins, ", "),
		float32Lit(sp.Style.Gap),
		float32Lit(sp.Style.Padding),
	)
	return rawGoExpr(newContainer + "(" + layout + ")")
}

// anyChildFlexes reports whether some child claimed a share of this box.
func (t *fyneTranslator) anyChildFlexes(sp *fyneSpec) bool {
	for _, id := range sp.Children {
		if child := t.specs[id]; child != nil && child.Style.Flex > 0 {
			return true
		}
	}
	return false
}

// float32Lit writes a value as an untyped Go float literal, which takes the
// float32 of the slice it lands in.
func float32Lit(v float64) string {
	return strconv.FormatFloat(v, 'f', -1, 32)
}
