//go:build !js

package gtk4

import (
	"fmt"
	"sort"
	"strings"
	"testing"

	"duckfam.us/sngl/codegen"
)

// unixPrintClasses are declared from Gtk-4.0.gir but live in
// <gtk/gtkunixprint.h>, which <gtk/gtk.h> does not include — so cgo cannot
// resolve their types or constructors no matter what arguments it is handed.
// A separate defect from anything about zero values, and the reason this
// sweep excludes them rather than asserting they build.
var unixPrintClasses = map[string]bool{
	"GtkPageSetupUnixDialog": true,
	"GtkPrintJob":            true,
	"GtkPrintUnixDialog":     true,
	"GtkPrinter":             true,
	// Not a unix-print class: gtk_application_window_new is special-cased to
	// take the GtkApplication the generated BuildUI receives as `app`, so it
	// can only be emitted into that scope. Reached from anywhere else the
	// emitted call references an identifier that is not in scope. A separate
	// defect from anything this sweep is about.
	"GtkApplicationWindow": true,
}

// TestWidgetSurface_EitherCompilesOrRefuses is the invariant the whole
// GIR-driven surface has to hold: instantiating any declared widget class
// either emits cgo that builds against the host's GTK, or refuses the build
// with a diagnostic naming the class. Nothing in between — a declaration that
// type-checks in SNGL and then emits Go the user's compiler rejects is the
// bug class this platform is most exposed to, since every declaration comes
// from introspection data.
//
// It generates every class once to partition them, then builds every widget
// that did generate in a single program. The classes are the bundled GIR's,
// which are the ones this platform wraps.
func TestWidgetSurface_EitherCompilesOrRefuses(t *testing.T) {
	reg, err := (&Generator{}).gir()
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, c := range reg.Classes {
		if c.CType != "" && !unixPrintClasses[c.CType] {
			names = append(names, c.CType)
		}
	}
	sort.Strings(names)

	var buildable []string
	refused := map[string]string{}
	for _, n := range names {
		if _, err := generateGtk4(t, gtk4Window("            gtk."+n+"() {}")); err != nil {
			// A refusal has to name the class, or it is not actionable.
			if !strings.Contains(err.Error(), n) {
				t.Errorf("%s was refused without being named: %v", n, err)
			}
			refused[n] = err.Error()
			continue
		}
		buildable = append(buildable, n)
	}
	t.Logf("%d of %d classes generate; %d refuse with a diagnostic", len(buildable), len(names), len(refused))
	// Refusing is a valid answer for a class, so the counts have to be guarded:
	// a change that made every widget refuse would otherwise leave nothing to
	// compile and nothing to fail.
	if len(buildable) == 0 {
		t.Fatal("no class generated; the surface asserted nothing")
	}
	if min := len(names) / 2; len(buildable) < min {
		t.Errorf("only %d of %d classes generate; want at least %d -- the surface collapsed rather than a class refusing",
			len(buildable), len(names), min)
	}

	var b strings.Builder
	for _, n := range buildable {
		fmt.Fprintf(&b, "            gtk.%s() {}\n", n)
	}
	files, err := generateGtk4(t, gtk4Window(b.String()))
	if err != nil {
		t.Fatalf("generating all %d buildable widgets together: %v", len(buildable), err)
	}
	if out, ok := buildEmittedGo(t, files); !ok {
		t.Errorf("the emitted cgo for %d widgets does not build:\n%s", len(buildable), out)
	}
}

// buildEmittedGo builds the generated files as a module of their own and
// reports the compiler's output. The emitted package is `main` but carries no
// entry point unless the main option is set, so one is supplied.
func buildEmittedGo(t *testing.T, files map[string][]byte) (string, bool) {
	t.Helper()
	build := map[string]string{"entry.go": "package main\n\nfunc main() {}\n"}
	for name, data := range files {
		build[name] = string(data)
	}
	goVersion, extra := codegen.DetectHostGoMod()
	if goVersion == "" {
		goVersion = "1.23"
	}
	mod := "module tmp\n\ngo " + goVersion + "\n"
	if extra != "" {
		mod += "\n" + extra + "\n"
	}
	build["go.mod"] = mod
	return runGo(t, "gtk4-emitted-", build, false, "build", "./...")
}
