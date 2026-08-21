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
func skipWithoutGIR(t *testing.T) {
	t.Helper()
	if err := (&Generator{}).Unavailable(); err != nil {
		t.Skipf("gtk4 metadata unavailable: %v", err)
	}
}

// TestUnavailable_WithdrawsPackage pins the degradation contract: with no
// usable GIR the platform contributes no Package() docs, so the checker never
// sees the gtk4.Gtk* references in gtk4.sngl and compiles for every other
// platform are unaffected.
func TestUnavailable_WithdrawsPackage(t *testing.T) {
	g := &Generator{}
	if err := g.Configure(map[string]string{"gir": filepath.Join(t.TempDir(), "absent.gir")}); err != nil {
		t.Fatal(err)
	}
	if err := g.Unavailable(); err == nil {
		t.Fatal("Unavailable() = nil with a missing gir path; want an error")
	}
	if docs := g.Package(); docs != nil {
		t.Errorf("Package() = %d docs while unavailable; want none", len(docs))
	}
	if sym := g.Resolve("GtkButton"); sym != nil {
		t.Errorf("Resolve(GtkButton) = %v while unavailable; want nil", sym)
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
// nothing about the platform's contribution changed.
func TestAvailable_ProvidesPackage(t *testing.T) {
	g := &Generator{}
	if err := g.Unavailable(); err != nil {
		t.Skipf("gtk4 metadata unavailable: %v", err)
	}
	if len(g.Package()) == 0 {
		t.Error("Package() empty while gtk4 is available")
	}
}
