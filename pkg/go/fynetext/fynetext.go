// Package fynetext is SNGL's inline rich text on Fyne.
//
// The span family is a tree of runs and what is true of each -- weight, slant,
// color, size, a rule under or through the words -- and Fyne has one widget
// that lays a run of text out inline: widget.RichText, over a flat list of
// segments. So this package is the distance between the two, and it is three
// things the host does not do for itself.
//
// # The cascade
//
// Fyne has no inheritance. A segment carries its whole appearance, so
// `bold { italic { … } }` has to arrive as one segment that is both -- which
// means the nesting an author wrote is flattened here, one leaf at a time,
// each run resolved against the runs it sits inside.
//
// It is resolved at render time rather than when the tree is built, because
// the tree is what a reactive program mutates: a `SetText` on a nested span is
// the only thing that reaches the emitted code when state moves, and the flow
// rebuilds its segments from the tree it still holds.
//
// # Color and size
//
// A segment says its color and its size by *theme name* --
// widget.RichTextStyle carries a fyne.ThemeColorName and a fyne.ThemeSizeName,
// resolved through the theme in scope for the widget -- and never as a value.
// A document that writes `color=#336699` therefore has nowhere to put it, so
// the flow collects the colors and sizes its runs asked for, names them, and
// carries a theme that answers those names. That is the same answer the fyne
// platform already gives for a widget's own paint style (pkg/go/fynetheme),
// applied one level down.
//
// # Underline and strikethrough
//
// Fyne draws neither. There is no strikethrough anywhere in it, and
// fyne.TextStyle.Underline is a field the text painter does not read -- its own
// comment says TextGrid only. So a run asking for either is rendered by a
// segment of this package's own, which draws the text and the rules across it.
//
// The cost is that it does not wrap inside itself: widget.RichText measures
// and breaks *widget.TextSegment and nothing else, so any other inline segment
// occupies one unbreakable box on the row. That is what Fyne's own
// HyperlinkSegment already is, and it is why the decorated segment is the
// exception rather than the rule: a run of emphasis is short, a paragraph is
// not.
package fynetext

import (
	"fmt"
	"image/color"
	"net/url"
	"strings"

	fyne "fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"git.duckfam.us/jonathan/sngl/pkg/go/snglcolor"
)

// Tri is a span style field that may say nothing.
//
// The cascade needs a third state: flattening `bold { normal { … } }` means an
// inner run overwrites only what it set, so "not set" has to be tellable from
// "set to the default". It is the same distinction markup.SpanStyle's two enums
// make by leading with `inherit`.
type Tri int8

const (
	// Inherit leaves whatever the enclosing runs said.
	Inherit Tri = iota
	// Off and On are the run saying so itself.
	Off
	On
)

// resolve answers what this field is worth inside a run that said outer.
func (t Tri) resolve(outer bool) bool {
	switch t {
	case On:
		return true
	case Off:
		return false
	}
	return outer
}

// SpanStyle is what one run said about its own words. A zero value says
// nothing, which is what a run that only groups its children is.
//
// The decorations are plain bools and not Tri, because the family's own
// declaration says a span inside one that turned a decoration on cannot turn
// it off: there is one property for both in CSS, and a fourth state per field
// to express it would be paid for on every run.
type SpanStyle struct {
	// Color is nil when the run named none. Size is 0 for the same reason,
	// a run of no height being no more a choice than an invisible color.
	Color     color.Color
	Size      float32
	Bold      Tri
	Italic    Tri
	Monospace Tri
	Underline bool
	Strike    bool
	// Token is what a `token` run said it *is* -- a kind and not a color,
	// the family leaving what a keyword looks like to whoever is drawing.
	// Answered by tokenColors below, in theme names rather than values, so an
	// application that themes its app themes its code samples with it.
	Token string
}

// Style begins a span style. The setters below chain off it, which is what
// lets a generated constructor name one package and spell the rest as method
// calls: `fynetext.Style().WithBold().WithSize(20)` is a value the emitter can write
// without knowing the alias its own file gave anything else.
func Style() SpanStyle { return SpanStyle{} }

// WithBold and NoBold are the run saying so; a run that says neither leaves
// the weight it was given. The same pairing for the other two tri-states.
func (s SpanStyle) WithBold() SpanStyle   { s.Bold = On; return s }
func (s SpanStyle) NoBold() SpanStyle     { s.Bold = Off; return s }
func (s SpanStyle) WithItalic() SpanStyle { s.Italic = On; return s }
func (s SpanStyle) NoItalic() SpanStyle   { s.Italic = Off; return s }
func (s SpanStyle) WithMono() SpanStyle   { s.Monospace = On; return s }
func (s SpanStyle) NoMono() SpanStyle     { s.Monospace = Off; return s }

// WithUnderline and WithStrike turn a rule on. There is no pair: a run inside
// one that turned a decoration on cannot turn it off.
func (s SpanStyle) WithUnderline() SpanStyle { s.Underline = true; return s }
func (s SpanStyle) WithStrike() SpanStyle    { s.Strike = true; return s }

// WithRGBA is the color the run named, in the channels a SNGL color literal
// carries.
func (s SpanStyle) WithRGBA(r, g, b, a uint8) SpanStyle {
	s.Color = color.NRGBA{R: r, G: g, B: b, A: a}
	return s
}

// WithSize is the run's own text size, in the device-independent pixels Fyne
// measures in.
func (s SpanStyle) WithSize(px float32) SpanStyle { s.Size = px; return s }

// WithToken is what a `token` run is.
func (s SpanStyle) WithToken(kind string) SpanStyle { s.Token = kind; return s }

// tokenColors is what a markup.Token looks like on Fyne: a name in the app's
// own theme, which is the form this host has for "whoever is drawing decides".
// A document naming #7DAEA3 instead would be unreadable against a theme it
// never heard of, and would override an application that had its own palette.
//
// Four kinds are absent and render in the foreground, which is what a theme
// with six semantic colors has to say about `plain`, `variable`, `operator`
// and `punctuation`.
var tokenColors = map[string]fyne.ThemeColorName{
	"keyword":  theme.ColorNamePrimary,
	"function": theme.ColorNameHyperlink,
	"type":     theme.ColorNameWarning,
	"constant": theme.ColorNameWarning,
	"number":   theme.ColorNameSuccess,
	"string":   theme.ColorNameSuccess,
	"comment":  theme.ColorNameDisabled,
}

// resolved is a SpanStyle with every question answered: what a leaf is
// actually rendered with, once the runs around it have had their say.
type resolved struct {
	color              color.Color
	colorName          fyne.ThemeColorName
	size               float32
	bold, italic, mono bool
	underline, strike  bool
}

// over answers this run's style inside one already resolved to outer.
func (s SpanStyle) over(outer resolved) resolved {
	out := outer
	if n, ok := tokenColors[s.Token]; ok {
		out.colorName = n
		out.color = nil
	}
	// After the token, so a palette the program set wins over the theme.
	if s.Color != nil {
		out.color = s.Color
		out.colorName = ""
	}
	if s.Size > 0 {
		out.size = s.Size
	}
	out.bold = s.Bold.resolve(outer.bold)
	out.italic = s.Italic.resolve(outer.italic)
	out.mono = s.Monospace.resolve(outer.mono)
	out.underline = out.underline || s.Underline
	out.strike = out.strike || s.Strike
	return out
}

// Span is one run of rich text: its own words, what is true of them, and the
// runs written inside it.
//
// A leaf carries Text and no children; a wrapper carries children and no text.
// Both are one type because the family is: `bold` is a run with something
// inside it and `text` is a run with words, and the tree is the same shape
// either way.
type Span struct {
	Text  string
	Href  string
	Style SpanStyle

	children []*Span
	// owner is the flow this span was eventually added to, so a SetText the
	// emitted code makes against a nested span reaches the widget that has to
	// redraw. A span not yet added to one has none, which is every span during
	// the build: the tree is assembled before it is handed over.
	owner *Flow
}

// NewSpan makes one run. Its words and its link arrive through the setters,
// because those are what a reactive program writes again.
func NewSpan(style SpanStyle) *Span { return &Span{Style: style} }

// Add writes a run inside this one.
func (s *Span) Add(child *Span) {
	s.children = append(s.children, child)
	child.adopt(s.owner)
	s.refresh()
}

// SetText replaces this run's words. It is the setter a reactive prop reaches,
// which is why it refreshes the flow rather than leaving that to the caller:
// the emitted code assigns to the span and knows nothing about the widget.
func (s *Span) SetText(text string) {
	s.Text = text
	s.refresh()
}

// SetColor is the color a palette the program set gives this run, which wins
// over its token's theme color. The unset color leaves the theme's answer.
func (s *Span) SetColor(c snglcolor.Color) {
	if c.A == 0 {
		s.Style.Color = nil
	} else {
		s.Style.Color = color.NRGBA{R: uint8(c.R), G: uint8(c.G), B: uint8(c.B), A: uint8(c.A)}
	}
	s.refresh()
}

// SetHref replaces where this run leads.
func (s *Span) SetHref(href string) {
	s.Href = href
	s.refresh()
}

func (s *Span) adopt(f *Flow) {
	if f == nil || s.owner == f {
		return
	}
	s.owner = f
	for _, c := range s.children {
		c.adopt(f)
	}
}

func (s *Span) refresh() {
	if s.owner != nil {
		s.owner.Refresh()
	}
}

// Flow shows one run of rich text: a tree of spans, flattened to the segment
// list Fyne wants, under a theme that answers the colors and sizes those spans
// asked for.
type Flow struct {
	widget.BaseWidget

	roots []*Span
	rich  *widget.RichText
	// palette is rebuilt from the tree on every refresh and is what the
	// segments' ColorName and SizeName resolve through.
	palette *palette
}

// NewFlow makes an empty flow. Fyne wraps its own words, so the whole of what
// this asks for is word wrapping; where the lines fall is the layout's.
func NewFlow() *Flow {
	f := &Flow{rich: widget.NewRichText(), palette: newPalette()}
	f.rich.Wrapping = fyne.TextWrapWord
	f.ExtendBaseWidget(f)
	return f
}

// Add writes a run into the flow.
func (f *Flow) Add(s *Span) {
	f.roots = append(f.roots, s)
	s.adopt(f)
	f.Refresh()
}

// Refresh rebuilds the segment list from the tree.
func (f *Flow) Refresh() {
	f.palette = newPalette()
	var segs []widget.RichTextSegment
	for _, s := range f.roots {
		segs = f.appendSpan(segs, s, resolved{})
	}
	f.rich.Segments = segs
	f.rich.Refresh()
	f.BaseWidget.Refresh()
}

// CreateRenderer puts the rich text under the theme its own segments named.
//
// The override wraps the RichText rather than the Flow, and its base is the
// theme in scope for the Flow -- which is whatever the block style around it
// asked for. Based on the default theme instead, a heading's own text size
// would be lost the moment one of its words named a color.
func (f *Flow) CreateRenderer() fyne.WidgetRenderer {
	f.ExtendBaseWidget(f)
	return widget.NewSimpleRenderer(container.NewThemeOverride(f.rich, f.palette.themeOver(theme.CurrentForWidget(f))))
}

// appendSpan flattens one span and the runs inside it.
//
// A span with words contributes a segment; a span with children contributes
// theirs, resolved against its own style. Both at once is legal and is how a
// leaf that also wraps something would read, so both are done, in that order.
func (f *Flow) appendSpan(segs []widget.RichTextSegment, s *Span, outer resolved) []widget.RichTextSegment {
	r := s.Style.over(outer)
	if s.Href != "" {
		return append(segs, f.linkSegment(s, r))
	}
	if s.Text != "" {
		segs = append(segs, f.textSegment(s.Text, r))
	}
	for _, c := range s.children {
		segs = f.appendSpan(segs, c, r)
	}
	return segs
}

// linkSegment renders a run that leads somewhere.
//
// Fyne's HyperlinkSegment carries its own words and no style, so a link's
// content is flattened to its text and whatever was said about it inside the
// link is lost. That buys a link that is actually followed when it is tapped,
// which is the half of a link a reader can tell is missing. A URL Fyne cannot
// parse is rendered as ordinary words rather than dropped.
func (f *Flow) linkSegment(s *Span, r resolved) widget.RichTextSegment {
	u, err := url.Parse(s.Href)
	if err != nil {
		return f.textSegment(s.textual(), r)
	}
	return &widget.HyperlinkSegment{Text: s.textual(), URL: u}
}

// textual is every word under this span, in order -- what a segment that
// cannot hold a tree has to be given instead.
func (s *Span) textual() string {
	var out strings.Builder
	out.WriteString(s.Text)
	for _, c := range s.children {
		out.WriteString(c.textual())
	}
	return out.String()
}

// textSegment renders one run of words.
//
// A run asking for a decoration gets this package's own segment, which draws
// what Fyne does not; everything else gets Fyne's, which is the only segment
// widget.RichText will break a line inside.
func (f *Flow) textSegment(text string, r resolved) widget.RichTextSegment {
	style := widget.RichTextStyle{
		Inline: true,
		TextStyle: fyne.TextStyle{
			Bold:      r.bold,
			Italic:    r.italic,
			Monospace: r.mono,
		},
	}
	if r.underline || r.strike {
		return &decorated{
			text:      text,
			style:     style.TextStyle,
			color:     r.paint(),
			size:      r.size,
			underline: r.underline,
			strike:    r.strike,
		}
	}
	style.ColorName = r.colorName
	if style.ColorName == "" {
		style.ColorName = f.palette.colorName(r.color)
	}
	style.SizeName = f.palette.sizeName(r.size)
	return &widget.TextSegment{Style: style, Text: text}
}

// paint is the color a decorated run draws in. A theme name has to be resolved
// here rather than left to the segment: RichText sets the parent pointer on
// *widget.TextSegment and on nothing else, so a decorated segment's own lookup
// would resolve against the app's theme rather than the flow's.
func (r resolved) paint() color.Color {
	if r.color != nil {
		return r.color
	}
	if r.colorName != "" {
		return theme.Color(r.colorName)
	}
	return nil
}

// palette is the theme names one flow's runs asked for, and the values behind
// them.
//
// A name per distinct value rather than per run: two words in the same color
// are one entry, which is what keeps the theme a flow carries proportional to
// what the document actually says rather than to how long it is.
type palette struct {
	colors map[color.Color]fyne.ThemeColorName
	sizes  map[float32]fyne.ThemeSizeName
}

func newPalette() *palette {
	return &palette{
		colors: map[color.Color]fyne.ThemeColorName{},
		sizes:  map[float32]fyne.ThemeSizeName{},
	}
}

// colorName registers a color and returns the name a segment reaches it by.
// The empty name is a run that asked for none, which is what leaves a segment
// on the foreground color it would have had.
func (p *palette) colorName(c color.Color) fyne.ThemeColorName {
	if c == nil {
		return ""
	}
	if n, ok := p.colors[c]; ok {
		return n
	}
	n := fyne.ThemeColorName(fmt.Sprintf("sngl.span.color.%d", len(p.colors)))
	p.colors[c] = n
	return n
}

func (p *palette) sizeName(s float32) fyne.ThemeSizeName {
	if s <= 0 {
		return ""
	}
	if n, ok := p.sizes[s]; ok {
		return n
	}
	n := fyne.ThemeSizeName(fmt.Sprintf("sngl.span.size.%d", len(p.sizes)))
	p.sizes[s] = n
	return n
}

// themeOver answers this palette's names, and hands everything else to base.
func (p *palette) themeOver(base fyne.Theme) fyne.Theme {
	byColor := map[fyne.ThemeColorName]color.Color{}
	for c, n := range p.colors {
		byColor[n] = c
	}
	bySize := map[fyne.ThemeSizeName]float32{}
	for s, n := range p.sizes {
		bySize[n] = s
	}
	return &spanTheme{Theme: base, colors: byColor, sizes: bySize}
}

type spanTheme struct {
	fyne.Theme
	colors map[fyne.ThemeColorName]color.Color
	sizes  map[fyne.ThemeSizeName]float32
}

func (t *spanTheme) Color(n fyne.ThemeColorName, v fyne.ThemeVariant) color.Color {
	if c, ok := t.colors[n]; ok {
		return c
	}
	return t.Theme.Color(n, v)
}

func (t *spanTheme) Size(n fyne.ThemeSizeName) float32 {
	if s, ok := t.sizes[n]; ok {
		return s
	}
	return t.Theme.Size(n)
}

// decorated is a run with a rule under or through it.
//
// It embeds Fyne's own TextSegment for the interface it does not change --
// Textual, the selection no-ops -- and answers Visual and Update itself,
// because the rules are canvas objects Fyne's segment does not draw.
//
// Its color is a value rather than a theme name: RichText sets the parent
// pointer on *widget.TextSegment and on nothing else, so the embedded lookup
// would resolve against the app's theme instead of the flow's, and a value is
// what this segment has in hand anyway.
type decorated struct {
	widget.TextSegment

	text              string
	style             fyne.TextStyle
	color             color.Color
	size              float32
	underline, strike bool
}

// Inline is true: a decorated run sits on the line with the words around it.
// It does not *wrap* there -- see the package comment -- but where it starts is
// the row's business and not a block of its own.
func (d *decorated) Inline() bool { return true }

func (d *decorated) Textual() string { return d.text }

func (d *decorated) Visual() fyne.CanvasObject {
	txt := canvas.NewText(d.text, d.paint())
	txt.TextStyle = d.style
	txt.TextSize = d.textSize()
	objs := []fyne.CanvasObject{txt}
	if d.underline {
		objs = append(objs, canvas.NewLine(d.paint()))
	}
	if d.strike {
		objs = append(objs, canvas.NewLine(d.paint()))
	}
	return container.New(&decorLayout{underline: d.underline, strike: d.strike}, objs...)
}

func (d *decorated) Update(o fyne.CanvasObject) {
	c, ok := o.(*fyne.Container)
	if !ok || len(c.Objects) == 0 {
		return
	}
	txt, ok := c.Objects[0].(*canvas.Text)
	if !ok {
		return
	}
	txt.Text = d.text
	txt.Color = d.paint()
	txt.TextStyle = d.style
	txt.TextSize = d.textSize()
	for _, line := range c.Objects[1:] {
		if l, ok := line.(*canvas.Line); ok {
			l.StrokeColor = d.paint()
		}
	}
	c.Refresh()
}

// paint is the color this run draws in: its own, or the foreground it would
// have had.
func (d *decorated) paint() color.Color {
	if d.color != nil {
		return d.color
	}
	return theme.Color(theme.ColorNameForeground)
}

func (d *decorated) textSize() float32 {
	if d.size > 0 {
		return d.size
	}
	return theme.Size(theme.SizeNameText)
}

// decorLayout puts the text at the origin and the rules across it. The lines
// come in the order Visual appended them: the underline first when there is
// one, then the strike.
type decorLayout struct{ underline, strike bool }

func (l *decorLayout) MinSize(objs []fyne.CanvasObject) fyne.Size {
	if len(objs) == 0 {
		return fyne.Size{}
	}
	return objs[0].MinSize()
}

func (l *decorLayout) Layout(objs []fyne.CanvasObject, size fyne.Size) {
	if len(objs) == 0 {
		return
	}
	txt := objs[0]
	txt.Move(fyne.NewPos(0, 0))
	txt.Resize(size)
	rest := objs[1:]
	if l.underline && len(rest) > 0 {
		// Just below the baseline, which is where a reader expects it and
		// which the descenders of a `g` reach into either way.
		place(rest[0], size, size.Height*0.92)
		rest = rest[1:]
	}
	if l.strike && len(rest) > 0 {
		place(rest[0], size, size.Height*0.55)
	}
}

func place(o fyne.CanvasObject, size fyne.Size, y float32) {
	line, ok := o.(*canvas.Line)
	if !ok {
		return
	}
	line.Position1 = fyne.NewPos(0, y)
	line.Position2 = fyne.NewPos(size.Width, y)
	line.StrokeWidth = 1
	line.Refresh()
}
