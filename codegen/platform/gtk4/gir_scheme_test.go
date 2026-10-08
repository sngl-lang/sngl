package gtk4

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"duckfam.us/sngl/codegen"
	"duckfam.us/sngl/internal/build"
	"duckfam.us/sngl/internal/gencache"
	"duckfam.us/sngl/internal/parser"
	"duckfam.us/sngl/ir"
)

// The widget declarations are the `gir:` scheme's package, which
// sngl:platform/gtk4 imports, and `--opt gir=` is what decides which GIR that
// import reads. Its output is stored, so the option has to be part of what
// the stored package depends on: a class only one of two GIRs declares checks
// under the option naming that GIR and not under the other, in either order
// and against one store.
func TestGIRSchemeFollowsTheOption(t *testing.T) {
	t.Setenv("SNGL_GENCACHE_DIR", t.TempDir())
	gencache.ResetDefault()
	defer gencache.ResetDefault()

	base, err := os.ReadFile(filepath.Join("gir", "minimal", "Gtk-4.0.gir"))
	if err != nil {
		t.Fatal(err)
	}
	extra := `    <class name="Foo" c:type="GtkFoo" parent="Widget">
      <constructor name="new" c:identifier="gtk_foo_new"/>
    </class>
  </namespace>`
	custom := filepath.Join(t.TempDir(), "Gtk-4.0.gir")
	if err := os.WriteFile(custom, []byte(strings.Replace(string(base), "  </namespace>", extra, 1)), 0o644); err != nil {
		t.Fatal(err)
	}

	src := `import gtk "gir:Gtk-4.0"
import ui "sngl:ui"

ui.window {
    gtk.GtkFoo() {}
}
`
	t.Cleanup(func() { _ = codegen.LookupPlatform("gtk4").(*Generator).Configure(map[string]string{}) })
	check := func(opt string) error {
		t.Helper()
		g, ok := codegen.LookupPlatform("gtk4").(*Generator)
		if !ok {
			t.Fatalf("gtk4 resolved to %T", codegen.LookupPlatform("gtk4"))
		}
		if err := g.Configure(map[string]string{"gir": opt}); err != nil {
			t.Fatal(err)
		}
		doc, err := parser.Parse("app.sngl", []byte(src))
		if err != nil {
			t.Fatal(err)
		}
		_, err = build.Check(doc, build.CheckConfig{
			Dir:     t.TempDir(),
			Targets: []ir.StaticTarget{{Platform: "gtk4", Language: "go"}},
		})
		return err
	}
	for _, step := range []struct {
		opt  string
		want bool
	}{{custom, true}, {girBuiltin, false}, {custom, true}} {
		err := check(step.opt)
		if step.want && err != nil {
			t.Errorf("gir=%s: %v", step.opt, err)
		}
		if !step.want && (err == nil || !strings.Contains(err.Error(), "GtkFoo")) {
			t.Errorf("gir=%s: err = %v, want GtkFoo unknown", step.opt, err)
		}
	}
}
