package bubbletea

import (
	"duckfam.us/sngl/codegen"
	"duckfam.us/sngl/ir"
)

// pictureIntrinsic is bubbletea.sngl's Picture: the picture a URL names, drawn
// in half-block cells.
const pictureIntrinsic = "Picture"

// The box a picture is fitted into, in cells. A terminal has no size a pixel
// has, so nothing in the program says one.
const (
	pictureCols = 24
	pictureRows = 12
)

func (vc *irViewContext) renderPicture(n *ir.NodeInst, resultVar string) {
	vc.requireImport(tuiImportPath)
	vc.line("%s = tui.Picture(%s, %d, %d)", resultVar, vc.gc.EvalExpr(codegen.NodeProp(n, ir.ImageSourceURL)), pictureCols, pictureRows)
}

// usesPictures reports whether the view draws a picture, whose fetch arriving
// is a reason to draw the view again.
func usesPictures(pkg *ir.Package) bool {
	found := false
	_ = ir.Walk(pkg, func(node ir.Node) error {
		if n, ok := node.(*ir.NodeInst); ok && btIntrinsic(n) == pictureIntrinsic {
			found = true
			return ir.SkipAll
		}
		return nil
	})
	return found
}
