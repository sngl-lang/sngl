package fynetext

import (
	"image/color"
	"os"
	"testing"

	fynetest "fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

// Building a flow refreshes it, and a refresh reaches for the running app.
// There is no window in these tests, only the segment list.
func TestMain(m *testing.M) {
	fynetest.NewApp()
	os.Exit(m.Run())
}

// leaf is a run of words with nothing said about it.
func leaf(text string) *Span {
	s := NewSpan(SpanStyle{})
	s.Text = text
	return s
}

// wrap builds `outer { inner… }` in one expression.
func wrap(style SpanStyle, inner ...*Span) *Span {
	s := NewSpan(style)
	for _, c := range inner {
		s.Add(c)
	}
	return s
}

func flowOf(spans ...*Span) *Flow {
	f := NewFlow()
	for _, s := range spans {
		f.Add(s)
	}
	return f
}

func textSegs(t *testing.T, f *Flow) []*widget.TextSegment {
	t.Helper()
	var out []*widget.TextSegment
	for _, s := range f.rich.Segments {
		if ts, ok := s.(*widget.TextSegment); ok {
			out = append(out, ts)
		}
	}
	return out
}

// The flattening is the whole point: Fyne has no inheritance, so a leaf under
// two wrappers has to arrive carrying both of them.
func TestNestedRunsFlattenToOneSegment(t *testing.T) {
	f := flowOf(wrap(SpanStyle{Bold: On}, wrap(SpanStyle{Italic: On}, leaf("x"))))

	segs := textSegs(t, f)
	if len(segs) != 1 {
		t.Fatalf("got %d text segments, want 1", len(segs))
	}
	if !segs[0].Style.TextStyle.Bold || !segs[0].Style.TextStyle.Italic {
		t.Errorf("got %+v, want both bold and italic", segs[0].Style.TextStyle)
	}
	if segs[0].Text != "x" {
		t.Errorf("got %q, want %q", segs[0].Text, "x")
	}
}

// A field the inner run left alone leaves the outer one's answer standing, and
// one it set to the default overrides it. That distinction is what Tri is for:
// without it the inner run says `normal` and un-bolds its parent.
func TestInnerRunOverridesOnlyWhatItSet(t *testing.T) {
	outer := SpanStyle{Bold: On, Italic: On}

	silent := wrap(outer, wrap(SpanStyle{}, leaf("a")))
	spoken := wrap(outer, wrap(SpanStyle{Italic: Off}, leaf("b")))

	f := flowOf(silent, spoken)
	segs := textSegs(t, f)
	if len(segs) != 2 {
		t.Fatalf("got %d text segments, want 2", len(segs))
	}
	if !segs[0].Style.TextStyle.Bold || !segs[0].Style.TextStyle.Italic {
		t.Errorf("silent inner run: got %+v, want both kept", segs[0].Style.TextStyle)
	}
	if !segs[1].Style.TextStyle.Bold || segs[1].Style.TextStyle.Italic {
		t.Errorf("spoken inner run: got %+v, want bold kept and italic dropped", segs[1].Style.TextStyle)
	}
}

// A decoration Fyne cannot draw is this package's own segment; everything else
// is Fyne's, which is the only one widget.RichText breaks a line inside.
func TestOnlyDecoratedRunsLeaveFynesSegment(t *testing.T) {
	f := flowOf(
		wrap(SpanStyle{Bold: On}, leaf("plain")),
		wrap(SpanStyle{Underline: true}, leaf("under")),
		wrap(SpanStyle{Strike: true}, leaf("through")),
	)

	want := []string{"*widget.TextSegment", "*fynetext.decorated", "*fynetext.decorated"}
	if len(f.rich.Segments) != len(want) {
		t.Fatalf("got %d segments, want %d", len(f.rich.Segments), len(want))
	}
	for i, seg := range f.rich.Segments {
		if got := typeName(seg); got != want[i] {
			t.Errorf("segment %d: got %s, want %s", i, got, want[i])
		}
	}
}

func typeName(v any) string {
	switch v.(type) {
	case *widget.TextSegment:
		return "*widget.TextSegment"
	case *decorated:
		return "*fynetext.decorated"
	case *widget.HyperlinkSegment:
		return "*widget.HyperlinkSegment"
	}
	return "?"
}

// A decoration cannot be turned off by a run inside one that turned it on,
// which the family's own declaration says and which is why these are bools.
func TestADecorationReachesEveryRunInside(t *testing.T) {
	f := flowOf(wrap(SpanStyle{Underline: true}, wrap(SpanStyle{Bold: On}, leaf("x"))))

	if len(f.rich.Segments) != 1 {
		t.Fatalf("got %d segments, want 1", len(f.rich.Segments))
	}
	d, ok := f.rich.Segments[0].(*decorated)
	if !ok {
		t.Fatalf("got %s, want the decorated segment", typeName(f.rich.Segments[0]))
	}
	if !d.underline || !d.style.Bold {
		t.Errorf("got underline=%t bold=%t, want both", d.underline, d.style.Bold)
	}
}

// A color reaches a segment by theme name and never as a value, so the flow
// names the ones its runs asked for -- one name per distinct color, not per
// run.
func TestOneNamePerDistinctColor(t *testing.T) {
	red := color.NRGBA{R: 255, A: 255}
	blue := color.NRGBA{B: 255, A: 255}

	f := flowOf(
		wrap(SpanStyle{Color: red}, leaf("a")),
		wrap(SpanStyle{Color: red}, leaf("b")),
		wrap(SpanStyle{Color: blue}, leaf("c")),
		leaf("d"),
	)

	segs := textSegs(t, f)
	if len(segs) != 4 {
		t.Fatalf("got %d text segments, want 4", len(segs))
	}
	if segs[0].Style.ColorName != segs[1].Style.ColorName {
		t.Errorf("one color got two names: %q and %q", segs[0].Style.ColorName, segs[1].Style.ColorName)
	}
	if segs[1].Style.ColorName == segs[2].Style.ColorName {
		t.Errorf("two colors got one name: %q", segs[2].Style.ColorName)
	}
	if segs[3].Style.ColorName != "" {
		t.Errorf("a run that named no color got %q", segs[3].Style.ColorName)
	}

	th := f.palette.themeOver(fynetest.Theme())
	if got := th.Color(segs[0].Style.ColorName, theme.VariantDark); got != color.Color(red) {
		t.Errorf("the name resolved to %v, want %v", got, red)
	}
}

// A theme answers the names this flow registered and hands everything else to
// the one it wraps -- which is what keeps a heading's own text size when one of
// its words names a color.
func TestTheThemeFallsThroughToWhatItWraps(t *testing.T) {
	f := flowOf(wrap(SpanStyle{Color: color.NRGBA{R: 255, A: 255}}, leaf("x")))

	base := fynetest.Theme()
	th := f.palette.themeOver(base)
	if got, want := th.Size(theme.SizeNameText), base.Size(theme.SizeNameText); got != want {
		t.Errorf("got %v for a size it never registered, want %v", got, want)
	}
}

// A link is Fyne's own segment, which carries its words and no style: what was
// said inside the link is lost and the link is followed when it is tapped.
func TestALinkIsFlattenedToItsWords(t *testing.T) {
	link := NewSpan(SpanStyle{})
	link.Href = "https://example.com/"
	link.Add(wrap(SpanStyle{Monospace: On}, leaf("mono "), leaf("link")))

	f := flowOf(link)
	if len(f.rich.Segments) != 1 {
		t.Fatalf("got %d segments, want 1", len(f.rich.Segments))
	}
	h, ok := f.rich.Segments[0].(*widget.HyperlinkSegment)
	if !ok {
		t.Fatalf("got %s, want a hyperlink", typeName(f.rich.Segments[0]))
	}
	if h.Text != "mono link" {
		t.Errorf("got %q, want %q", h.Text, "mono link")
	}
	if h.URL == nil || h.URL.String() != "https://example.com/" {
		t.Errorf("got %v, want the href", h.URL)
	}
}

// A reactive prop reaches a nested span and nothing else, so the setter is
// what has to rebuild the flow: the emitted code assigns to the span and knows
// nothing about the widget.
func TestSettingANestedRunRebuildsTheFlow(t *testing.T) {
	inner := leaf("before")
	f := flowOf(wrap(SpanStyle{Bold: On}, inner))

	inner.SetText("after")

	segs := textSegs(t, f)
	if len(segs) != 1 || segs[0].Text != "after" {
		t.Fatalf("got %+v, want one segment reading \"after\"", segs)
	}
}

// A span added to a flow after the tree was built still reaches it, which is
// what the ownership walk buys: the emitted code creates every node before it
// appends any of them.
func TestASpanAddedLaterIsOwnedToo(t *testing.T) {
	outer := NewSpan(SpanStyle{Bold: On})
	f := flowOf(outer)

	inner := leaf("x")
	outer.Add(inner)
	inner.SetText("y")

	segs := textSegs(t, f)
	if len(segs) != 1 || segs[0].Text != "y" {
		t.Fatalf("got %+v, want one segment reading \"y\"", segs)
	}
}

// A token says what it *is*, and this host answers in theme names: an
// application that themes its app themes its code samples with it, where a
// document naming a color would override whatever palette it found.
func TestATokenNamesAThemeColor(t *testing.T) {
	f := flowOf(
		wrap(SpanStyle{Token: "keyword"}, leaf("func")),
		wrap(SpanStyle{Token: "variable"}, leaf("x")),
	)

	segs := textSegs(t, f)
	if len(segs) != 2 {
		t.Fatalf("got %d text segments, want 2", len(segs))
	}
	if segs[0].Style.ColorName != theme.ColorNamePrimary {
		t.Errorf("keyword got %q, want %q", segs[0].Style.ColorName, theme.ColorNamePrimary)
	}
	// Four kinds are deliberately absent from the table and render in the
	// foreground, which is what a theme with six semantic colors has to say
	// about them.
	if segs[1].Style.ColorName != "" {
		t.Errorf("variable got %q, want the foreground", segs[1].Style.ColorName)
	}
}

// A token is more specific than the run it sits in, so it wins -- and it wins
// as a name, which means the color the outer run registered is not also left
// standing.
func TestATokenOverridesTheColorAroundIt(t *testing.T) {
	f := flowOf(wrap(SpanStyle{Color: color.NRGBA{R: 255, A: 255}},
		wrap(SpanStyle{Token: "string"}, leaf("x"))))

	segs := textSegs(t, f)
	if len(segs) != 1 {
		t.Fatalf("got %d text segments, want 1", len(segs))
	}
	if segs[0].Style.ColorName != theme.ColorNameSuccess {
		t.Errorf("got %q, want %q", segs[0].Style.ColorName, theme.ColorNameSuccess)
	}
}

// The builder is what a generated constructor writes, so it has to mean the
// same thing as the record: one qualified name and methods for the rest is a
// spelling, not a second vocabulary.
func TestTheBuilderMeansTheRecord(t *testing.T) {
	got := Style().WithBold().NoItalic().WithMono().WithUnderline().WithStrike().
		WithRGBA(51, 102, 153, 255).WithSize(20).WithToken("keyword")
	want := SpanStyle{
		Color:     color.NRGBA{R: 51, G: 102, B: 153, A: 255},
		Size:      20,
		Bold:      On,
		Italic:    Off,
		Monospace: On,
		Underline: true,
		Strike:    true,
		Token:     "keyword",
	}
	if got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
}
