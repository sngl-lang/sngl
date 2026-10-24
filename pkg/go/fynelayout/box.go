package fynelayout

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"duckfam.us/sngl/pkg/go/fynetheme"
	"duckfam.us/sngl/pkg/go/snglcolor"
)

// Look is what a node's box asks of Fyne, in device-independent pixels. The
// fyne platform's style.sngl builds one from a ui.Style; a zero field asks
// for nothing, and a color with no alpha is no color.
type Look struct {
	MarginTop, MarginRight, MarginBottom, MarginLeft     float32
	PaddingTop, PaddingRight, PaddingBottom, PaddingLeft float32

	// Width and Height fix the box's outer size, inside its margin.
	Width, Height, MinWidth, MaxWidth, MaxHeight float32

	// Grow is the share of its parent's free space the box takes, read by
	// the Weighted layout holding it.
	Grow float32

	// Gap, Cross and Justify are read by the Weighted layout this box holds,
	// when it holds one. A zero Gap is the theme's padding, as Fyne's own
	// boxes space their children.
	Gap            float32
	Cross, Justify int

	Fill, Stroke snglcolor.Color
	StrokeWidth  float32
	Radius       float32

	TextColor               snglcolor.Color
	TextSize                float32
	Bold, Italic, Monospace bool
	TextAlign, Wrap         int

	Hidden bool
}

// Cross is where a Weighted layout's children sit across its axis.
const (
	CrossStretch = iota
	CrossStart
	CrossCenter
	CrossEnd
)

// Justify is where a Weighted layout puts the space no child grows into.
const (
	JustifyStart = iota
	JustifyCenter
	JustifyEnd
	JustifyBetween
	JustifyAround
	JustifyEvenly
)

// TextAlign is a label's or a button's alignment; zero leaves the widget's own.
const (
	TextAlignDefault = iota
	TextAlignLeading
	TextAlignCenter
	TextAlignTrailing
)

// Wrap is how a label or an entry breaks its lines; zero leaves the widget's own.
const (
	WrapDefault = iota
	WrapOff
	WrapWord
)

// Box shows one widget inside the margin, border, fill and padding its Look
// asks for, under a theme carrying the Look's text and paint, as CSS's
// border-box lays a node out.
type Box struct {
	widget.BaseWidget
	Content fyne.CanvasObject
	look    Look
	fill    *canvas.Rectangle
	// themed is made the first time the Look asks for a theme: an override
	// re-measures its whole subtree on every refresh.
	themed *container.ThemeOverride
	theme  fynetheme.Theme
	// in is the Weighted layout holding the box, told when its share changes.
	in *Weighted
	// own is what the widget was built with, which a Look asking for nothing
	// restores.
	own text
}

type text struct {
	style       fyne.TextStyle
	align       fyne.TextAlign
	buttonAlign widget.ButtonAlign
	wrap        fyne.TextWrap
}

func NewBox(content fyne.CanvasObject) *Box {
	b := &Box{
		Content: content,
		fill:    canvas.NewRectangle(color.Transparent),
	}
	switch w := content.(type) {
	case *widget.Label:
		b.own.style, b.own.align, b.own.wrap = w.TextStyle, w.Alignment, w.Wrapping
	case *widget.Button:
		b.own.buttonAlign = w.Alignment
	case *widget.Entry:
		b.own.wrap = w.Wrapping
	case *widget.Hyperlink:
		b.own.align, b.own.wrap = w.Alignment, w.Wrapping
	}
	b.ExtendBaseWidget(b)
	return b
}

// Grow is the share of its parent's free space this box asked for.
func (b *Box) Grow() float32 { return b.look.Grow }

func (b *Box) SetLook(l Look) {
	regrow := l.Grow != b.look.Grow && b.in != nil
	b.look = l
	b.fill.FillColor = nrgba(l.Fill)
	b.fill.StrokeWidth = l.StrokeWidth
	b.fill.CornerRadius = l.Radius
	b.setText(l)
	if w, ok := layoutOf(b.Content); ok {
		w.Gap, w.Cross, w.Justify = l.Gap, l.Cross, l.Justify
	}
	if l.Hidden {
		b.Hide()
	} else {
		b.Show()
	}
	b.Refresh()
	if regrow {
		b.in.c.Refresh()
	}
}

func layoutOf(o fyne.CanvasObject) (*Weighted, bool) {
	c, ok := o.(*fyne.Container)
	if !ok {
		return nil, false
	}
	w, ok := c.Layout.(*Weighted)
	return w, ok
}

func (b *Box) setText(l Look) {
	align, buttonAlign, wrap := b.own.align, b.own.buttonAlign, b.own.wrap
	switch l.TextAlign {
	case TextAlignLeading:
		align, buttonAlign = fyne.TextAlignLeading, widget.ButtonAlignLeading
	case TextAlignCenter:
		align, buttonAlign = fyne.TextAlignCenter, widget.ButtonAlignCenter
	case TextAlignTrailing:
		align, buttonAlign = fyne.TextAlignTrailing, widget.ButtonAlignTrailing
	}
	switch l.Wrap {
	case WrapOff:
		wrap = fyne.TextWrapOff
	case WrapWord:
		wrap = fyne.TextWrapWord
	}
	switch w := b.Content.(type) {
	case *widget.Label:
		// A label measures itself in its TextStyle's face, not its theme's,
		// so the face the theme draws it in is its TextStyle's too.
		style := b.own.style
		style.Bold, style.Italic, style.Monospace = style.Bold || l.Bold, style.Italic || l.Italic, style.Monospace || l.Monospace
		w.Alignment, w.Wrapping, w.TextStyle = align, wrap, style
	case *widget.Button:
		w.Alignment = buttonAlign
	case *widget.Entry:
		w.Wrapping = wrap
	case *widget.Hyperlink:
		w.Alignment, w.Wrapping = align, wrap
	}
}

// retheme puts the widget under its Look's theme over the one the box itself
// is under, so a field the Look leaves alone is inherited rather than reset,
// and reports whether that theme changed.
func (b *Box) retheme() bool {
	base := theme.CurrentForWidget(b)
	if b.themed == nil && b.look.themes() {
		b.themed = container.NewThemeOverride(b.Content, base)
	}
	changed := false
	if b.themed != nil {
		if t := themeOf(b.look, base); !sameTheme(t, b.theme) {
			b.theme, b.themed.Theme, changed = t, t, true
		}
		base = b.theme
	}
	// An outline with no color of its own is the text's, as CSS's currentColor.
	if b.look.Stroke.A != 0 {
		b.fill.StrokeColor = nrgba(b.look.Stroke)
	} else {
		b.fill.StrokeColor = base.Color(theme.ColorNameForeground, variant())
	}
	return changed
}

func (l Look) themes() bool {
	return l.Fill.A != 0 || l.TextColor.A != 0 || l.TextSize != 0 || l.Radius != 0 || l.Bold || l.Italic || l.Monospace
}

// sameTheme compares two themes, a base whose type has no == being taken as
// changed.
func sameTheme(a, b fynetheme.Theme) (same bool) {
	defer func() {
		if recover() != nil {
			same = false
		}
	}()
	return a == b
}

// inner is what the box shows inside its padding.
func (b *Box) inner() fyne.CanvasObject {
	if b.themed != nil {
		return b.themed
	}
	return b.Content
}

func variant() fyne.ThemeVariant {
	if a := fyne.CurrentApp(); a != nil {
		return a.Settings().ThemeVariant()
	}
	return theme.VariantLight
}

func themeOf(l Look, base fyne.Theme) fynetheme.Theme {
	t := fynetheme.New(base)
	if l.Fill.A != 0 {
		t.Background = nrgba(l.Fill)
	}
	if l.TextColor.A != 0 {
		t.Foreground = nrgba(l.TextColor)
	}
	t.TextSize, t.Radius = l.TextSize, l.Radius
	t.Bold, t.Italic, t.Monospace = l.Bold, l.Italic, l.Monospace
	return t
}

func nrgba(c snglcolor.Color) color.Color {
	return color.NRGBA{R: uint8(c.R), G: uint8(c.G), B: uint8(c.B), A: uint8(c.A)}
}

func (b *Box) CreateRenderer() fyne.WidgetRenderer {
	return &boxRenderer{b}
}

type boxRenderer struct{ b *Box }

func (r *boxRenderer) Objects() []fyne.CanvasObject {
	return []fyne.CanvasObject{r.b.fill, r.b.inner()}
}

func (r *boxRenderer) Destroy() {}

func (r *boxRenderer) Refresh() {
	if r.b.retheme() {
		r.b.themed.Refresh()
	}
	r.b.fill.Refresh()
}

// insets are what lies around the content inside the margin: the border and
// the padding, as a width and a height.
func (l Look) insets() (float32, float32) {
	return l.PaddingLeft + l.PaddingRight + 2*l.StrokeWidth, l.PaddingTop + l.PaddingBottom + 2*l.StrokeWidth
}

// outer is the size inside the margin, the content's minimum bounded as the
// Look says.
func (l Look) outer(content fyne.Size) fyne.Size {
	iw, ih := l.insets()
	w, h := content.Width+iw, content.Height+ih
	if l.Width > 0 {
		w = l.Width
	}
	if l.Height > 0 {
		h = l.Height
	}
	w = max(w, l.MinWidth)
	if l.MaxWidth > 0 {
		w = min(w, l.MaxWidth)
	}
	if l.MaxHeight > 0 {
		h = min(h, l.MaxHeight)
	}
	return fyne.NewSize(w, h)
}

func (r *boxRenderer) MinSize() fyne.Size {
	l := r.b.look
	o := l.outer(r.b.inner().MinSize())
	return fyne.NewSize(o.Width+l.MarginLeft+l.MarginRight, o.Height+l.MarginTop+l.MarginBottom)
}

func (r *boxRenderer) Layout(size fyne.Size) {
	l := r.b.look
	w := size.Width - l.MarginLeft - l.MarginRight
	h := size.Height - l.MarginTop - l.MarginBottom
	if l.Width > 0 {
		w = min(w, l.Width)
	}
	if l.Height > 0 {
		h = min(h, l.Height)
	}
	if l.MaxWidth > 0 {
		w = min(w, l.MaxWidth)
	}
	if l.MaxHeight > 0 {
		h = min(h, l.MaxHeight)
	}
	w, h = max(w, 0), max(h, 0)
	r.b.fill.Move(fyne.NewPos(l.MarginLeft, l.MarginTop))
	r.b.fill.Resize(fyne.NewSize(w, h))
	iw, ih := l.insets()
	r.b.inner().Move(fyne.NewPos(l.MarginLeft+l.PaddingLeft+l.StrokeWidth, l.MarginTop+l.PaddingTop+l.StrokeWidth))
	r.b.inner().Resize(fyne.NewSize(max(w-iw, 0), max(h-ih, 0)))
}
