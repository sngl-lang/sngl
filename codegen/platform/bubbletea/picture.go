package bubbletea

import (
	"duckfam.us/sngl/codegen"
	"duckfam.us/sngl/ir"
)

// pictureIntrinsic is bubbletea.sngl's Picture: the picture a URL names, drawn
// in half-block cells.
const pictureIntrinsic = "Picture"

// The box a picture is fitted into, in cells, where the image holding it
// gives no size (pictureBox).
const (
	pictureCols = 24
	pictureRows = 12
)

func (vc *irViewContext) renderPicture(n *ir.NodeInst, resultVar string) {
	vc.requireImport(tuiImportPath)
	cols, rows := pictureCols, pictureRows
	if vc.pictureCols > 0 {
		cols = vc.pictureCols
	}
	if vc.pictureRows > 0 {
		rows = vc.pictureRows
	}
	vc.line("%s = tui.Picture(%s, %d, %d)", resultVar, vc.gc.EvalExpr(codegen.NodeProp(n, ir.ImageSourceURL)), cols, rows)
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
