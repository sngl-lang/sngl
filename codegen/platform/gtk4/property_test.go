//go:build !js

package gtk4

import (
	"strings"
	"testing"

	"duckfam.us/sngl/codegen"
	_ "duckfam.us/sngl/codegen/lang"
	"duckfam.us/sngl/internal/checker"
	"duckfam.us/sngl/internal/lower"
	"duckfam.us/sngl/internal/parser"
	"duckfam.us/sngl/ir"
)

// generateGtk4 runs one SNGL source through the whole gtk4 pipeline and
// returns the generated files. A checker diagnostic fails the test; a codegen
// error is returned, since refusing to emit is behavior under test.
func generateGtk4(t *testing.T, src string) (map[string][]byte, error) {
	t.Helper()
	pkg, err := lowerGtk4(t, src)
	if err != nil {
		t.Fatalf("lower: %v", err)
	}
	g := codegen.LookupPlatform(platformName)
	mem := codegen.NewMemSink()
	if err := g.Generate(&codegen.Request{Pkg: pkg, Lang: codegen.LookupLang("go"), Source: "t.sngl"}, mem); err != nil {
		return nil, err
	}
	return mem.Files(), nil
}

// lowerGtk4 checks src for gtk4, failing the test on a check error, and
// lowers a copy of it.
func lowerGtk4(t *testing.T, src string) (*ir.Package, error) {
	t.Helper()
	doc, err := parser.Parse("t.sngl", []byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	pkg, diags := checker.Check(doc, &checker.Config{IsMain: true, Platforms: codegen.CollectPlatforms(), Languages: codegen.CollectLangs(), Targets: []ir.StaticTarget{{Platform: "gtk4", Language: "go"}}})
	for _, d := range diags {
		if d.Severity == ir.Error {
			t.Fatalf("check diag: %s", d.Msg)
		}
	}
	g := codegen.LookupPlatform(platformName)
	lang := codegen.LookupLang("go")
	if lang == nil {
		t.Fatal("go lang not registered")
	}
	pkg = ir.ClonePackage(pkg)
	return pkg, lower.Lower(pkg, codegen.CapsOrNone(lang.LanguageIdentifier(), g.PlatformIdentifier()), lower.Options{Platform: "gtk4", Language: "go"})
}

// checkGtk4 runs one SNGL source through the checker against every registered
// platform and returns the error diagnostics. Used where refusing to
// type-check is the behavior under test: a prop or event this platform does
// not declare is a checker error, not a codegen one.
func checkGtk4(t *testing.T, src string) []string {
	t.Helper()
	doc, err := parser.Parse("t.sngl", []byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	_, diags := checker.Check(doc, &checker.Config{IsMain: true, Platforms: codegen.CollectPlatforms(), Languages: codegen.CollectLangs(), Targets: []ir.StaticTarget{{Platform: "gtk4", Language: "go"}}})
	var msgs []string
	for _, d := range diags {
		if d.Severity == ir.Error {
			msgs = append(msgs, d.Msg)
		}
	}
	return msgs
}

// gtk4Window wraps one platform-gtk4 body in the smallest program that
// reaches the emitter.
func gtk4Window(body string) string {
	return "\nimport . \"sngl:ui\"\nimport gtk \"gir:Gtk-4.0\"\n\nwindow(title=\"t\") {\n" + body + "\n}\n"
}

// setterlessSource sets four GTK properties GIR names no setter for — one of
// each kind the generic path carries — alongside one that has a setter, so the
// two emission paths appear side by side. GtkTextBuffer.text is the fifth
// kind: GIR does name a setter, but gtk_text_buffer_set_text takes (text,
// len) and a cgo call site passes one value, so it belongs to the generic
// path too.
const setterlessSource = `
import . "sngl:ui"
import gtk "gir:Gtk-4.0"

window(title="t") {
    gtk.GtkEntry(primaryIconName="edit-find", enableEmojiCompletion=true, maxLength=12) {}
    gtk.GtkLabel(label="hi", accessibleRole="button") {}
    gtk.GtkTextBuffer(text="hi") {}
    gtk.GtkCellRendererText(alignSet=true, scale=0.25) {}
    gtk.GtkGrid(columnSpacing=4) {}
}
`

// TestGObjectPropSet_EmitsGenericPath pins that a property with no C setter is
// still set: by name, through g_object_set_property, with the preamble helper
// the call needs. Nothing may emit a gtk_*_set_* function that does not exist.
func TestGObjectPropSet_EmitsGenericPath(t *testing.T) {
	rulesGIR(t)
	files, err := generateGtk4(t, setterlessSource)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	model := string(files["model.go"])
	for _, want := range []string{
		// The helper definitions, emitted only when something needs them.
		"static void sngl_set_prop(void *obj, const char *name, GValue *src)",
		"g_object_set_property(o, name, &dst);",
		// The property's GObject name is its GIR name, hyphens and all, and
		// it and a literal value are C strings made once for the application
		// and freed at teardown (ReleaseTemps).
		`m.__rel0 = C.CString("primary-icon-name")`,
		`m.__rel1 = C.CString("edit-find")`,
		`C.sngl_set_prop_string(unsafe.Pointer(m.__n1), m.__rel0, m.__rel1)`,
		`Free(m.__rel1)`,
		`m.__rel2 = C.CString("enable-emoji-completion")`,
		`C.sngl_set_prop_bool(unsafe.Pointer(m.__n1), m.__rel2, C.int(1))`,
		// An enum-typed property with no setter takes its member's value,
		// which the set's match folds to, through the int helper.
		`m.__rel3 = C.CString("accessible-role")`,
		`C.sngl_set_prop_int(unsafe.Pointer(m.__n2), m.__rel3, C.int(3))`,
		// A property whose GIR setter takes two values takes the generic
		// path, and its object-pointer constructor parameter is nil rather
		// than a bare 0 no cgo pointer type accepts.
		`C.sngl_set_prop_string(unsafe.Pointer(m.__n3), m.__rel4, m.__rel5)`,
		`C.gtk_text_buffer_new(nil)`,
		`m.__rel7 = C.CString("scale")`,
		`C.sngl_set_prop_double(unsafe.Pointer(m.__n4), m.__rel7, C.double(0.25))`,
		// A property that does have a setter still calls it directly.
		`C.gtk_entry_set_max_length((*C.GtkEntry)(unsafe.Pointer(m.__n1)), C.int(12))`,
		// The value is cast to what the setter's parameter is declared as,
		// which GIR does not promise is the property's own type: a gint
		// property whose setter takes a guint.
		`C.gtk_grid_set_column_spacing((*C.GtkGrid)(unsafe.Pointer(m.__n5)), C.guint(C.int(4)))`,
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
		// GIR names this one, but a cgo call cannot satisfy its signature.
		"gtk_text_buffer_set_text",
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
	rulesGIR(t)
	files, err := generateGtk4(t, `
import . "sngl:ui"
import gtk "gir:Gtk-4.0"

window(title="t") {
    gtk.GtkGrid(columnSpacing=4) {}
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

// TestUnsettableProp_IsNotDeclared pins the other half of "never emit a call
// that does not compile": a property whose value type no SNGL value can
// produce is not declared on the widget, so writing it is an unknown prop
// where it is written rather than a call whose argument is the wrong type.
func TestUnsettableProp_IsNotDeclared(t *testing.T) {
	rulesGIR(t)
	doc, err := parser.Parse("t.sngl", []byte(`
import . "sngl:ui"
import gtk "gir:Gtk-4.0"

window(title="t") {
    gtk.GtkLabel(label="hi", ellipsize="end") {}
}
`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	_, diags := checker.Check(doc, &checker.Config{IsMain: true, Platforms: codegen.CollectPlatforms(), Languages: codegen.CollectLangs(), Targets: []ir.StaticTarget{{Platform: "gtk4", Language: "go"}}})
	for _, d := range diags {
		if d.Severity == ir.Error && strings.Contains(d.Msg, `unknown prop "ellipsize" on component GtkLabel`) {
			return
		}
	}
	t.Fatalf("GtkLabel.ellipsize takes a Pango.EllipsizeMode, which no SNGL value produces, and checked as %v", diags)
}

// TestEnumProp_RejectsUnknownMember pins that an enum member is resolved from
// GIR and not guessed: a string naming no member fails the build, where the
// set's match is folded.
func TestEnumProp_RejectsUnknownMember(t *testing.T) {
	rulesGIR(t)
	_, err := lowerGtk4(t, `
import . "sngl:ui"
import gtk "gir:Gtk-4.0"

window(title="t") {
    gtk.GtkLabel(label="hi", justify="middle") {}
}
`)
	if err == nil {
		t.Fatal("generate succeeded; GtkJustification has no member named middle")
	}
	if !strings.Contains(err.Error(), `GtkLabel.justify: "middle" is none of the values`) {
		t.Errorf("error %q does not name the prop and the value", err)
	}
}

// TestSetterlessProps_GeneratedGoCompiles is the point of the generic path: a
// declaration that type-checks in SNGL but emits Go that cannot be built is
// not a usable property. Builds the emitted cgo against the host's GTK.
func TestSetterlessProps_GeneratedGoCompiles(t *testing.T) {
	rulesGIR(t)
	files, err := generateGtk4(t, setterlessSource)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if out, ok := buildEmittedGo(t, files); !ok {
		t.Fatalf("go build on the emitted cgo:\n%s", out)
	}
}

// TestDispatchIsBoundedBothWays pins the lower bound on the callback index.
// It arrives as GTK's user_data pointer round-tripped through
// GPOINTER_TO_INT, so it is not necessarily an index this program handed out;
// an upper bound alone leaves a negative value indexing off the front of the
// slice and panicking inside a C callback, where a Go panic cannot be
// recovered by the caller.
func TestDispatchIsBoundedBothWays(t *testing.T) {
	rulesGIR(t)
	// GtkGrid is outside the wrapped gtk4rt surface, which puts the whole
	// program on the inline-cgo path where snglGoDispatch exists at all.
	files, err := generateGtk4(t, gtk4Window(`            gtk.GtkGrid(columnSpacing=4) {}
            gtk.GtkButton(label="go", @clicked { }) {}`))
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	cb := string(files["callbacks.go"])
	if cb == "" {
		t.Fatal("no callbacks.go was emitted; there is no dispatcher to check")
	}
	if !strings.Contains(cb, "if idx >= 0 && int(idx) < len(snglCallbacks)") {
		t.Errorf("snglGoDispatch is not bounded below:\n%s", cb)
	}
}

// TestConstructOnlyProp_IsNotDeclared pins that a construct-only property is
// not offered at all. GObject answers a post-construction write with a
// g_critical and no change, and this platform creates the widget before it
// assigns any prop — so a declaration would type-check a binding that does
// nothing.
func TestConstructOnlyProp_IsNotDeclared(t *testing.T) {
	rulesGIR(t)
	msgs := checkGtk4(t, gtk4Window(`            gtk.GtkAssistant(useHeaderBar=1) {}`))
	if len(msgs) == 0 {
		t.Fatal("useHeaderBar type-checked; GtkAssistant.use-header-bar is construct-only and nothing can set it")
	}
	if !strings.Contains(strings.Join(msgs, "\n"), "useHeaderBar") {
		t.Errorf("diagnostics do not name the prop: %v", msgs)
	}
	// The control, which has to be a different class: GtkAssistant declares
	// exactly two properties, `pages` (read-only) and `use-header-bar`
	// (construct-only), so nothing on it can show that the rejection above is
	// about being construct-only rather than about being rejected at all.
	// GtkLabel.label is writable, not construct-only, and carries a setter.
	if msgs := checkGtk4(t, gtk4Window(`            gtk.GtkLabel(label="x") {}`)); len(msgs) != 0 {
		t.Errorf("GtkLabel.label is writable and not construct-only, so it must type-check: %v", msgs)
	}
}

// TestNonConnectableSignal_IsNotDeclared pins the other half of the fixed
// trampoline: a signal that passes arguments of its own, or whose return value
// GTK reads, is not declared as an event. Connecting one would hand the
// signal's first argument to snglGoDispatch in place of the callback index.
func TestNonConnectableSignal_IsNotDeclared(t *testing.T) {
	rulesGIR(t)
	for _, tc := range []struct{ node, event string }{
		// One extra argument (GtkEntryIconPosition).
		{`gtk.GtkEntry(@iconPress { })`, "iconPress"},
		// One extra argument (the activated GtkListBoxRow).
		{`gtk.GtkListBox(@rowActivated { })`, "rowActivated"},
		// No arguments, but returns a gboolean the trampoline cannot supply.
		{`gtk.GtkWindow(@closeRequest { })`, "closeRequest"},
	} {
		t.Run(tc.event, func(t *testing.T) {
			msgs := checkGtk4(t, gtk4Window("            "+tc.node+" {}"))
			if len(msgs) == 0 {
				t.Fatalf("@%s type-checked; the trampoline is (instance, user_data) returning void", tc.event)
			}
			if !strings.Contains(strings.Join(msgs, "\n"), tc.event) {
				t.Errorf("diagnostics do not name the event: %v", msgs)
			}
		})
	}
	// The three signals the stdlib overrides actually use must survive.
	for _, ok := range []string{
		`gtk.GtkButton(@clicked { })`,
		`gtk.GtkEntry(@changed { })`,
		`gtk.GtkCheckButton(@toggled { })`,
	} {
		if msgs := checkGtk4(t, gtk4Window("            "+ok+" {}")); len(msgs) != 0 {
			t.Errorf("%s was refused: %v", ok, msgs)
		}
	}
}

// TestUnconstructibleClass_FailsTheBuild pins that a class this platform
// cannot construct refuses the build naming it, rather than emitting a call
// that does not compile or does not link. Both reasons appear: GIR names no
// constructor at all, and it names one whose parameter type comes from a
// namespace this platform does not parse.
func TestUnconstructibleClass_FailsTheBuild(t *testing.T) {
	rulesGIR(t)
	for _, tc := range []struct{ node, want string }{
		// Abstract base: GTK ships no gtk_widget_new to link against.
		{"gtk.GtkWidget", "no constructor"},
		// gtk_drop_target_new(GType, GdkDragAction) — neither zero is
		// spellable from Gtk-4.0.gir alone.
		{"gtk.GtkDropTarget", "namespace this platform does not parse"},
		// gtk_list_store_new is variadic.
		{"gtk.GtkListStore", "no C type of its own"},
	} {
		t.Run(tc.node, func(t *testing.T) {
			_, err := generateGtk4(t, gtk4Window("            "+tc.node+"() {}"))
			if err == nil {
				t.Fatalf("%s generated; it cannot be constructed", tc.node)
			}
			if !strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), tc.node[6:]) {
				t.Errorf("error %q does not name the class and the reason %q", err, tc.want)
			}
		})
	}
}

// A class GIR names no single-widget adder for has a create and no
// #[gen.protocol(append)], and a child written into it is refused where the
// op is made rather than dropped.
func TestClassWithNoAdder_RefusesAChild(t *testing.T) {
	_, err := lowerGtk4(t, gtk4Window(`            gtk.GtkLabel() {
                text(value="inside")
            }`))
	if err == nil {
		t.Fatal("a child of a GtkLabel generated; GTK has no way to put one there")
	}
	if want := "GtkLabel takes no child"; !strings.Contains(err.Error(), want) {
		t.Errorf("error %q does not say %q", err, want)
	}
}
