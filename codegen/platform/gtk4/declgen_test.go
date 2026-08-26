package gtk4

import (
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/codegen/platform/gtk4/gir"
	"git.duckfam.us/jonathan/sngl/internal/checker"
	"git.duckfam.us/jonathan/sngl/internal/parser"
	"git.duckfam.us/jonathan/sngl/ir"
)

// girClassInfoFor is the loaded class metadata for one C type, from whatever
// GIR the host has. Skips when there is none.
func girClassInfoFor(t *testing.T, cType string) *gir.ClassInfo {
	t.Helper()
	skipWithoutGIR(t)
	reg, err := (&Generator{}).gir()
	if err != nil {
		t.Fatal(err)
	}
	info := reg.ByCType[cType]
	if info == nil {
		t.Fatalf("GIR has no class %s", cType)
	}
	return info
}

// TestWidgetSource_DeclaresProps pins what a generated declaration says: the
// C type is the component name and its #[intrinsic] id, a GIR property is a
// prop under the SNGL spelling of its name and the type girTypeToIR gave it,
// and a GLib signal is an event.
func TestWidgetSource_DeclaresProps(t *testing.T) {
	skipWithoutGIR(t)
	reg, err := (&Generator{}).gir()
	if err != nil {
		t.Fatal(err)
	}
	src := string(widgetSource(reg))
	for _, want := range []string{
		"#[intrinsic(\"gtk4:GtkBox\")]\ncomponent GtkBox(",
		"\n    spacing int,\n",
		"\n    orientation dyn,\n",
		"#[intrinsic(\"gtk4:GtkButton\")]\ncomponent GtkButton(",
		"\n    @clicked,\n",
		// Hyphenated GIR names are unwritable in SNGL as they stand, so the
		// declaration carries the camel-cased spelling.
		"\n    defaultWidth int,\n",
		"\n    style dyn,\n",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("generated source is missing %q", want)
		}
	}
	// GTK's own `unit` property is a SNGL keyword; no declaration can carry it.
	if strings.Contains(src, "\n    unit ") {
		t.Error("generated source declares a prop named `unit`, which is a keyword")
	}
}

// TestPackageFS_LoadsAsAPackage pins that the generated source is loadable
// library source and not just text: the checker builds sngl://platforms/gtk4
// from it, the widget declarations come back with their marks and types
// applied, and the `component sngl.X` overrides in gtk4.sngl — which are
// written against those declarations — check clean against them.
func TestPackageFS_LoadsAsAPackage(t *testing.T) {
	skipWithoutGIR(t)
	g := &Generator{}
	if docs := checker.ProvidedDocs(g); len(docs) != 1 {
		t.Fatalf("ProvidedDocs = %d docs; want 1", len(docs))
	}
	doc, err := parser.Parse("t.sngl", []byte("import w \"sngl://platforms/gtk4\"\n"))
	if err != nil {
		t.Fatal(err)
	}
	pkg, diags := checker.Check(doc, &checker.Config{Platforms: []ir.Platform{g}})
	for _, d := range diags {
		t.Errorf("checking against the gtk4 package: %s: %s", d.Pos, d.Msg)
	}
	var loaded *ir.Package
	for _, imp := range pkg.Imports {
		if imp.Path == "sngl://platforms/gtk4" {
			loaded = imp.Pkg
		}
	}
	if loaded == nil {
		t.Fatal("sngl://platforms/gtk4 did not load")
	}
	var box *ir.Component
	for _, c := range loaded.Components {
		if c.Name == "GtkBox" {
			box = c
		}
	}
	if box == nil {
		t.Fatal("sngl://platforms/gtk4 declares no GtkBox")
	}
	if box.Intrinsic != intrinsicPrefix+"GtkBox" {
		t.Errorf("GtkBox.Intrinsic = %q; want %s", box.Intrinsic, intrinsicPrefix+"GtkBox")
	}
	if box.ChildrenType == nil {
		t.Error("GtkBox accepts no children")
	}
	spacing := propNamed(box, "spacing")
	if spacing == nil {
		t.Fatal("GtkBox declares no spacing prop")
	}
	if spacing.Type == nil || spacing.Type.Kind != ir.TypeInt {
		t.Errorf("GtkBox.spacing type = %v; want int", spacing.Type)
	}
	// orientation comes from the GtkOrientable interface, so the GIR merge
	// pass has to have run for the vbox/hbox overrides to check at all.
	if propNamed(box, "orientation") == nil {
		t.Error("GtkBox declares no orientation prop (interface merge)")
	}
}

func propNamed(c *ir.Component, name string) *ir.Prop {
	for _, p := range c.Props {
		if p.Name == name {
			return p
		}
	}
	return nil
}

// TestGirSetter_ReadsMetadataBack pins the half a declaration cannot carry: the
// C setter, and the casts a call to it needs. The emitter recovers these from
// the registry keyed by the C type the intrinsic id names.
func TestGirSetter_ReadsMetadataBack(t *testing.T) {
	box := girClassInfoFor(t, "GtkBox")
	p, ok := girProp(box, "spacing")
	if !ok {
		t.Fatal("girProp(GtkBox, spacing) not found")
	}
	if got := girSetter(box, p); got.Setter != "gtk_box_set_spacing" || got.RecvType != "" || got.ValType != "" {
		t.Errorf("girSetter(GtkBox.spacing) = %+v; want gtk_box_set_spacing with no casts", got)
	}
	// An interface-inherited property binds to the interface's setter and
	// takes the interface as its receiver.
	p, ok = girProp(box, "orientation")
	if !ok {
		t.Fatal("girProp(GtkBox, orientation) not found")
	}
	got := girSetter(box, p)
	if got.Setter != "gtk_orientable_set_orientation" || got.RecvType != "GtkOrientable" || got.ValType != "GtkOrientation" {
		t.Errorf("girSetter(GtkBox.orientation) = %+v; want the GtkOrientable setter with both casts", got)
	}
	// The event a body writes is the SNGL spelling of a GLib signal name.
	if got := girSignal(girClassInfoFor(t, "GtkButton"), "clicked"); got != "clicked" {
		t.Errorf("girSignal(GtkButton, clicked) = %q; want clicked", got)
	}
	if got := girSignal(box, "notAThing"); got != "" {
		t.Errorf("girSignal(GtkBox, notAThing) = %q; want empty", got)
	}
}

// TestWidgetCType pins the key the emitter dispatches on: only this platform's
// intrinsic ids name a widget.
func TestWidgetCType(t *testing.T) {
	for _, tc := range []struct {
		intrinsic, want string
	}{
		{"gtk4:GtkButton", "GtkButton"},
		{"android:Column", ""},
		{"", ""},
	} {
		if got := widgetCType(&ir.Component{Intrinsic: tc.intrinsic}); got != tc.want {
			t.Errorf("widgetCType(%q) = %q; want %q", tc.intrinsic, got, tc.want)
		}
	}
	if got := widgetCType(nil); got != "" {
		t.Errorf("widgetCType(nil) = %q; want empty", got)
	}
}
