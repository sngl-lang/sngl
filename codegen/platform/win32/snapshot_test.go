//go:build !js

package win32_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"duckfam.us/sngl/codegen"
	_ "duckfam.us/sngl/codegen/lang"
	_ "duckfam.us/sngl/codegen/platform"
	"duckfam.us/sngl/internal/build"
	"duckfam.us/sngl/internal/checker"
	"duckfam.us/sngl/internal/parser"
	"duckfam.us/sngl/ir"
)

var (
	update = flag.Bool("update", false, "rerun a snapshot whose program moved, and rewrite its testdata/snapshot_runs record")
	verify = flag.Bool("verify", false, "rerun every snapshot, whatever its testdata/snapshot_runs record says")
)

// TestSnapshot is `sngl snapshot` on win32: the program built for it draws
// its window at the size asked for, which the PNG has, with the controls
// drawn in it.
func TestSnapshot(t *testing.T) {
	const src = `import ui "sngl:ui"

output {
    c {
        win32
    }
}

ui.window(title="Snapshot") {
    ui.vbox {
        ui.text(value="Hello from win32")
        ui.button(text="Press")
        ui.checkbox(label="Check me")
    }
}
`
	img := snapshot(t, src, 300, 120)
	if img == nil {
		return
	}
	// The window's background, and the text and the controls drawn on it.
	colors := map[uint32]bool{}
	b := img.Bounds()
	for y := range b.Dy() {
		for x := range b.Dx() {
			r, g, b, _ := img.At(x, y).RGBA()
			colors[r>>8<<16|g>>8<<8|b>>8] = true
		}
	}
	if len(colors) < 3 {
		t.Fatalf("the snapshot holds %d colors: nothing was drawn on the window", len(colors))
	}
}

// TestSnapshotCanvas is a canvas GDI+ paints, stretched over the window: a
// path stroked around an arcTo's corner and on down past it, and a disc.
// Wine's GdipAddPathArc ended an arc with a segment of no length, after
// which its stroke dropped the rest of the figure, so the line down the
// right was not drawn; the runtime writes an arc as cubic segments.
func TestSnapshotCanvas(t *testing.T) {
	const src = `import ui "sngl:ui"
import draw "sngl:ui/draw"

output {
    c {
        win32
    }
}

ui.window(title="Canvas") {
    draw.canvas(width=300px, height=120px, scalingMode=draw.ScalingMode.stretch) {
        draw.path(style=draw.Style{stroke=#000000, strokeWidth=4.0}) {
            draw.moveTo(x=20.0, y=110.0)
            draw.lineTo(x=20.0, y=10.0)
            draw.arcTo(cx=250.0, cy=10.0, x=250.0, y=110.0, r=30.0)
            draw.lineTo(x=250.0, y=110.0)
        }
        draw.circle(cx=150.0, cy=60.0, r=20.0, style=draw.Style{fill=#ff0000})
    }
}
`
	img := snapshot(t, src, 300, 120)
	if img == nil {
		return
	}
	// Whether some pixel within 10 of x, y is c, give or take.
	near := func(x, y int, want func(r, g, b uint32) bool) bool {
		for dy := -10; dy <= 10; dy++ {
			for dx := -10; dx <= 10; dx++ {
				r, g, b, _ := img.At(x+dx, y+dy).RGBA()
				if want(r>>8, g>>8, b>>8) {
					return true
				}
			}
		}
		return false
	}
	dark := func(r, g, b uint32) bool { return r < 80 && g < 80 && b < 80 }
	red := func(r, g, b uint32) bool { return r > 200 && g < 60 && b < 60 }
	for _, at := range []struct {
		what string
		x, y int
		want func(r, g, b uint32) bool
	}{
		{"the line up the left", 20, 60, dark},
		{"the corner's arc", 241, 19, dark},
		{"the line down the right, after the arc", 250, 90, dark},
		{"the disc", 150, 60, red},
	} {
		if !near(at.x, at.y, at.want) {
			t.Errorf("%s is not drawn near %d, %d", at.what, at.x, at.y)
		}
	}
}

// TestSnapshotDrawnImages is a drawing as an image source twice over: a
// canvasImage painting one in place, mapped from its own space onto the box
// and clipped to it, and an image showing one as a picture its shapes paint.
// The drawn disc overruns its drawing, which the clip cuts off.
func TestSnapshotDrawnImages(t *testing.T) {
	const src = `import ui "sngl:ui"
import draw "sngl:ui/draw"

output {
    c {
        win32
    }
}

ui.window(title="Drawn") {
    ui.hbox {
        draw.canvas(width=150px, height=120px) {
            draw.canvasImage(x=10.0, y=10.0, w=100.0, h=100.0) {
                draw.image(width=10.0, height=10.0) {
                    draw.rect(x=0.0, y=0.0, w=10.0, h=10.0, style=draw.Style{fill=#ff0000})
                    draw.circle(cx=10.0, cy=5.0, r=4.0, style=draw.Style{fill=#00ff00})
                }
            }
        }
        ui.image(style={width=100px, height=100px}) {
            draw.image(width=20.0, height=20.0) {
                draw.rect(x=0.0, y=0.0, w=20.0, h=20.0, style=draw.Style{fill=#0000ff})
            }
        }
    }
}
`
	img := snapshot(t, src, 300, 120)
	if img == nil {
		return
	}
	at := func(x, y int, want func(r, g, b uint32) bool) bool {
		r, g, b, _ := img.At(x, y).RGBA()
		return want(r>>8, g>>8, b>>8)
	}
	red := func(r, g, b uint32) bool { return r > 200 && g < 60 && b < 60 }
	green := func(r, g, b uint32) bool { return g > 200 && r < 60 && b < 60 }
	blue := func(r, g, b uint32) bool { return b > 200 && r < 60 && g < 60 }
	if !at(30, 30, red) || !at(100, 100, red) {
		t.Error("the drawing is not painted scaled onto the canvasImage's box")
	}
	if !at(105, 60, green) {
		t.Error("the drawing's disc is not painted inside its box")
	}
	if at(118, 60, green) {
		t.Error("the drawing's disc is painted past its box, which clips it")
	}
	blueAt := false
	for x := 150; x < 300 && !blueAt; x++ {
		blueAt = at(x, 60, blue)
	}
	if !blueAt {
		t.Error("the image does not show its drawing")
	}
}

// TestSnapshotMarkup is a flow of rich text, which the runtime wraps and
// draws itself: a paragraph too long for its width wraps onto lines of its
// own, a keyword token is drawn in the light palette's red and a link in the
// system's link color.
func TestSnapshotMarkup(t *testing.T) {
	const src = `import ui "sngl:ui"
import markup "sngl:ui/markup"

output {
    c {
        win32
    }
}

ui.window(title="Markup") {
    markup.paragraph {
        markup.token(kind=markup.Token.keyword) {
            markup.text("func")
        }
        markup.text(" leads a paragraph long enough that it cannot fit on one line of the window, so it wraps onto several, and then a ")
        markup.link(href="https://example.com") {
            markup.text("link")
        }
    }
}
`
	img := snapshot(t, src, 300, 120)
	if img == nil {
		return
	}
	b := img.Bounds()
	// The bands of rows text is drawn in, each a line.
	lines, inLine := 0, false
	keyword, link := false, false
	for y := range b.Dy() {
		inked := false
		for x := range b.Dx() {
			r, g, bl, _ := img.At(x, y).RGBA()
			r, g, bl = r>>8, g>>8, bl>>8
			if r < 120 && g < 120 && bl < 120 {
				inked = true
			}
			if r > 180 && g < 80 && bl < 80 {
				keyword = true
			}
			if bl > 150 && r < 100 && bl > g+40 {
				link = true
			}
		}
		if inked && !inLine {
			lines++
		}
		inLine = inked
	}
	if lines < 3 {
		t.Errorf("the paragraph is drawn on %d lines, not wrapped onto three or more", lines)
	}
	if !keyword {
		t.Error("the keyword is not drawn in the palette's red")
	}
	if !link {
		t.Error("the link is not drawn in the system's link color")
	}
}

// TestSnapshotContents is a component rendering a slot and a box at its top,
// which placement holds in the platform's contents box, drawn larger than it
// measures. The contents box grows by its box's flex, as gtk4's does, so the
// key reaches the window's foot: it had no flex of its own, and the box in it
// kept its measured height, as the Pokédex's grid kept a scroll's 200 px. The
// hbox, with no background of its own, shows the vbox's around the key, where
// it had painted the system's face color; and the key is painted in its own
// background, which a push button's theme paints over.
func TestSnapshotContents(t *testing.T) {
	const src = `import ui "sngl:ui"

output {
    c {
        win32
    }
}

component page ui.node {
    var more = false
    if more {
        ui.text(value="More")
    }
    ui.vbox(style={flex=1, background=#15171c}) {
        ui.hbox(style={flex=1, padding=20px}) {
            ui.button(text="7", style={flex=1, background=#f59e0b, color=#000000})
        }
    }
}

ui.window(title="Contents") {
    page()
}
`
	img := snapshot(t, src, 300, 400)
	if img == nil {
		return
	}
	is := func(x, y int, want uint32) bool {
		r, g, b, _ := img.At(x, y).RGBA()
		return r>>8<<16|g>>8<<8|b>>8 == want
	}
	for _, at := range []struct {
		what string
		x, y int
		want uint32
	}{
		{"the hbox's padding, in the vbox's background", 8, 200, 0x15171c},
		{"the hbox's padding at the window's foot", 150, 392, 0x15171c},
		{"the key, in its background, at the window's foot", 30, 370, 0xf59e0b},
	} {
		if !is(at.x, at.y, at.want) {
			r, g, b, _ := img.At(at.x, at.y).RGBA()
			t.Errorf("%s: %d, %d is #%02x%02x%02x, not #%06x", at.what, at.x, at.y, r>>8, g>>8, b>>8, at.want)
		}
	}
}

// TestSnapshotSized is a width and a height a style gives, which are the
// node's size, and a line box's alignItems. A size had been the least a node
// was, so the Pokédex's 96 px artwork grew to the picture's own 475 px as
// each arrived, and every card in the grid moved; and a box stretched each
// child across it whatever it asked. The first key is centered, the second,
// sized in a box that stretches, starts at the box's start, as CSS stretches
// only a child with no size of its own.
func TestSnapshotSized(t *testing.T) {
	const src = `import ui "sngl:ui"

output {
    c {
        win32
    }
}

ui.window(title="Sized") {
    ui.vbox(style={flex=1, gap=0, background=#15171c}) {
        ui.vbox(style={alignItems=center}) {
            ui.button(text="", style={width=100px, height=40px, background=#f59e0b})
        }
        ui.vbox {
            ui.button(text="", style={width=100px, height=40px, background=#2c2f36})
        }
    }
}
`
	img := snapshot(t, src, 300, 200)
	if img == nil {
		return
	}
	is := func(x, y int, want uint32) bool {
		r, g, b, _ := img.At(x, y).RGBA()
		return r>>8<<16|g>>8<<8|b>>8 == want
	}
	for _, at := range []struct {
		what string
		x, y int
		want uint32
	}{
		{"the centered key", 150, 20, 0xf59e0b},
		{"left of the centered key", 40, 20, 0x15171c},
		{"right of the centered key", 260, 20, 0x15171c},
		{"the key at its box's start", 50, 60, 0x2c2f36},
		{"right of the key at its box's start", 200, 60, 0x15171c},
		{"under both keys, which are 40 px tall", 150, 150, 0x15171c},
	} {
		if !is(at.x, at.y, at.want) {
			r, g, b, _ := img.At(at.x, at.y).RGBA()
			t.Errorf("%s: %d, %d is #%02x%02x%02x, not #%06x", at.what, at.x, at.y, r>>8, g>>8, b>>8, at.want)
		}
	}
}

// snapshot is src's window as `sngl snapshot` draws it, width by height,
// under Wine, built in C and in Go, which must draw it alike, pixel for
// pixel; nil where its record says the program has not changed since it
// last ran. A run is recorded in testdata/snapshot_runs, by the test's name:
// a digest of the programs generated for it, the Go wrappers and the
// runtime they include, the size and this package's Go, which reads and
// encodes what the program writes.
func snapshot(t *testing.T, src string, width, height int) image.Image {
	t.Helper()
	doc, err := parser.Parse("app.sngl", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	g := codegen.LookupPlatform("win32")
	type built struct {
		lang codegen.LangTranslator
		pkg  *ir.Package
	}
	var langs []built
	files := map[string][]byte{}
	for _, id := range []string{"c", "go"} {
		pkg, diags := checker.Check(doc, &checker.Config{IsMain: true, Platforms: codegen.CollectPlatforms(), Languages: codegen.CollectLangs(), Targets: []ir.StaticTarget{{Platform: "win32", Language: id}}})
		for _, d := range diags {
			if d.Severity == ir.Error {
				t.Fatalf("check: %s", d.Msg)
			}
		}
		results, err := build.Emit(pkg, build.Options{Name: "app.sngl", Dir: t.TempDir(), Lang: id, Platform: "win32"})
		if err != nil {
			t.Fatal(err)
		}
		lang := codegen.LookupLang(id)
		// What Snapshot generates and builds.
		sink := codegen.NewMemSink()
		if err := g.Generate(&codegen.Request{Pkg: results[0].Pkg, Lang: lang, Options: codegen.OptionsFromMap(map[string]any{"snapshot": true})}, sink); err != nil {
			t.Fatal(err)
		}
		entry, call := "main.c", "sngl_win32_snapshot(argv[1]"
		if id == "go" {
			entry, call = "main.go", "snglWin32Snapshot(os.Args[1]"
		}
		if !bytes.Contains(sink.Files()[entry], []byte(call)) {
			t.Fatalf("%s was not built for a snapshot: its entry draws nothing", entry)
		}
		for name, data := range sink.Files() {
			files[id+"/"+name] = data
		}
		langs = append(langs, built{lang, results[0].Pkg})
	}
	record := filepath.Join("testdata", "snapshot_runs", t.Name())
	want := snapshotDigest(t, files, width, height) + "\n"
	have, _ := os.ReadFile(record)
	switch {
	case string(have) == want && !*verify:
		return nil
	case !*update && !*verify && have == nil:
		t.Fatalf("%s: no record of this snapshot; rerun with -update on a host with Wine and MinGW", record)
	case !*update && !*verify:
		t.Fatalf("%s: the program changed since its snapshot ran; rerun with -update -- `go tool verify -update` rerecords every package's", record)
	}
	var drawn [][]byte
	for _, b := range langs {
		out, err := g.(codegen.Snapshotter).Snapshot(b.pkg, b.lang, width, height)
		if err != nil {
			t.Fatalf("%s: %v", b.lang.LanguageIdentifier(), err)
		}
		drawn = append(drawn, out)
	}
	img, err := png.Decode(bytes.NewReader(drawn[0]))
	if err != nil {
		t.Fatal(err)
	}
	if b := img.Bounds(); b.Dx() != width || b.Dy() != height {
		t.Fatalf("the snapshot is %d by %d, not the %d by %d asked for", b.Dx(), b.Dy(), width, height)
	}
	goImg, err := png.Decode(bytes.NewReader(drawn[1]))
	if err != nil {
		t.Fatal(err)
	}
	if !samePixels(img, goImg) {
		t.Error("the program built in Go draws its window otherwise than built in C")
	}
	if *update && !t.Failed() {
		t.Cleanup(func() {
			if t.Failed() {
				return
			}
			if err := os.MkdirAll(filepath.Dir(record), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(record, []byte(want), 0o644); err != nil {
				t.Fatal(err)
			}
		})
	}
	return img
}

// samePixels reports whether a and b are one picture.
func samePixels(a, b image.Image) bool {
	if a.Bounds() != b.Bounds() {
		return false
	}
	r := a.Bounds()
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			if a.At(x, y) != b.At(x, y) {
				return false
			}
		}
	}
	return true
}

func snapshotDigest(t *testing.T, files map[string][]byte, width, height int) string {
	t.Helper()
	h := sha256.New()
	field := func(b []byte) {
		h.Write([]byte{byte(len(b) >> 24), byte(len(b) >> 16), byte(len(b) >> 8), byte(len(b))})
		h.Write(b)
	}
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		field([]byte(name))
		field(files[name])
	}
	field([]byte{byte(width >> 8), byte(width), byte(height >> 8), byte(height)})
	// What the Go build compiles beside what it was generated: the wrappers
	// and the runtime they include.
	for _, dir := range []string{"../../../lib/platform/win32/cgo", "../../../lib/platform/win32/rt"} {
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range entries {
			b, err := os.ReadFile(filepath.Join(dir, e.Name()))
			if err != nil {
				t.Fatal(err)
			}
			field([]byte(e.Name()))
			field(b)
		}
	}
	for _, name := range []string{"snapshot.go", "launcher.go"} {
		b, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		field(b)
	}
	return hex.EncodeToString(h.Sum(nil))
}
