package bubbletea

import (
	"duckfam.us/sngl/codegen"
	"duckfam.us/sngl/ir"
)

// pictureIntrinsic is bubbletea.sngl's Picture: the picture a URL names, drawn
// in half-block cells.
const pictureIntrinsic = "Picture"

// The box a picture is fitted into, in cells, where the box it is shown in
// gives it no size (tui.PictureBox): a terminal has no size a pixel has.
const (
	pictureCols = 24
	pictureRows = 12
)

func (vc *irViewContext) renderPicture(n *ir.NodeInst, resultVar string) {
	vc.requireImport(tuiImportPath)
	url := vc.gc.EvalExpr(codegen.NodeProp(n, ir.ImageSourceURL))
	if vc.pictureLook == "" || vc.pictureLook == plainLook {
		vc.line("%s = tui.Picture(%s, %d, %d)", resultVar, url, pictureCols, pictureRows)
		return
	}
	vc.line("%sCols, %sRows := tui.PictureBox(%s, %d, %d)", resultVar, resultVar, vc.pictureLook, pictureCols, pictureRows)
	vc.line("%s = tui.Picture(%s, %sCols, %sRows)", resultVar, url, resultVar, resultVar)
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
