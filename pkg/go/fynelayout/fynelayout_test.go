package fynelayout_test

import (
	"image/color"
	"os"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	fynetest "fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"duckfam.us/sngl/pkg/go/fynelayout"
	"duckfam.us/sngl/pkg/go/fynetheme"
	"duckfam.us/sngl/pkg/go/snglcolor"
)

// A canvas object refreshes itself when its minimum changes, and that reaches
// for the running app. There is no window in these tests, only geometry.
func TestMain(m *testing.M) {
	fynetest.NewApp()
	os.Exit(m.Run())
}

// box is a child with a known minimum, which is all this layout reads.
func box(w, h float32) fyne.CanvasObject {
	r := canvas.NewRectangle(nil)
	r.SetMinSize(fyne.NewSize(w, h))
	return r
}

func grown(w, h, grow float32) *fynelayout.Box {
	b := fynelayout.NewBox(box(w, h))
	b.SetLook(fynelayout.Look{Grow: grow})
	return b
}

func TestGrowsSplitTheSurplus(t *testing.T) {
	a, b := grown(10, 10, 1), grown(10, 10, 3)
	l := &fynelayout.Weighted{Horizontal: true, Gap: 10}
	l.Layout([]fyne.CanvasObject{a, b}, fyne.NewSize(410, 50))

	if got := a.Size().Width; got != 100 {
		t.Errorf("grow 1 of 4 got width %v, want 100", got)
	}
	if got := b.Size().Width; got != 300 {
		t.Errorf("grow 3 of 4 got width %v, want 300", got)
	}
	if got := b.Position().X; got != 110 {
		t.Errorf("second child starts at %v, want 110", got)
	}
	// Stretch, the zero Cross, fills the box across.
	if got := a.Size().Height; got != 50 {
		t.Errorf("cross axis got %v, want 50", got)
	}
}

// A child that does not grow keeps its minimum, and only what is left over is
// shared -- which is what makes a fixed header above a flexing body work.
func TestUngrownChildrenKeepTheirMinimum(t *testing.T) {
	fixed, grows := box(10, 30), grown(10, 10, 1)
	l := &fynelayout.Weighted{Gap: 10}
	l.Layout([]fyne.CanvasObject{fixed, grows}, fyne.NewSize(100, 200))

	if got := fixed.Size().Height; got != 30 {
		t.Errorf("fixed child got height %v, want its minimum 30", got)
	}
	if got := grows.Size().Height; got != 160 {
		t.Errorf("growing child got height %v, want the remaining 160", got)
	}
}

func TestCrossAndJustifyPlaceFixedChildren(t *testing.T) {
	a, b := box(10, 10), box(10, 20)
	l := &fynelayout.Weighted{Horizontal: true, Gap: 10, Cross: fynelayout.CrossCenter, Justify: fynelayout.JustifyEnd}
	l.Layout([]fyne.CanvasObject{a, b}, fyne.NewSize(100, 40))

	if got := a.Position(); got != fyne.NewPos(70, 15) {
		t.Errorf("first child at %v, want (70, 15)", got)
	}
	if got := b.Position(); got != fyne.NewPos(90, 10) {
		t.Errorf("second child at %v, want (90, 10)", got)
	}
	if got := a.Size(); got != fyne.NewSize(10, 10) {
		t.Errorf("a centered child keeps its own size, got %v", got)
	}
}

func TestMinSizeSumsTheChildren(t *testing.T) {
	l := &fynelayout.Weighted{Gap: 6}
	got := l.MinSize([]fyne.CanvasObject{box(30, 10), grown(50, 20, 1)})
	if got != fyne.NewSize(50, 36) {
		t.Errorf("min size %v, want 50x36", got)
	}
}

// A growing child is never given less than its own minimum, as CSS clamps a
// flex item, and what that costs the others is shared among them.
func TestAGrowingChildKeepsItsMinimum(t *testing.T) {
	tall, short := grown(10, 100, 1), grown(10, 10, 1)
	l := &fynelayout.Weighted{Gap: 4}
	l.Layout([]fyne.CanvasObject{tall, short}, fyne.NewSize(50, 114))
	if got := tall.Size().Height; got != 100 {
		t.Errorf("the tall child got %v, want its minimum 100", got)
	}
	if got := short.Size().Height; got != 10 {
		t.Errorf("the short child got %v, want the 10 left", got)
	}
}

// Fyne's own boxes give a spacer the free space, and ui.spacer is one.
func TestASpacerGrows(t *testing.T) {
	a, b := box(10, 10), box(10, 10)
	l := &fynelayout.Weighted{Gap: 4}
	l.Layout([]fyne.CanvasObject{a, layout.NewSpacer(), b}, fyne.NewSize(50, 200))
	if got := b.Position().Y; got != 190 {
		t.Errorf("the child after a spacer is at %v, want the bottom, 190", got)
	}
}

// The margin lies outside the border, the padding inside it, as CSS's
// border-box lays them out.
func TestBoxInsetsItsContent(t *testing.T) {
	content := box(20, 10)
	b := fynelayout.NewBox(content)
	b.SetLook(fynelayout.Look{
		MarginTop: 5, MarginRight: 5, MarginBottom: 5, MarginLeft: 5,
		PaddingTop: 3, PaddingRight: 3, PaddingBottom: 3, PaddingLeft: 3,
		StrokeWidth: 2,
	})
	if got := b.MinSize(); got != fyne.NewSize(40, 30) {
		t.Errorf("min size %v, want 40x30", got)
	}
	b.Resize(fyne.NewSize(100, 50))
	r := fynetest.WidgetRenderer(b)
	r.Layout(fyne.NewSize(100, 50))
	themed := r.Objects()[1]
	if got := themed.Position(); got != fyne.NewPos(10, 10) {
		t.Errorf("content at %v, want (10, 10)", got)
	}
	if got := themed.Size(); got != fyne.NewSize(80, 30) {
		t.Errorf("content size %v, want 80x30", got)
	}
}

func TestBoxSizeBounds(t *testing.T) {
	b := fynelayout.NewBox(box(20, 10))
	b.SetLook(fynelayout.Look{Width: 60, MaxHeight: 4})
	if got := b.MinSize(); got != fyne.NewSize(60, 4) {
		t.Errorf("min size %v, want 60x4", got)
	}
}

// A width is the box's whatever its parent stretches it to.
func TestBoxKeepsItsWidthWhenStretched(t *testing.T) {
	b := fynelayout.NewBox(box(20, 10))
	b.SetLook(fynelayout.Look{Width: 50})
	r := fynetest.WidgetRenderer(b)
	r.Layout(fyne.NewSize(300, 10))
	if got := r.Objects()[1].Size().Width; got != 50 {
		t.Errorf("content width %v, want 50", got)
	}
}

// A Look that leaves a field alone inherits it from whatever the box is under.
func TestBoxInheritsWhatItsLookLeavesAlone(t *testing.T) {
	label := widget.NewLabel("x")
	b := fynelayout.NewBox(label)
	b.SetLook(fynelayout.Look{PaddingTop: 4})
	red := color.NRGBA{R: 255, A: 255}
	outer := container.NewThemeOverride(b, fynetheme.Theme{Theme: theme.DefaultTheme(), Foreground: red})
	w := fynetest.NewWindow(outer)
	defer w.Close()
	b.Refresh()
	if got := theme.CurrentForWidget(label).Color(theme.ColorNameForeground, theme.VariantLight); got != red {
		t.Errorf("the boxed label's foreground is %v, want the enclosing %v", got, red)
	}
}

// Back to no alignment is back to the widget's own.
func TestBoxRestoresTheWidgetsAlignment(t *testing.T) {
	btn := widget.NewButton("x", nil)
	b := fynelayout.NewBox(btn)
	b.SetLook(fynelayout.Look{TextAlign: fynelayout.TextAlignLeading})
	b.SetLook(fynelayout.Look{PaddingTop: 1})
	if btn.Alignment != widget.ButtonAlignCenter {
		t.Errorf("the button's alignment is %v, want its own center", btn.Alignment)
	}
}

// An override re-measures its whole subtree on every refresh, so a box asking
// for no theme puts its widget under none.
func TestBoxWithNoThemeHoldsNoOverride(t *testing.T) {
	content := box(20, 10)
	b := fynelayout.NewBox(content)
	b.SetLook(fynelayout.Look{PaddingTop: 2, Grow: 1})
	if got := fynetest.WidgetRenderer(b).Objects()[1]; got != content {
		t.Errorf("the box shows a %T, want its widget", got)
	}
}

// A share that changes while the program runs lays the parent out again.
func TestAChangedGrowRelaysTheParent(t *testing.T) {
	a := grown(10, 10, 0)
	c := fynelayout.NewVBox(a, box(10, 10))
	w := fynetest.NewWindow(c)
	defer w.Close()
	w.Resize(fyne.NewSize(100, 300))
	a.SetLook(fynelayout.Look{Grow: 1})
	if got := a.Size().Height; got <= 10 {
		t.Errorf("the child that now grows is %v high, want the free space", got)
	}
}

// Nothing -- an instance whose body renders no node -- takes no room and no
// gap in either kind of box.
func TestNothingTakesNoRoom(t *testing.T) {
	a, b := box(10, 10), box(10, 10)
	l := &fynelayout.Weighted{Gap: 6}
	objects := []fyne.CanvasObject{a, fynelayout.Nothing(), b}
	l.Layout(objects, fyne.NewSize(100, 200))
	if got := b.Position().Y; got != 16 {
		t.Errorf("child after Nothing starts at %v, want 10 + one gap 6", got)
	}
	if got := l.MinSize(objects).Height; got != 26 {
		t.Errorf("min height %v, want 10 + 6 + 10", got)
	}

	c := container.NewVBox(box(10, 10), fynelayout.Nothing(), box(10, 10))
	if got, want := c.MinSize().Height, 20+theme.Padding(); got != want {
		t.Errorf("Fyne's box with Nothing in it is %v tall, want %v", got, want)
	}
}

// counted is a widget that counts its refreshes.
type counted struct {
	widget.Label
	refreshes int
}

func newCounted() *counted {
	c := &counted{}
	c.ExtendBaseWidget(c)
	return c
}

func (c *counted) Refresh() {
	c.refreshes++
	c.Label.Refresh()
}

// A render slot inserting a row lays its box out again, and refreshes none of
// the rows already there: a refresh reaches every child, a theme override
// refreshed is a new theme scope for Fyne's text cache, and a table filled
// row by row measured all its text again for each row -- seconds for a
// table of fifteen.
func TestInsertingRefreshesNoOtherChild(t *testing.T) {
	old := newCounted()
	c := fynelayout.NewVBox(old)
	c.Resize(fyne.NewSize(100, 200))
	anchor := fynelayout.SlotAnchor(c, nil)
	before := old.refreshes
	added := box(10, 30)
	fynelayout.InsertBefore(c, anchor, added)
	if old.refreshes != before {
		t.Errorf("inserting a row refreshed the row already there %d times", old.refreshes-before)
	}
	if got, want := added.Position().Y, old.MinSize().Height+theme.Padding(); got != want {
		t.Errorf("the inserted row is at %v, want laid out after the first, at %v", got, want)
	}
}

// A row a render slot inserts under a Box's theme takes that theme by the
// next draw, with no refresh of the Box or of the rows already there. Before,
// it kept the one it had until the Box refreshed, as Fyne's ThemeOverride does
// for what is added beneath it.
func TestAnInsertedRowTakesItsBoxsTheme(t *testing.T) {
	old := newCounted()
	c := fynelayout.NewVBox(old)
	anchor := fynelayout.SlotAnchor(c, nil)
	b := fynelayout.NewBox(c)
	red := color.NRGBA{R: 255, A: 255}
	b.SetLook(fynelayout.Look{TextColor: snglcolor.Color{R: 255, A: 255}})
	w := fynetest.NewWindow(b)
	defer w.Close()
	before := old.refreshes

	added := widget.NewLabel("added")
	fynelayout.InsertBefore(c, anchor, added)
	w.Canvas().Capture()
	if got := theme.CurrentForWidget(added).Color(theme.ColorNameForeground, theme.VariantLight); got != red {
		t.Errorf("the inserted label's foreground is %v, want its box's %v", got, red)
	}
	boxed := fynelayout.NewBox(widget.NewLabel("boxed"))
	boxed.SetLook(fynelayout.Look{PaddingTop: 2, Bold: true})
	fynelayout.InsertBefore(c, anchor, boxed)
	w.Canvas().Capture()
	if got := theme.CurrentForWidget(boxed.Content).Color(theme.ColorNameForeground, theme.VariantLight); got != red {
		t.Errorf("an inserted box's label's foreground is %v, want the enclosing %v", got, red)
	}
	if old.refreshes != before {
		t.Errorf("inserting refreshed the row already there %d times", old.refreshes-before)
	}
}

// A build gives a box its Look before it adds the box's rows, and fills a
// render slot before the window shows: every row takes the theme, and so does
// one the slot inserts after.
func TestRowsAddedBeforeTheBoxShowsTakeItsTheme(t *testing.T) {
	c := fynelayout.NewVBox()
	b := fynelayout.NewBox(c)
	b.SetLook(fynelayout.Look{TextColor: snglcolor.Color{R: 255, A: 255}})
	static := widget.NewLabel("static")
	c.Add(static)
	anchor := fynelayout.SlotAnchor(c, nil)
	first := widget.NewLabel("first")
	fynelayout.InsertBefore(c, anchor, first)
	w := fynetest.NewWindow(b)
	defer w.Close()
	later := widget.NewLabel("later")
	fynelayout.InsertBefore(c, anchor, later)
	w.Canvas().Capture()
	red := color.NRGBA{R: 255, A: 255}
	for _, l := range []*widget.Label{static, first, later} {
		if got := theme.CurrentForWidget(l).Color(theme.ColorNameForeground, theme.VariantLight); got != red {
			t.Errorf("%s's foreground is %v, want its box's %v", l.Text, got, red)
		}
	}
}

// A row inserted where no Box themes it is under no override.
func TestARowInsertedUnthemedTakesNoOverride(t *testing.T) {
	c := fynelayout.NewVBox(widget.NewLabel("old"))
	anchor := fynelayout.SlotAnchor(c, nil)
	w := fynetest.NewWindow(c)
	defer w.Close()
	added := widget.NewLabel("added")
	fynelayout.InsertBefore(c, anchor, added)
	w.Canvas().Capture()
	if got := theme.CurrentForWidget(added); got != theme.Current() {
		t.Errorf("the inserted label is under %T, want the app's theme", got)
	}
}

// A grid given another column count lays its children out that many to a row.
func TestSetGridColumnsLaysOutThatManyToARow(t *testing.T) {
	a, b, c := box(10, 10), box(10, 10), box(10, 10)
	g := container.NewGridWithColumns(2, a, b, c)
	g.Resize(fyne.NewSize(300, 100))
	if c.Position().Y == a.Position().Y {
		t.Fatalf("with two columns the third child should start a row: %v %v", a.Position(), c.Position())
	}
	fynelayout.SetGridColumns(g, 3)
	if c.Position().Y != a.Position().Y {
		t.Errorf("with three columns the third child should share the first's row: %v %v", a.Position(), c.Position())
	}
}
