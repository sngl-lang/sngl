package gtk4

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"duckfam.us/sngl/codegen"
	_ "duckfam.us/sngl/codegen/lang"
	"duckfam.us/sngl/ir"
)

// No test reads the host's GIR: what it declares is whichever GTK the host
// has, so every test generates against the bundled subset or rulesGIR.
func init() { girAutoPaths = nil }

// rulesGIR points the registered generator at the bundled GIR followed by
// testdata/rules.gir, until the test ends, and returns it. The fragment is
// GTK 4.8's own entries for the classes the generic rules are asserted over,
// and an entry there replaces the bundled one of its name.
func rulesGIR(t *testing.T) *Generator {
	t.Helper()
	base, err := os.ReadFile(filepath.Join("gir", "minimal", "Gtk-4.0.gir"))
	if err != nil {
		t.Fatal(err)
	}
	rules, err := os.ReadFile(filepath.Join("testdata", "rules.gir"))
	if err != nil {
		t.Fatal(err)
	}
	end := []byte("  </namespace>")
	start := bytes.Index(rules, []byte("<namespace"))
	start += bytes.IndexByte(rules[start:], '\n') + 1
	cut := bytes.LastIndex(base, end)
	merged := slices.Concat(base[:cut], rules[start:bytes.LastIndex(rules, end)], base[cut:])
	// Named by its content: the path is in the gtk4.registry store key and the
	// library cache's, so a fresh one per test filled the user's store with an
	// entry per run and checked the library again per call.
	sum := sha256.Sum256(merged)
	path := filepath.Join(os.TempDir(), "sngl-gtk4-rules-"+hex.EncodeToString(sum[:8])+".gir")
	if _, err := os.Stat(path); err != nil {
		tmp, err := os.CreateTemp(os.TempDir(), "sngl-gtk4-rules-*")
		if err != nil {
			t.Fatal(err)
		}
		_, werr := tmp.Write(merged)
		if err := errors.Join(werr, tmp.Close(), os.Rename(tmp.Name(), path)); err != nil {
			t.Fatal(err)
		}
	}
	g := codegen.LookupPlatform(platformName).(*Generator)
	if err := g.Configure(map[string]string{"gir": path}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = g.Configure(map[string]string{}) })
	return g
}

// TestUnavailable_WithdrawsPackage pins the degradation contract: with no
// usable GIR there is no widget set to generate declarations from, so the
// platform contributes none — the checker never sees the gtk.Gtk* references
// in gtk4.sngl, and compiles for every other platform are unaffected.
func TestUnavailable_WithdrawsPackage(t *testing.T) {
	g := &Generator{}
	if err := g.Configure(map[string]string{"gir": filepath.Join(t.TempDir(), "absent.gir")}); err != nil {
		t.Fatal(err)
	}
	if err := g.Unavailable(); err == nil {
		t.Fatal("Unavailable() = nil with a missing gir path; want an error")
	}
	if docs := codegen.PlatformDocs(g); docs != nil {
		t.Errorf("PlatformDocs = %d docs while unavailable; want none", len(docs))
	}
	// Targeting it anyway must fail loudly rather than emit an empty UI.
	req := &codegen.Request{Pkg: &ir.Package{}, Lang: codegen.LookupLang("go"), Source: "t.sngl"}
	err := g.Generate(req, codegen.NewMemSink())
	if err == nil {
		t.Fatal("Generate() = nil while unavailable; want an error")
	}
	for _, want := range []string{"gtk4 is unavailable", "--opt gir="} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("Generate error %q does not mention %q", err, want)
		}
	}
}

// TestAvailable_ProvidesPackage is the mirror case: where the GIR file exists,
// the platform contributes its package -- lib/platform/gtk4, whose import of
// gir:Gtk-4.0 brings the generated widget set.
func TestAvailable_ProvidesPackage(t *testing.T) {
	g := &Generator{}
	if err := g.Unavailable(); err != nil {
		t.Skipf("gtk4 metadata unavailable: %v", err)
	}
	if n := len(codegen.PlatformDocs(g)); n < 2 {
		t.Errorf("PlatformDocs = %d docs while gtk4 is available; want doc.sngl and gtk4.sngl", n)
	}
}
