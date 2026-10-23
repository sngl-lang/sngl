package bubbletea

import (
	"fmt"
	"strconv"

	"duckfam.us/sngl/codegen"
	"duckfam.us/sngl/ir"
)

// A box's layout in cells: the gap between its parts, a flexing part's
// share of a row, and the picture an image fits.
//
// A cell is one pixel wide and two tall (tui.Picture), so a length across is
// scaleFactor pixels to the cell and a length down twice that: a 192px image
// was 24 rows tall around a picture of 12, and left a dozen blank rows under
// it.

// styleField is the value of one field of a node's literal style, or nil.
func styleField(n *ir.NodeInst, name string) ir.Expr {
	for _, f := range codegen.NodeStyleFields(n) {
		if f.Name == name {
			return f.Value
		}
	}
	return nil
}

// stylePx is a literal length field of a node's style in pixels, or zero.
func stylePx(n *ir.NodeInst, name string) float64 {
	v := styleField(n, name)
	if v == nil {
		return 0
	}
	px, _ := codegen.MeasurementPx(v, emPx)
	return px
}

// gapCells is the box's gap along its axis, in cells, as Go: a column's in
// rows, a row's in columns. A gap read from state is read in columns either
// way.
func (vc *irViewContext) gapCells(n *ir.NodeInst) string {
	v := styleField(n, "gap")
	if v == nil {
		return "0"
	}
	if px, ok := codegen.MeasurementPx(v, emPx); ok {
		if vc.vertical {
			return strconv.Itoa(int(px) / (2 * vc.scaleFactor))
		}
		return scaleVal(strconv.Itoa(int(px)), vc.scaleFactor)
	}
	return cellVal(v, vc.gc, vc.scaleFactor)
}

// pictureBox is the box, in cells, a picture in an image node is fitted into:
// its style's width and height, or what the parent box said where it gives
// neither.
func (vc *irViewContext) pictureBox(n *ir.NodeInst) (cols, rows int) {
	cols, rows = vc.pictureCols, vc.pictureRows
	w, h := stylePx(n, "width"), stylePx(n, "height")
	if w > 0 {
		cols = max(int(w)/vc.scaleFactor, 1)
	}
	if h > 0 {
		rows = max(int(h)/(2*vc.scaleFactor), 1)
	}
	return cols, rows
}

// flexOf is a part's share of the row it is in: its literal style's `flex`,
// zero for anything else.
func flexOf(s ir.Stmt) float64 {
	n, ok := s.(*ir.NodeInst)
	if !ok {
		return 0
	}
	v := styleField(n, "flex")
	if v == nil {
		return 0
	}
	f, _ := codegen.IRLiteralNumber(v)
	return f
}

// childFlexes reports whether a part of a box asks for a share of it.
func childFlexes(stmts []ir.Stmt) bool {
	for _, s := range stmts {
		if flexOf(s) > 0 {
			return true
		}
	}
	return false
}

// usesFlexRows reports whether some row of the program has a flexing part,
// which is what reads the width each box leaves its children.
func usesFlexRows(pkg *ir.Package) bool {
	if pkg == nil {
		return false
	}
	found := false
	_ = ir.Walk(pkg, func(node ir.Node) error {
		n, ok := node.(*ir.NodeInst)
		if !ok || btIntrinsic(n) == "" || codegen.NodeProp(n, "join") == nil {
			return nil
		}
		if extractBlueprint(n).Join == joinHorizontal && childFlexes(n.Children) {
			found = true
			return ir.SkipAll
		}
		return nil
	})
	return found
}

// narrow sets the width a box leaves its children (m.avail) while they are
// rendered: its style's width, or what it was given, less its padding and
// margin on either side. It returns what puts the width back. Only a program
// with a flexing row reads it.
func (vc *irViewContext) narrow(n *ir.NodeInst, resultVar string) func() {
	if !vc.flexRows {
		return func() {}
	}
	width, pad, margin := stylePx(n, "width"), stylePx(n, "padding"), stylePx(n, "margin")
	if width == 0 && pad == 0 && margin == 0 {
		return func() {}
	}
	cells := func(px float64) int {
		if px == 0 {
			return 0
		}
		return max(int(px)/vc.scaleFactor, 1)
	}
	outer := "m.avail"
	if width > 0 {
		outer = strconv.Itoa(cells(width))
	}
	saved := resultVar + "Avail"
	vc.line("%s := m.avail", saved)
	vc.line("m.avail = max(%s-%d, 0)", outer, 2*(cells(pad)+cells(margin)))
	return func() { vc.line("m.avail = %s", saved) }
}

// float32Text writes a weight as a Go float literal.
func float32Text(v float64) string {
	return strconv.FormatFloat(v, 'f', -1, 64)
}

// alignAt is where a box's `alignItems` puts its parts across it, as a Go
// lipgloss.Position: a column's at its left, center or right, a row's at its
// top, center or bottom. "" is the start, which is also where a stretched
// part sits: a part drawn is its own width, and the box pads it to its own.
// A member read at run time -- a field reading state, a Style held as a
// value -- is looked up then.
func (vc *irViewContext) alignAt(n *ir.NodeInst) string {
	end := "lipgloss.Right"
	if vc.horizontal {
		end = "lipgloss.Bottom"
	}
	var val string
	if held := styleValue(n); held != nil {
		val = vc.gc.EvalExpr(held) + ".AlignItems"
	} else {
		v := styleField(n, "alignItems")
		if v == nil {
			return ""
		}
		switch enumMember(v) {
		case "center":
			return "lipgloss.Center"
		case "end":
			return end
		case "":
			val = vc.gc.EvalExpr(v)
		default:
			return ""
		}
	}
	// A member with no entry reads the zero Position, which is the start.
	return fmt.Sprintf(`map[string]lipgloss.Position{"center": lipgloss.Center, "end": %s}[%s]`, end, val)
}
