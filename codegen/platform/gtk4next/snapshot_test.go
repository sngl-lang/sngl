//go:build !js

package gtk4next_test

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

// TestSnapshot is `sngl snapshot` on gtk4next: the program built for it
// draws its window at the size asked for, which the PNG has, with the
// controls drawn in it.
func TestSnapshot(t *testing.T) {
	const src = `import ui "sngl:ui"

output {
    c {
        gtk4next
    }
}

ui.window(title="Snapshot") {
    ui.vbox {
        ui.text(value="Hello from GTK")
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

// snapshot is src's window as `sngl snapshot` draws it, width by height,
// built in C and in Go, which must draw it alike, pixel for pixel; nil where its record says the program has not changed since it
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
	g := codegen.LookupPlatform("gtk4next")
	type built struct {
		lang codegen.LangTranslator
		pkg  *ir.Package
	}
	var langs []built
	files := map[string][]byte{}
	for _, id := range []string{"c", "go"} {
		pkg, diags := checker.Check(doc, &checker.Config{IsMain: true, Platforms: codegen.CollectPlatforms(), Languages: codegen.CollectLangs(), Targets: []ir.StaticTarget{{Platform: "gtk4next", Language: id}}})
		for _, d := range diags {
			if d.Severity == ir.Error {
				t.Fatalf("check: %s", d.Msg)
			}
		}
		results, err := build.Emit(pkg, build.Options{Name: "app.sngl", Dir: t.TempDir(), Lang: id, Platform: "gtk4next"})
		if err != nil {
			t.Fatal(err)
		}
		lang := codegen.LookupLang(id)
		// What Snapshot generates and builds.
		sink := codegen.NewMemSink()
		if err := g.Generate(&codegen.Request{Pkg: results[0].Pkg, Lang: lang, Options: codegen.OptionsFromMap(map[string]any{"snapshot": true})}, sink); err != nil {
			t.Fatal(err)
		}
		entry, call := "main.c", "sngl_gtk_capture_file(argv[1]"
		if id == "go" {
			entry, call = "main.go", "SnglGtkCaptureFile(os.Args[1]"
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
		t.Fatalf("%s: no record of this snapshot; rerun with -update on a host with GTK 4 and a display", record)
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
	for _, dir := range []string{"../../../lib/platform/gtk4next/cgo", "../../../lib/platform/gtk4next/rt"} {
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
	for _, name := range []string{"snapshot.go"} {
		b, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		field(b)
	}
	return hex.EncodeToString(h.Sum(nil))
}
