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
// sngl:platform/gtk4 imports, and gtk4's `gir` option is what decides which
// GIR that import reads. Its output is stored, so the option has to be part of what
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
	check := func(src string, opts map[string]string) error {
		t.Helper()
		doc, err := parser.Parse("app.sngl", []byte(src))
		if err != nil {
			t.Fatal(err)
		}
		_, err = build.Check(doc, build.CheckConfig{
			Dir:     t.TempDir(),
			IsMain:  true,
			Targets: []ir.StaticTarget{{Platform: "gtk4", Language: "go"}},
			Opts:    opts,
		})
		return err
	}
	for _, step := range []struct {
		opt  string
		want bool
	}{{custom, true}, {girBuiltin, false}, {custom, true}} {
		err := check(src, map[string]string{"gir": step.opt})
		if step.want && err != nil {
			t.Errorf("gir=%s: %v", step.opt, err)
		}
		if !step.want && (err == nil || !strings.Contains(err.Error(), "GtkFoo")) {
			t.Errorf("gir=%s: err = %v, want GtkFoo unknown", step.opt, err)
		}
	}

	// The output block's option decides the check as it decides the build,
	// and the command line's overrides it.
	declared := strings.Replace(src, "ui.window", "output {\n    go {\n        gtk4(gir=\""+custom+"\")\n    }\n}\n\nui.window", 1)
	if err := check(declared, nil); err != nil {
		t.Errorf("output block gir=%s: %v", custom, err)
	}
	if err := check(declared, map[string]string{"gir": girBuiltin}); err == nil || !strings.Contains(err.Error(), "GtkFoo") {
		t.Errorf("--opt gir=builtin over the output block: err = %v, want GtkFoo unknown", err)
	}
}
