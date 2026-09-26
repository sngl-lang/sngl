package bubbletea

import (
	"fmt"

	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/ir"
)

// styleValueFunc turns a whole Style value -- one held in state rather than
// written as a literal -- into the lipgloss style irStyleCall builds for a
// literal. A field left unset in the value is its zero, which is also what
// __merge_Style reads as unset, so a zero field adds nothing.
const styleValueFunc = "__styleLipgloss"

// styleValue is the node's style when it is a whole value, nil when it is a
// literal or absent.
func styleValue(n *ir.NodeInst) ir.Expr {
	v := codegen.NodeProp(n, "style")
	if v == nil {
		return nil
	}
	if _, lit := v.(*ir.StructLit); lit {
		return nil
	}
	if t := v.ExprType(); t == nil || t.Kind != ir.TypeStruct {
		return nil
	}
	return v
}

func usesStyleValue(pkg *ir.Package) bool {
	found := false
	visit := func(nd ir.Node) error {
		if n, ok := nd.(*ir.NodeInst); ok && styleValue(n) != nil {
			found = true
		}
		return nil
	}
	for _, o := range ir.Owners(pkg) {
		_ = ir.Walk(*o.Body, visit)
		for _, f := range o.Funcs {
			_ = ir.Walk(f.Block, visit)
		}
	}
	return found
}

// nodeStyle is the lipgloss style expression a node renders with.
func (vc *irViewContext) nodeStyle(n *ir.NodeInst) string {
	if v := styleValue(n); v != nil {
		return styleValueFunc + "(" + vc.gc.EvalExpr(v) + ")"
	}
	return buildIRStyleExpr(codegen.NodeStyleFields(n), vc.gc, vc.scaleFactor)
}

func styleValueDecl(scale int) string {
	return fmt.Sprintf(`func %s(s Style) lipgloss.Style {
	cells := func(v float64) int {
		if v == 0 {
			return 0
		}
		return max(1, int(v)/%d)
	}
	st := lipgloss.NewStyle()
	if s.Color != (snglcolor.Color{}) {
		st = st.Foreground(lipgloss.Color(s.Color.String()))
	}
	if s.Background != (snglcolor.Color{}) {
		st = st.Background(lipgloss.Color(s.Background.String()))
	}
	if s.BorderColor != (snglcolor.Color{}) {
		st = st.BorderForeground(lipgloss.Color(s.BorderColor.String()))
	}
	if s.BorderWidth != (Measurement{}) {
		st = st.Border(lipgloss.NormalBorder())
	}
	if s.FontWeight == "bold" {
		st = st.Bold(true)
	}
	if s.FontStyle == "italic" {
		st = st.Italic(true)
	}
	switch s.TextAlign {
	case "center":
		st = st.AlignHorizontal(lipgloss.Center)
	case "right":
		st = st.AlignHorizontal(lipgloss.Right)
	}
	if s.Opacity != 0 {
		st = st.Faint(true)
	}
	if v := cells(s.Padding.Px); v != 0 {
		st = st.Padding(v)
	}
	if v := cells(s.Margin.Px); v != 0 {
		st = st.Margin(v)
	}
	if v := cells(s.PaddingTop.Px); v != 0 {
		st = st.PaddingTop(v)
	}
	if v := cells(s.PaddingRight.Px); v != 0 {
		st = st.PaddingRight(v)
	}
	if v := cells(s.PaddingBottom.Px); v != 0 {
		st = st.PaddingBottom(v)
	}
	if v := cells(s.PaddingLeft.Px); v != 0 {
		st = st.PaddingLeft(v)
	}
	if v := cells(s.MarginTop.Px); v != 0 {
		st = st.MarginTop(v)
	}
	if v := cells(s.MarginRight.Px); v != 0 {
		st = st.MarginRight(v)
	}
	if v := cells(s.MarginBottom.Px); v != 0 {
		st = st.MarginBottom(v)
	}
	if v := cells(s.MarginLeft.Px); v != 0 {
		st = st.MarginLeft(v)
	}
	if v := cells(s.Width.Px); v != 0 {
		st = st.Width(v)
	}
	if v := cells(s.Height.Px); v != 0 {
		st = st.Height(v)
	}
	if v := cells(s.MaxWidth.Px); v != 0 {
		st = st.MaxWidth(v)
	}
	if v := cells(s.MaxHeight.Px); v != 0 {
		st = st.MaxHeight(v)
	}
	return st
}

`, styleValueFunc, scale)
}
