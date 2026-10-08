package fynehost

import (
	"image/color"

	fyne "fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"

	"duckfam.us/sngl/pkg/go/fynelayout"
	"duckfam.us/sngl/pkg/go/fynetheme"
	"duckfam.us/sngl/pkg/go/snglhost"
)

// style is the part of sngl.Style this host can answer, in the
// device-independent pixels Fyne measures in.
//
// The layout half is read by a layout -- flex and margin by the *parent's*,
// gap and padding by this box's own -- and the paint half by a theme, which is
// the only per-widget styling Fyne has.
type style struct {
	flex, margin, gap, padding float32
	background, foreground     color.Color
	textSize, radius           float32
	bold                       bool
}

// decodeStyle reads a node's `style` prop. It arrives as the Style record the
// program wrote, projected onto the wire, so nothing here parses SNGL.
func decodeStyle(props []snglhost.PropVal) style {
	var st style
	raw, ok := fieldOf(props, "style")
	if !ok {
		return st
	}
	rec, ok := raw.(snglhost.WireStruct)
	if !ok {
		return st
	}
	st.flex = num(structField(rec, "flex"))
	st.margin = num(structField(rec, "margin"))
	st.gap = num(structField(rec, "gap"))
	st.padding = num(structField(rec, "padding"))
	st.textSize = num(structField(rec, "fontSize"))
	st.radius = num(structField(rec, "borderRadius"))
	st.bold = structField(rec, "fontWeight") == "bold"
	st.background = colorOf(structField(rec, "background"))
	st.foreground = colorOf(structField(rec, "color"))
	return st
}

// theme is the paint half as a Fyne theme, or an empty one when the node asked
// for nothing.
func (s style) theme(base fyne.Theme) fynetheme.Theme {
	t := fynetheme.New(base)
	t.Background, t.Foreground = s.background, s.foreground
	t.TextSize, t.Radius, t.Bold = s.textSize, s.radius, s.bold
	return t
}

// num reads a measurement. A `measurement` crosses the wire as its number, and
// JSON has only float64.
func num(v any) float32 {
	switch x := v.(type) {
	case float64:
		return float32(x)
	case float32:
		return x
	case int:
		return float32(x)
	}
	return 0
}

// colorOf reads a color record, which is four channels rather than a string.
func colorOf(v any) color.Color {
	rec, ok := v.(snglhost.WireStruct)
	if !ok {
		return nil
	}
	a := num(structField(rec, "a"))
	if a == 0 {
		return nil // fully transparent is how "unset" reaches here
	}
	return color.NRGBA{
		R: uint8(num(structField(rec, "r"))),
		G: uint8(num(structField(rec, "g"))),
		B: uint8(num(structField(rec, "b"))),
		A: uint8(a),
	}
}

// relayout rebuilds a container's layout from its children's styles.
//
// Fyne's own boxes pack children at their minimum size, so `flex` has nowhere
// to be said; fynelayout.Weighted is what answers it. The weights are
// positional and the children change, so the layout is rebuilt rather than
// configured once.
func (h *Host) relayout(key snglhost.Key) {
	m, ok := h.nodes[key]
	if !ok {
		return
	}
	c, ok := m.obj.(*fyne.Container)
	if !ok || m.spec.axis == "" {
		return
	}
	weights := make([]float32, len(m.children))
	margins := make([]float32, len(m.children))
	for i, kid := range m.children {
		weights[i], margins[i] = kid.style.flex, kid.style.margin
	}
	c.Layout = fynelayout.New(m.spec.axis == "horizontal", weights, margins, m.style.gap, m.style.padding)
	c.Refresh()
}

// themed wraps an object in its node's paint theme, or returns it unchanged
// when the node asked for none -- a wrapper that answers nothing still costs a
// node in the tree.
func themed(obj fyne.CanvasObject, st style) fyne.CanvasObject {
	t := st.theme(fyne.CurrentApp().Settings().Theme())
	if t.Empty() {
		return obj
	}
	return container.NewThemeOverride(obj, t)
}
