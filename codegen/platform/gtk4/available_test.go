package gtk4

import (
	"path/filepath"
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/codegen"
	_ "git.duckfam.us/jonathan/sngl/codegen/lang"
	"git.duckfam.us/jonathan/sngl/ir"
)

// skipWithoutGIR skips tests that need the GTK 4 introspection data to resolve
// widget metadata. These tests are metadata-only — they never link against
// GTK — but without Gtk-4.0.gir the platform reports itself unavailable and
// Generate refuses, so there is nothing to assert. Tests that additionally
// need a real GTK to compile or render gate on pkg-config as well (see
// internal/testutil.ComponentFixtureSkipReason).
// skipWithoutGIR skips a test that asserts the host GTK's own catalogue --
// which classes exist, which properties it records a setter for. Since the
// platform falls back to the bundled subset, Unavailable() can no longer fail,
// so the condition is whether the registry came from that subset rather than
// whether one loaded at all.
func skipWithoutGIR(t *testing.T) {
	t.Helper()
	if (&Generator{}).usingMinimalGIR() {
		t.Skip("no host Gtk-4.0.gir; the bundled subset does not carry the catalogue this asserts")
	}
}

// TestUnavailable_WithdrawsPackage pins the degradation contract: with no
// usable GIR there is no widget set to generate declarations from, so the
// platform contributes none — the checker never sees the gtk4.Gtk* references
// in gtk4.sngl and compiles for every other platform are unaffected.
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
	if fsys := g.PackageFS(); fsys != nil {
		t.Errorf("PackageFS = %v while unavailable; want nil", fsys)
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
// the platform contributes both halves of its package — the hand-written
// overrides and the generated widget set.
func TestAvailable_ProvidesPackage(t *testing.T) {
	g := &Generator{}
	if err := g.Unavailable(); err != nil {
		t.Skipf("gtk4 metadata unavailable: %v", err)
	}
	if n := len(codegen.PlatformDocs(g)); n < 2 {
		t.Errorf("PlatformDocs = %d docs while gtk4 is available; want the embedded source and the generated widgets", n)
	}
	if g.PackageFS() == nil {
		t.Error("PackageFS = nil while gtk4 is available")
	}
}
