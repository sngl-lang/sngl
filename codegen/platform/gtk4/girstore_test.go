package gtk4

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/codegen/platform/gtk4/gir"
	"git.duckfam.us/jonathan/sngl/internal/gencache"
)

// countParses swaps parseGIR for one that counts, for the length of the test.
func countParses(t *testing.T) *int {
	t.Helper()
	n := 0
	orig := parseGIR
	parseGIR = func(path string) (*gir.TypeRegistry, error) {
		n++
		return orig(path)
	}
	t.Cleanup(func() { parseGIR = orig })
	return &n
}

// girCopy writes the bundled GIR to a file of the test's own, so the test can
// edit it.
func girCopy(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("gir", "minimal", "Gtk-4.0.gir"))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "Gtk-4.0.gir")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// A GIR is parsed once per content, not once per build: a later build over
// the same store reads the stored registry, and an edited file is parsed
// again.
func TestGIRIsParsedOncePerContent(t *testing.T) {
	parses := countParses(t)
	store, path := t.TempDir(), girCopy(t)
	build := func() *gir.TypeRegistry {
		t.Helper()
		g := &Generator{store: gencache.Open(store), girOpt: path}
		reg, err := g.gir()
		if err != nil {
			t.Fatal(err)
		}
		return reg
	}

	first := build()
	second := build()
	if *parses != 1 {
		t.Fatalf("two builds over one GIR parsed it %d times, want 1", *parses)
	}
	if len(second.ByCType) == 0 || len(second.ByCType) != len(first.ByCType) {
		t.Errorf("the stored registry has %d classes, the parsed one %d", len(second.ByCType), len(first.ByCType))
	}
	if first.ByCType["GtkButton"] == nil || second.ByCType["GtkButton"].Props[0].IRType == nil {
		t.Error("the stored registry lost what Restore should rebuild")
	}

	data, _ := os.ReadFile(path)
	os.WriteFile(path, append(data, "\n<!-- edited -->\n"...), 0o644)
	build()
	if *parses != 2 {
		t.Errorf("after the GIR was edited, it was parsed %d times in all, want 2", *parses)
	}
}

// The generated declarations are served exactly as the store holds them, so
// the file the checker reads says what it was generated from: the registry
// entry, which in turn names the GIR.
func TestServedDeclarationsCarryTheirInputs(t *testing.T) {
	g := &Generator{store: gencache.Open(t.TempDir()), girOpt: girCopy(t)}
	fsys := g.PackageFS()
	if fsys == nil {
		t.Fatal(g.fsErr)
	}
	data, err := fs.ReadFile(fsys, widgetSourceFile)
	if err != nil {
		t.Fatal(err)
	}
	src := string(data)
	for _, want := range []string{
		`import cache "sngl:x/gen/cache"`,
		`cache.entry(producer="gtk4.registry"`,
		"component GtkButton(",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("served declarations are missing %s", want)
		}
	}
}

// A path the option names and that is not there is the error it always was,
// and the platform reports itself unavailable with it.
func TestMissingNamedGIRIsUnavailable(t *testing.T) {
	g := &Generator{store: gencache.Open(t.TempDir()), girOpt: filepath.Join(t.TempDir(), "nope.gir")}
	err := g.Unavailable()
	if err == nil || !strings.Contains(err.Error(), "--opt gir=") {
		t.Errorf("Unavailable() = %v, want the --opt gir= message", err)
	}
	if g.PackageFS() != nil {
		t.Error("a platform with no GIR served a package")
	}
}

// The probe takes the first location that stats cleanly, as resolveGIRPath
// does, and records every one before it as absent. A dangling link and a
// location under a file are neither a GIR nor a reason to fail: both are
// skipped, and the stored registry is reused by the next build rather than
// read as stale because the check disagreed with the probe.
func TestProbeSkipsUnusableLocations(t *testing.T) {
	parses := countParses(t)
	tmp := t.TempDir()
	dangling := filepath.Join(tmp, "dangling.gir")
	if err := os.Symlink(filepath.Join(tmp, "missing"), dangling); err != nil {
		t.Skip("symlinks unavailable:", err)
	}
	file := filepath.Join(tmp, "file")
	os.WriteFile(file, nil, 0o644)
	orig := girAutoPaths
	girAutoPaths = []string{dangling, filepath.Join(file, "Gtk-4.0.gir"), girCopy(t)}
	t.Cleanup(func() { girAutoPaths = orig })

	store := t.TempDir()
	for range 2 {
		g := &Generator{store: gencache.Open(store)}
		reg, err := g.gir()
		if err != nil {
			t.Fatal(err)
		}
		if reg.ByCType["GtkButton"] == nil {
			t.Fatal("the probe did not reach the GIR past the unusable locations")
		}
	}
	if *parses != 1 {
		t.Errorf("two builds over one probe parsed the GIR %d times, want 1", *parses)
	}
}
