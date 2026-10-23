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
// for a box whose style or children ask for what only this layout answers, and
// nil when the box's own constructor will do.
//
// A box of fixed children whose style says nothing is exactly what
// container.NewVBox already does. One whose style names a gap, a padding or an
// alignment is this layout from the start: Fyne's box spaces its children by
// the theme, and a box adopted later (fynelayout.Adopt) cannot know the gap
// its style asked for. What each child asked for is its own (shareStmt),
// since a render slot puts its anchor and content among the children written
// here.
func (t *fyneTranslator) flexLayoutCall(sp *fyneSpec) ir.Expr {
	if sp == nil || sp.Axis == "" || !(sp.lays() || t.anyChildFlexes(sp)) {
		return nil
	}
	newContainer := fyneNative{Path: "fyne.io/fyne/v2/container", Name: "New"}.qualify(t.gc)
	args := fmt.Sprintf("%t, %s, %s", sp.Axis == "horizontal", float32Lit(sp.Style.Gap), float32Lit(sp.Style.Padding))
	var layout string
	if align := alignments[sp.Style.Align]; align != "" {
		layout = fmt.Sprintf("%s(%s, %s)", fyneNative{Path: fyneLayoutImportPath, Name: "NewAligned"}.qualify(t.gc), args,
			fyneNative{Path: fyneLayoutImportPath, Name: align}.qualify(t.gc))
	} else {
		layout = fmt.Sprintf("%s(%s)", fyneNative{Path: fyneLayoutImportPath, Name: "New"}.qualify(t.gc), args)
	}
	return rawGoExpr(newContainer+"("+layout+")", ir.NativeGoPointerOf("fyne.Container"))
}

// alignments is the fynelayout.Alignment each `alignItems` member is, of the
// ones that place a child at its own minimum; stretch is the layout's default,
// and a baseline Fyne cannot read is the start.
var alignments = map[string]string{
	"start":    "Start",
	"baseline": "Start",
	"center":   "Center",
	"end":      "End",
}

// lays reports whether a box's own style asks its layout for something Fyne's
// box does not do.
func (sp *fyneSpec) lays() bool {
	return sp.Style.Gap > 0 || sp.Style.Padding > 0 || alignments[sp.Style.Align] != ""
}

// shares reports whether a node asks the box holding it for anything.
func (sp *fyneSpec) shares() bool {
	return sp != nil && (sp.Style.Flex > 0 || sp.Style.Margin > 0 || sp.sized())
}

// sized reports whether the node's style gave it a width or a height.
func (sp *fyneSpec) sized() bool {
	return sp.Style.Width > 0 || sp.Style.Height > 0
}

// shareCall records on obj, the object the node's box holds, what the node
// asks of that box, and is obj.
func shareCall(obj ir.Expr, sp *fyneSpec) *ir.Call {
	args := []ir.Expr{obj, rawGoExpr(float32Lit(sp.Style.Flex), ir.TypFloat), rawGoExpr(float32Lit(sp.Style.Margin), ir.TypFloat)}
	if sp.sized() {
		args = append(args, rawGoExpr(float32Lit(sp.Style.Width), ir.TypFloat), rawGoExpr(float32Lit(sp.Style.Height), ir.TypFloat))
		return nativeCallAt("fynelayout.ShareSized", fyneLayoutImportPath, args, fyneObject())
	}
	return nativeCallAt("fynelayout.Share", fyneLayoutImportPath, args, fyneObject())
}

// anyChildFlexes reports whether some child claimed a share of this box, a
// margin or a size of its own, which only this layout reads: a child's margin
// in a box that was Fyne's was dropped.
func (t *fyneTranslator) anyChildFlexes(sp *fyneSpec) bool {
	for _, id := range sp.Children {
		if child := t.specs[id]; child.shares() {
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
