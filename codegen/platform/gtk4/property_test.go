//go:build !js

package gtk4

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/codegen"
	_ "git.duckfam.us/jonathan/sngl/codegen/lang"
	"git.duckfam.us/jonathan/sngl/internal/checker"
	"git.duckfam.us/jonathan/sngl/internal/lower"
	"git.duckfam.us/jonathan/sngl/internal/parser"
	"git.duckfam.us/jonathan/sngl/ir"
)

// generateGtk4 runs one SNGL source through the whole gtk4 pipeline and
// returns the generated files. A checker diagnostic fails the test; a codegen
// error is returned, since refusing to emit is behavior under test.
func generateGtk4(t *testing.T, src string) (map[string][]byte, error) {
	t.Helper()
	doc, err := parser.Parse("t.sngl", []byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	pkg, diags := checker.Check(doc, &checker.Config{IsMain: true, Platforms: codegen.CollectPlatforms()})
	for _, d := range diags {
		if d.Severity == ir.Error {
			t.Fatalf("check diag: %s", d.Msg)
		}
	}
	g := &Generator{}
	lang := codegen.LookupLang("go")
	if lang == nil {
		t.Fatal("go lang not registered")
	}
	if err := lower.Lower(pkg, g.Capabilities(lang).ToLowerCaps(), lower.Options{Platform: "gtk4"}); err != nil {
		t.Fatalf("lower: %v", err)
	}
	mem := codegen.NewMemSink()
	if err := g.Generate(&codegen.Request{Pkg: pkg, Lang: lang, Source: "t.sngl"}, mem); err != nil {
		return nil, err
	}
	return mem.Files(), nil
}

// setterlessSource sets four GTK properties GIR names no setter for — one of
// each kind the generic path carries — alongside one that has a setter, so the
// two emission paths appear side by side.
const setterlessSource = `
import . "sngl://std"

component main {
    window(title="t") {
        platform gtk4 {
            gtk4.GtkEntry(primaryIconName="edit-find", enableEmojiCompletion=true, maxLength=12) {}
            gtk4.GtkLabel(label="hi", accessibleRole="button") {}
            gtk4.GtkAssistant(useHeaderBar=1) {}
            gtk4.GtkCellRendererText(alignSet=true, scale=0.25) {}
            gtk4.GtkGrid(columnSpacing=4) {}
        }
    }
}
`

// TestGObjectPropSet_EmitsGenericPath pins that a property with no C setter is
// still set: by name, through g_object_set_property, with the preamble helper
// the call needs. Nothing may emit a gtk_*_set_* function that does not exist.
func TestGObjectPropSet_EmitsGenericPath(t *testing.T) {
	skipWithoutGIR(t)
	files, err := generateGtk4(t, setterlessSource)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	model := string(files["model.go"])
	for _, want := range []string{
		// The helper definitions, emitted only when something needs them.
		"static void sngl_set_prop(void *obj, const char *name, GValue *src)",
		"g_object_set_property(o, name, &dst);",
		// The property's GObject name is its GIR name, hyphens and all.
		`C.sngl_set_prop_string(unsafe.Pointer(m.__n0), C.CString("primary-icon-name"), C.CString("edit-find"))`,
		`C.sngl_set_prop_bool(unsafe.Pointer(m.__n0), C.CString("enable-emoji-completion"), C.int(boolToInt(true)))`,
		// An enum-typed property with no setter takes its member's C
		// constant through the int helper.
		`C.sngl_set_prop_int(unsafe.Pointer(m.__n1), C.CString("accessible-role"), C.int(C.GTK_ACCESSIBLE_ROLE_BUTTON))`,
		`C.sngl_set_prop_int(unsafe.Pointer(m.__n2), C.CString("use-header-bar"), C.int(1))`,
		`C.sngl_set_prop_double(unsafe.Pointer(m.__n3), C.CString("scale"), C.double(0.25))`,
		// A property that does have a setter still calls it directly.
		`C.gtk_entry_set_max_length((*C.GtkEntry)(unsafe.Pointer(m.__n0)), C.int(12))`,
		// The value is cast to what the setter's parameter is declared as,
		// which GIR does not promise is the property's own type: a gint
		// property whose setter takes a guint.
		`C.gtk_grid_set_column_spacing((*C.GtkGrid)(unsafe.Pointer(m.__n4)), C.guint(4))`,
	} {
		if !strings.Contains(model, want) {
			t.Errorf("model.go is missing:\n\t%s", want)
		}
	}
	// A setter GTK does not ship must not be invented for any of them.
	for _, absent := range []string{
		"gtk_entry_set_primary_icon_name",
		"gtk_entry_set_enable_emoji_completion",
		"gtk_label_set_accessible_role",
	} {
		if strings.Contains(model, absent) {
			t.Errorf("model.go calls %s, which GTK does not declare", absent)
		}
	}
}

// TestGObjectPropSet_HelperIsGatedOnUse pins that a program setting nothing
// through the generic path does not carry its helpers. GtkGrid is outside the
// wrapped surface, so this one is emitted as cgo and does have a preamble for
// the helpers to be absent from.
func TestGObjectPropSet_HelperIsGatedOnUse(t *testing.T) {
	skipWithoutGIR(t)
	files, err := generateGtk4(t, `
import . "sngl://std"

component main {
    window(title="t") {
        platform gtk4 {
            gtk4.GtkGrid(columnSpacing=4) {}
        }
    }
}
`)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	model := string(files["model.go"])
	if !strings.Contains(model, "gtk_grid_set_column_spacing") {
		t.Fatal("model.go is not the inline-cgo emission; there is no preamble to check")
	}
	if strings.Contains(model, "sngl_set_prop") {
		t.Error("model.go carries the g_object_set_property helpers with nothing to set through them")
	}
}

// TestUnsettableProp_FailsTheBuild pins the other half of "never emit a call
// that does not compile": a property whose value type no SNGL value can
// produce refuses the build, naming the property, rather than emitting a call
// whose argument is the wrong type.
func TestUnsettableProp_FailsTheBuild(t *testing.T) {
	skipWithoutGIR(t)
	_, err := generateGtk4(t, `
import . "sngl://std"

component main {
    window(title="t") {
        platform gtk4 {
            gtk4.GtkLabel(label="hi", ellipsize="end") {}
        }
    }
}
`)
	if err == nil {
		t.Fatal("generate succeeded; GtkLabel.ellipsize takes a Pango.EllipsizeMode, which no SNGL value produces")
	}
	for _, want := range []string{"GtkLabel", "ellipsize", "Pango.EllipsizeMode"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not name %s", err, want)
		}
	}
}

// TestEnumProp_RejectsUnknownMember pins that an enum member is resolved from
// GIR and not guessed: a string naming no member fails the build.
func TestEnumProp_RejectsUnknownMember(t *testing.T) {
	skipWithoutGIR(t)
	_, err := generateGtk4(t, `
import . "sngl://std"

component main {
    window(title="t") {
        platform gtk4 {
            gtk4.GtkLabel(label="hi", justify="middle") {}
        }
    }
}
`)
	if err == nil {
		t.Fatal("generate succeeded; GtkJustification has no member named middle")
	}
	if !strings.Contains(err.Error(), "middle") || !strings.Contains(err.Error(), "Justification") {
		t.Errorf("error %q does not name the value and its enumeration", err)
	}
}

// TestSetterlessProps_GeneratedGoCompiles is the point of the generic path: a
// declaration that type-checks in SNGL but emits Go that cannot be built is
// not a usable property. Builds the emitted cgo against the host's GTK.
func TestSetterlessProps_GeneratedGoCompiles(t *testing.T) {
	skipWithoutGIR(t)
	if _, err := exec.LookPath("pkg-config"); err != nil {
		t.Skip("pkg-config not on PATH")
	}
	if err := exec.Command("pkg-config", "--exists", "gtk4").Run(); err != nil {
		t.Skip("gtk4 dev libraries not installed (pkg-config)")
	}
	files, err := generateGtk4(t, setterlessSource)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	dir := t.TempDir()
	for name, data := range files {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	goVersion, extra := codegen.DetectHostGoMod()
	if goVersion == "" {
		goVersion = "1.23"
	}
	mod := "module tmp\n\ngo " + goVersion + "\n"
	if extra != "" {
		mod += "\n" + extra + "\n"
	}
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(mod), 0o644); err != nil {
		t.Fatal(err)
	}
	// The emitted package is `main` but carries no entry point of its own
	// unless the main option is set, which this request does not.
	if err := os.WriteFile(filepath.Join(dir, "entry.go"), []byte("package main\n\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("go", "build", "./...")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build on the emitted cgo: %v\n%s", err, out)
	}
}
