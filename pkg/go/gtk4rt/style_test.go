//go:build !js

package gtk4rt

import (
	"bytes"
	goimage "image"
	"image/color"
	"image/png"
	"math"
	"os"
	"strings"
	"testing"

	"duckfam.us/sngl/internal/testutil"
	"duckfam.us/sngl/pkg/go/snglcolor"
)

// A style is the same CSS whichever order it was written in, a side's own
// length wins over its axis's and an axis's over the whole box's, and a field
// at its zero writes nothing.
func TestStyleCSS(t *testing.T) {
	for _, tc := range []struct {
		name string
		s    Style
		want string
	}{
		{"unset", Style{}, ""},
		{"lengths only a box reads", Style{Width: 10, Height: 10, Gap: 4}, ""},
		{"padding", Style{Padding: 8}, "padding: 8px 8px 8px 8px;"},
		{"axes over the box, sides over axes", Style{Padding: 1, PaddingX: 2, PaddingTop: 3}, "padding: 3px 2px 1px 2px;"},
		{"margin", Style{MarginY: 5}, "margin: 5px 0px 5px 0px;"},
		{"face", Style{FontSize: 28, FontWeight: "bold", FontStyle: "italic"}, "font-size: 28px; font-weight: bold; font-style: italic;"},
		{"normal is the theme's", Style{FontWeight: "normal", FontStyle: "normal"}, ""},
		{"colors", Style{Color: snglcolor.Color{R: 255, A: 255}, Background: snglcolor.Color{G: 255, A: 128}},
			"color: #ff0000; background: none; background-color: rgba(0,255,0,0.5019607843137255);"},
		{"radius", Style{BorderRadius: 12.5}, "border-radius: 12.5px;"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.s.CSS(); got != tc.want {
				t.Errorf("CSS() = %q, want %q", got, tc.want)
			}
		})
	}
}

// What a style asks of a widget's size and of a box's spacing is what GTK
// lays out: a label is at least its width and height, an image is exactly
// its, however large its picture, and a box's gap stands between its
// children. The window is the size the compositor gives it, so each is
// measured against the label's height rather than in pixels.
func TestSetStyleSizesAndSpaces(t *testing.T) {
	if r := testutil.GUIRenderSkipReason(); r != "" {
		t.Skip(r)
	}
	t.Chdir(t.TempDir())
	blue := goimage.NewNRGBA(goimage.Rect(0, 0, 400, 400))
	for i := range blue.Pix {
		blue.Pix[i] = []byte{0, 0, 255, 255}[i%4]
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, blue); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("blue.png", buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}

	red := snglcolor.Color{R: 255, A: 255}
	green := snglcolor.Color{G: 255, A: 255}
	build := func(app Handle) Handle {
		col := BoxNew(OrientationVertical, 0)
		col2 := BoxNew(OrientationHorizontal, 0)
		SetStyle(col2, Style{Gap: 100})
		for range 2 {
			g := LabelNew("")
			SetStyle(g, Style{Width: 40, Height: 40, Background: green})
			BoxAppend(col2, g)
		}
		label := LabelNew("")
		SetStyle(label, Style{Width: 200, Height: 100, Background: red})
		frame := ImageFrameNew()
		src := ImageSourceNew()
		ImageSourceSetURL(src, "blue.png")
		BoxAppend(frame, src)
		SetStyle(frame, Style{Width: 50, Height: 50})
		for _, w := range []Handle{label, frame, col2} {
			row := BoxNew(OrientationHorizontal, 0)
			BoxAppend(row, w)
			BoxAppend(col, row)
		}
		win := ApplicationWindowNew(app)
		WindowSetChild(win, col)
		return win
	}
	data, err := SnapshotModelBytes(build, 1280, 720)
	if err != nil {
		if strings.Contains(err.Error(), "sngl_snapshot rc=") {
			t.Skipf("headless render backend unavailable: %v", err)
		}
		t.Fatalf("SnapshotModelBytes: %v", err)
	}
	shot, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	is := func(r, g, b uint8) func(color.NRGBA) bool {
		near := func(a, b uint8) bool { return math.Abs(float64(a)-float64(b)) < 40 }
		return func(c color.NRGBA) bool { return near(c.R, r) && near(c.G, g) && near(c.B, b) }
	}
	redBox := extent(shot, is(255, 0, 0))
	blueBox := extent(shot, is(0, 0, 255))
	if redBox.Empty() || blueBox.Empty() {
		t.Fatalf("red %v, blue %v: a styled widget drew nothing", redBox, blueBox)
	}
	unit := float64(redBox.Dy()) / 100
	about := func(what string, got int, want float64) {
		if math.Abs(float64(got)-want*unit) > 3*unit+2 {
			t.Errorf("%s is %d pixels, want about %.0f (%.0f at the label's scale)", what, got, want*unit, want)
		}
	}
	about("the label's width", redBox.Dx(), 200)
	about("the image's width", blueBox.Dx(), 50)
	about("the image's height", blueBox.Dy(), 50)
	gap := gapAlong(shot, is(0, 255, 0))
	about("the gap between the boxes", gap, 100)
}

// extent is the smallest rectangle holding every pixel that is.
func extent(img goimage.Image, is func(color.NRGBA) bool) goimage.Rectangle {
	var r goimage.Rectangle
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			if is(color.NRGBAModel.Convert(img.At(x, y)).(color.NRGBA)) {
				r = r.Union(goimage.Rect(x, y, x+1, y+1))
			}
		}
	}
	return r
}

// gapAlong is the widest run of other pixels between two that are, along the
// middle row of those that are.
func gapAlong(img goimage.Image, is func(color.NRGBA) bool) int {
	r := extent(img, is)
	y := (r.Min.Y + r.Max.Y) / 2
	widest, run := 0, -1
	for x := r.Min.X; x < r.Max.X; x++ {
		if is(color.NRGBAModel.Convert(img.At(x, y)).(color.NRGBA)) {
			widest, run = max(widest, run), 0
			continue
		}
		if run >= 0 {
			run++
		}
	}
	return widest
}

// A label's text starts at its left, as html's does, unless its textAlign says
// otherwise; a box's alignItems sits each child at its own width across the
// box rather than stretching it; and a label's width is its width, its words
// wrapping inside it, where a size request let a long line widen it.
func TestSetStyleAligns(t *testing.T) {
	if r := testutil.GUIRenderSkipReason(); r != "" {
		t.Skip(r)
	}
	red := snglcolor.Color{R: 255, A: 255}
	green := snglcolor.Color{G: 255, A: 255}
	blue := snglcolor.Color{B: 255, A: 255}
	yellow := snglcolor.Color{R: 255, G: 255, A: 255}
	black := snglcolor.Color{A: 255}
	// ui.text's label starts at the left, as gtk4.sngl builds it.
	label := func(text string) Handle {
		l := LabelNew(text)
		LabelSetXAlign(l, 0)
		return l
	}
	build := func(app Handle) Handle {
		col := BoxNew(OrientationVertical, 0)
		left := label("left")
		SetStyle(left, Style{Background: red, Color: black})
		centered := label("centered")
		SetStyle(centered, Style{Background: green, Color: black, TextAlign: "center"})
		inner := BoxNew(OrientationVertical, 0)
		SetStyle(inner, Style{AlignItems: "center"})
		narrow := label("narrow")
		SetStyle(narrow, Style{Background: blue, Color: black})
		BoxAppend(inner, narrow)
		row := BoxNew(OrientationHorizontal, 0)
		wrapped := label("a line much longer than the width it is given")
		SetStyle(wrapped, Style{Width: 100, Background: yellow, Color: black})
		BoxAppend(row, wrapped)
		for _, w := range []Handle{left, centered, inner, row} {
			BoxAppend(col, w)
		}
		win := ApplicationWindowNew(app)
		WindowSetChild(win, col)
		return win
	}
	data, err := SnapshotModelBytes(build, 1280, 720)
	if err != nil {
		if strings.Contains(err.Error(), "sngl_snapshot rc=") {
			t.Skipf("headless render backend unavailable: %v", err)
		}
		t.Fatalf("SnapshotModelBytes: %v", err)
	}
	shot, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	is := func(r, g, b uint8) func(color.NRGBA) bool {
		near := func(a, b uint8) bool { return math.Abs(float64(a)-float64(b)) < 40 }
		return func(c color.NRGBA) bool { return near(c.R, r) && near(c.G, g) && near(c.B, b) }
	}
	dark := func(c color.NRGBA) bool { return c.R < 100 && c.G < 100 && c.B < 100 }
	// The text drawn over a background is the dark pixels inside its extent.
	text := func(bg goimage.Rectangle) goimage.Rectangle {
		sub, ok := shot.(interface {
			SubImage(goimage.Rectangle) goimage.Image
		})
		if !ok {
			t.Fatalf("%T has no SubImage", shot)
		}
		return extent(sub.SubImage(bg), dark)
	}
	redBox, greenBox := extent(shot, is(255, 0, 0)), extent(shot, is(0, 255, 0))
	blueBox, yellowBox := extent(shot, is(0, 0, 255)), extent(shot, is(255, 255, 0))
	for name, r := range map[string]goimage.Rectangle{"red": redBox, "green": greenBox, "blue": blueBox, "yellow": yellowBox} {
		if r.Empty() {
			t.Fatalf("the %s label drew nothing", name)
		}
	}
	unit := float64(shot.Bounds().Dx()) / 1280
	if l := text(redBox); l.Min.X-redBox.Min.X > redBox.Dx()/10 {
		t.Errorf("a label's text starts %d pixels into its %d: it is not at the left", l.Min.X-redBox.Min.X, redBox.Dx())
	}
	if c := text(greenBox); math.Abs(float64(c.Min.X+c.Max.X)/2-float64(greenBox.Min.X+greenBox.Max.X)/2) > 4*unit+2 {
		t.Errorf("a centered label's text spans %v of its %v: it is not centered", c, greenBox)
	}
	if blueBox.Dx() > redBox.Dx()/2 {
		t.Errorf("a label in a centering box is %d pixels of %d: it stretched", blueBox.Dx(), redBox.Dx())
	}
	if mid := (blueBox.Min.X + blueBox.Max.X) / 2; math.Abs(float64(mid-(redBox.Min.X+redBox.Max.X)/2)) > 4*unit+2 {
		t.Errorf("a label in a centering box is at %v of %v: it is not centered", blueBox, redBox)
	}
	if math.Abs(float64(yellowBox.Dx())-100*unit) > 3*unit+2 {
		t.Errorf("a label 100 wide is %d pixels, want about %.0f", yellowBox.Dx(), 100*unit)
	}
	if yellowBox.Dy() < 2*redBox.Dy() {
		t.Errorf("a long label 100 wide is %d pixels high to a line's %d: it did not wrap", yellowBox.Dy(), redBox.Dy())
	}
}

// A style that keeps changing keeps one rule, not one per value it passed
// through: SetStyle kept a class per distinct style and dropped none, so an
// animated color grew the stylesheet by a rule a frame, each reloading the
// whole of it. Two widgets alike share one, and a destroyed widget lets its
// go.
func TestSetStyleDropsRulesNoWidgetHolds(t *testing.T) {
	if r := testutil.GUIRenderSkipReason(); r != "" {
		t.Skip(r)
	}
	var during, twins, after int
	build := func(app Handle) Handle {
		before := len(rules)
		moving := LabelNew("moving")
		Retain(moving)
		for i := range 100 {
			SetStyle(moving, Style{Color: snglcolor.Color{R: i, A: 255}})
		}
		during = len(rules) - before
		a, b := LabelNew("a"), LabelNew("b")
		Retain(a)
		Retain(b)
		SetStyle(a, Style{Padding: 3})
		SetStyle(b, Style{Padding: 3})
		twins = len(rules) - before
		Release(moving)
		Release(a)
		Release(b)
		after = len(rules) - before
		return ApplicationWindowNew(app)
	}
	if _, err := SnapshotModelBytes(build, 64, 64); err != nil {
		if strings.Contains(err.Error(), "sngl_snapshot rc=") {
			t.Skipf("headless render backend unavailable: %v", err)
		}
		t.Fatalf("SnapshotModelBytes: %v", err)
	}
	if during != 1 {
		t.Errorf("a style changed 100 times holds %d rules, want 1", during)
	}
	if twins != 2 {
		t.Errorf("two widgets alike beside it hold %d rules in all, want 2", twins)
	}
	if after != 0 {
		t.Errorf("%d rules outlived the widgets holding them", after)
	}
}
