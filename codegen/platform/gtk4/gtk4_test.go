package gtk4_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/codegen"
	_ "git.duckfam.us/jonathan/sngl/codegen/platform/gtk4"
	"git.duckfam.us/jonathan/sngl/internal/checker"
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

	docs := checker.ProvidedDocs(gen)
	if len(docs) != 3 {
		t.Fatalf("ProvidedDocs = %d docs; want 3 (doc.sngl, the written half and the generated one)", len(docs))
	}
	var names []string
	for _, doc := range docs {
		for _, stmt := range doc.Stmts {
			if c, ok := stmt.(*ast.ComponentDecl); ok && strings.HasPrefix(c.Name, "Gtk") {
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
