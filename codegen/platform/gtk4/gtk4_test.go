package gtk4_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"duckfam.us/sngl/codegen"
	_ "duckfam.us/sngl/codegen/platform/gtk4"
	"duckfam.us/sngl/internal/build"
	"duckfam.us/sngl/internal/checker"
	"duckfam.us/sngl/internal/parser"
	"duckfam.us/sngl/internal/snapshot"
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

// TestConfigureGIR_ProvidesDeclarations verifies the platform builds its
// widget declarations from the gir path supplied via Configure (--opt gir= on
// the CLI), so a type-check works on a machine with no system GTK4 dev files.
func TestConfigureGIR_ProvidesDeclarations(t *testing.T) {
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

	// The declarations are the gir: scheme's package, resolved here with no
	// build around the check as `sngl doc` resolves it, against the languages
	// the binary links: the package's Look is a `go.native` struct.
	doc, err := parser.Parse("t.sngl", []byte("import gtk \"gir:Gtk-4.0\"\n"))
	if err != nil {
		t.Fatal(err)
	}
	langs, _ := build.RegisteredTargets()
	pkg, diags := checker.Check(doc, &checker.Config{Languages: langs})
	for _, d := range diags {
		t.Errorf("%s: %s", d.Pos, d.Msg)
	}
	var names []string
	for _, imp := range pkg.Imports {
		if imp.Path != "gir:Gtk-4.0" || imp.Pkg == nil {
			continue
		}
		for _, c := range imp.Pkg.Components {
			if strings.HasPrefix(c.Name, "Gtk") {
				names = append(names, c.Name)
			}
		}
	}
	if len(names) != 1 || names[0] != "GtkButton" {
		t.Errorf("declared components = %v; want just the GtkButton the supplied GIR names", names)
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

func TestSnapshotTakesALanguageGtk4Supports(t *testing.T) {
	lang := snapshot.LangForPlatform("gtk4")
	if !slices.Contains(codegen.LookupPlatform("gtk4").SupportedLangs(), lang) {
		t.Errorf("sngl snapshot builds gtk4 under %q, which it does not support", lang)
	}
}
