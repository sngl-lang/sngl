package gtk4_test

import (
	"os"
	"path/filepath"
	"testing"

	"git.duckfam.us/jonathan/sngl/codegen"
	_ "git.duckfam.us/jonathan/sngl/codegen/platform/gtk4"
	"git.duckfam.us/jonathan/sngl/ir"
)

func TestPlatformRegistered(t *testing.T) {
	gen := codegen.LookupPlatform("gtk4")
	if gen == nil {
		t.Fatal("gtk4 platform not registered")
	}
	if gen.PlatformIdentifier() != "gtk4" {
		t.Errorf("identifier = %q, want gtk4", gen.PlatformIdentifier())
	}
}

func TestResolve_KnownWidget(t *testing.T) {
	gen := codegen.LookupPlatform("gtk4")
	if gen == nil {
		t.Fatal("gtk4 platform not registered")
	}
	sym := gen.Resolve("GtkButton")
	if sym == nil {
		t.Skip("GIR file not available on this machine — skipping")
	}
	comp, ok := sym.(*ir.Component)
	if !ok {
		t.Fatalf("expected *ir.Component, got %T", sym)
	}
	if comp.Name != "GtkButton" {
		t.Errorf("Name = %q, want GtkButton", comp.Name)
	}
}

// TestResolve_HonorsConfigureGIR verifies the platform's Resolve uses the
// gir path supplied via Configure (via --opt gir= on the CLI), so type-check
// works on machines without system GTK4 dev files installed.
func TestResolve_HonorsConfigureGIR(t *testing.T) {
	gen := codegen.LookupPlatform("gtk4")
	if gen == nil {
		t.Fatal("gtk4 platform not registered")
	}
	cfg, ok := gen.(codegen.OptionConfigurable)
	if !ok {
		t.Fatal("gtk4 generator does not implement OptionConfigurable")
	}
	dir := t.TempDir()
	gir := filepath.Join(dir, "Gtk-4.0-test.gir")
	if err := os.WriteFile(gir, []byte(minimalGIR), 0644); err != nil {
		t.Fatal(err)
	}
	if err := cfg.Configure(map[string]string{"gir": gir}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cfg.Configure(map[string]string{}) })
	sym := gen.Resolve("GtkButton")
	if sym == nil {
		t.Fatal("Resolve(GtkButton) = nil with Configure'd gir; expected component")
	}
	if comp, ok := sym.(*ir.Component); !ok || comp.Name != "GtkButton" {
		t.Fatalf("got %T %v, want *ir.Component named GtkButton", sym, sym)
	}
}

const minimalGIR = `<?xml version="1.0"?>
<repository version="1.2"
  xmlns="http://www.gtk.org/introspection/core/1.0"
  xmlns:c="http://www.gtk.org/introspection/c/1.0"
  xmlns:glib="http://www.gtk.org/introspection/glib/1.0">
  <namespace name="Gtk" version="4.0">
    <class name="Button" c:type="GtkButton">
      <constructor name="new" c:identifier="gtk_button_new"/>
    </class>
  </namespace>
</repository>`

func TestResolve_Unknown(t *testing.T) {
	gen := codegen.LookupPlatform("gtk4")
	if gen == nil {
		t.Skip("gtk4 not registered")
	}
	if sym := gen.Resolve("NotAWidget"); sym != nil {
		t.Errorf("expected nil for unknown widget, got %v", sym)
	}
}
