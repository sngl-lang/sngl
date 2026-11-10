//go:build !js

package win32_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"flag"
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
// drawn in it. Run under Wine, it is recorded in testdata/snapshot_runs: a
// digest of the program generated for it, the size and this package's Go,
// which reads and encodes what the program writes.
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
	const width, height = 300, 120
	doc, err := parser.Parse("app.sngl", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	pkg, diags := checker.Check(doc, &checker.Config{IsMain: true, Platforms: codegen.CollectPlatforms(), Languages: codegen.CollectLangs(), Targets: []ir.StaticTarget{{Platform: "win32", Language: "c"}}})
	for _, d := range diags {
		if d.Severity == ir.Error {
			t.Fatalf("check: %s", d.Msg)
		}
	}
	results, err := build.Emit(pkg, build.Options{Name: "app.sngl", Dir: t.TempDir(), Lang: "c", Platform: "win32"})
	if err != nil {
		t.Fatal(err)
	}
	res := results[0]
	g := codegen.LookupPlatform("win32")
	lang := codegen.LookupLang("c")
	// What Snapshot generates and builds.
	sink := codegen.NewMemSink()
	if err := g.Generate(&codegen.Request{Pkg: res.Pkg, Lang: lang, Options: codegen.OptionsFromMap(map[string]any{"snapshot": true})}, sink); err != nil {
		t.Fatal(err)
	}
	files := sink.Files()
	if !bytes.Contains(files["main.c"], []byte("sngl_win32_snapshot(argv[1]")) {
		t.Fatal("main.c was not built for a snapshot: its entry draws nothing")
	}
	record := filepath.Join("testdata", "snapshot_runs", t.Name())
	want := snapshotDigest(t, files, width, height) + "\n"
	have, _ := os.ReadFile(record)
	switch {
	case string(have) == want && !*verify:
		return
	case !*update && !*verify && have == nil:
		t.Fatalf("%s: no record of this snapshot; rerun with -update on a host with Wine and MinGW", record)
	case !*update && !*verify:
		t.Fatalf("%s: the program changed since its snapshot ran; rerun with -update -- `go tool verify -update` rerecords every package's", record)
	}
	out, err := g.(codegen.Snapshotter).Snapshot(res.Pkg, lang, width, height)
	if err != nil {
		t.Fatal(err)
	}
	img, err := png.Decode(bytes.NewReader(out))
	if err != nil {
		t.Fatal(err)
	}
	if b := img.Bounds(); b.Dx() != width || b.Dy() != height {
		t.Fatalf("the snapshot is %d by %d, not the %d by %d asked for", b.Dx(), b.Dy(), width, height)
	}
	// The window's background, and the text and the controls drawn on it.
	colors := map[uint32]bool{}
	for y := range height {
		for x := range width {
			r, g, b, _ := img.At(x, y).RGBA()
			colors[r>>8<<16|g>>8<<8|b>>8] = true
		}
	}
	if len(colors) < 3 {
		t.Fatalf("the snapshot holds %d colors: nothing was drawn on the window", len(colors))
	}
	if *update {
		if err := os.MkdirAll(filepath.Dir(record), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(record, []byte(want), 0o644); err != nil {
			t.Fatal(err)
		}
	}
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
	for _, name := range []string{"snapshot.go", "launcher.go"} {
		b, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		field(b)
	}
	return hex.EncodeToString(h.Sum(nil))
}
