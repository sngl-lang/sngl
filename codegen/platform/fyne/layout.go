package fyne

import (
	"fmt"
	"strconv"

	"duckfam.us/sngl/ir"
)

// fyneLayoutImportPath is the SNGL runtime package holding the weighted
// layout. Fyne's own boxes pack their children at minimum size, so `flex` --
// a child asking for a share of its parent -- has no layout in the toolkit
// that answers it.
const fyneLayoutImportPath = "duckfam.us/sngl/pkg/go/fynelayout"

// flexLayoutCall returns the `container.New(fynelayout.New(...))` constructor
// for a box some child asked for a share of, and nil when the box's own
// constructor will do.
//
// The decision is the children's rather than the box's: a box of fixed
// children is exactly what container.NewVBox already does, and swapping a
// layout in there would change a tree nothing asked to change. What each child
// asked for is its own (shareStmt), since a render slot puts its anchor and
// content among the children written here.
func (t *fyneTranslator) flexLayoutCall(sp *fyneSpec) ir.Expr {
	if sp == nil || sp.Axis == "" || !t.anyChildFlexes(sp) {
		return nil
	}
	newLayout := fyneNative{Path: fyneLayoutImportPath, Name: "New"}.qualify(t.gc)
	newContainer := fyneNative{Path: "fyne.io/fyne/v2/container", Name: "New"}.qualify(t.gc)
	layout := fmt.Sprintf("%s(%t, %s, %s)",
		newLayout,
		sp.Axis == "horizontal",
		float32Lit(sp.Style.Gap),
		float32Lit(sp.Style.Padding),
	)
	return rawGoExpr(newContainer+"("+layout+")", ir.NativeGoPointerOf("fyne.Container"))
}

// shares reports whether a node asks the box holding it for anything.
func (sp *fyneSpec) shares() bool {
	return sp != nil && (sp.Style.Flex > 0 || sp.Style.Margin > 0)
}

// shareCall records on obj, the object the node's box holds, what the node
// asks of that box, and is obj.
func shareCall(obj ir.Expr, sp *fyneSpec) *ir.Call {
	return nativeCallAt("fynelayout.Share", fyneLayoutImportPath,
		[]ir.Expr{obj, rawGoExpr(float32Lit(sp.Style.Flex), ir.TypFloat), rawGoExpr(float32Lit(sp.Style.Margin), ir.TypFloat)}, fyneObject())
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
