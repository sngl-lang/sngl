package gtk4

import (
	"slices"
	"sort"
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
	// GTK's own `unit` property is a SNGL keyword, so it is declared under
	// the deterministic rewrite rather than dropped.
	if strings.Contains(src, "\n    unit ") {
		t.Error("generated source declares a prop named `unit`, which is a keyword")
	}
	if !strings.Contains(src, "\n    unit_ ") {
		t.Error("generated source declares no `unit_` prop; GtkPrintOperation.unit has nowhere else to land")
	}
}

// TestSnglName_RewritesKeywords pins the rewrite itself, including that it is
// driven by the lexer rather than by a list of names.
func TestSnglName_RewritesKeywords(t *testing.T) {
	for _, tc := range []struct{ gir, want string }{
		{"default-width", "defaultWidth"},
		{"unit", "unit_"},
		{"for", "for_"},
		{"platform", "platform_"},
		{"icon_name", "iconName"},
	} {
		got, ok := snglName(tc.gir)
		if !ok || got != tc.want {
			t.Errorf("snglName(%q) = %q, %v; want %q", tc.gir, got, ok, tc.want)
		}
	}
	if _, ok := snglName(""); ok {
		t.Error("snglName(\"\") reported a name")
	}
}

// TestSnglName_NoCollisions sweeps the host's whole widget set: camel-casing
// and the keyword rewrite together must not make two GIR names on one class
// into one SNGL name, since the declaration would then carry one prop where
// GTK has two and the emitter would set the wrong one.
func TestSnglName_NoCollisions(t *testing.T) {
	skipWithoutGIR(t)
	reg, err := (&Generator{}).gir()
	if err != nil {
		t.Fatal(err)
	}
	rewritten := 0
	// reserved records every GIR property whose SNGL spelling collides with
	// a name the declaration reserves for the platform's own prop. declgen
	// silently drops those, so a sweep that does not model the reservation
	// would report no collision either way — and the two real ones would go
	// unnoticed if the reservation were ever removed.
	var reserved []string
	for _, info := range reg.Classes {
		if info.CType == "" {
			continue
		}
		props := map[string]string{}
		events := map[string]string{}
		for _, p := range info.Props {
			if p.ConstructOnly {
				continue
			}
			n, ok := snglName(p.Name)
			if !ok {
				t.Errorf("%s.%s has no SNGL spelling", info.CType, p.Name)
				continue
			}
			if strings.HasSuffix(n, "_") {
				rewritten++
			}
			if n == stylePropName {
				reserved = append(reserved, info.CType+"."+p.Name)
				continue
			}
			if prev, dup := props[n]; dup && prev != p.Name {
				t.Errorf("%s: %q and %q both spell %q", info.CType, prev, p.Name, n)
			}
			props[n] = p.Name
		}
		for _, sig := range info.Signals {
			if !sig.Connectable() {
				continue
			}
			n, ok := snglName(sig.Name)
			if !ok {
				t.Errorf("%s signal %s has no SNGL spelling", info.CType, sig.Name)
				continue
			}
			if prev, dup := events[n]; dup && prev != sig.Name {
				t.Errorf("%s: signals %q and %q both spell %q", info.CType, prev, sig.Name, n)
			}
			events[n] = sig.Name
		}
	}
	if rewritten == 0 {
		t.Error("no name needed the keyword rewrite; the sweep is asserting nothing")
	}
	// The reservation is load-bearing, and these five are what it costs.
	// GtkCellRendererText and GtkTextTag each have a real GTK property named
	// style that the platform's own style prop takes the name of; the other
	// three are GtkCellRendererText's subclasses, which inherit it. If this
	// list empties, the reservation is dropping nothing and the seeding in
	// widgetSource is dead; if it grows, a property stopped being declared
	// without anyone deciding that.
	sort.Strings(reserved)
	want := []string{
		"GtkCellRendererAccel.style",
		"GtkCellRendererCombo.style",
		"GtkCellRendererSpin.style",
		"GtkCellRendererText.style",
		"GtkTextTag.style",
	}
	if !slices.Equal(reserved, want) {
		t.Errorf("properties dropped by the %q reservation = %v; want %v", stylePropName, reserved, want)
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
	// A setter is the name GIR gives it, which for eleven GTK properties is
	// not the one the class and property names would produce.
	for _, tc := range []struct{ cType, prop, want string }{
		{"GtkImage", "iconName", "gtk_image_set_from_icon_name"},
		{"GtkNotebook", "page", "gtk_notebook_set_current_page"},
		{"GtkWindow", "focusWidget", "gtk_window_set_focus"},
		{"GtkGLArea", "useEs", "gtk_gl_area_set_use_es"},
	} {
		info := girClassInfoFor(t, tc.cType)
		p, ok := girProp(info, tc.prop)
		if !ok {
			t.Errorf("girProp(%s, %s) not found", tc.cType, tc.prop)
			continue
		}
		if got := girSetter(info, p).Setter; got != tc.want {
			t.Errorf("girSetter(%s.%s) = %q; want %q", tc.cType, tc.prop, got, tc.want)
		}
	}
	// A property GIR names no setter for has none — there is no derived
	// spelling to fall back on, and gtk_image_set_file does not exist.
	img := girClassInfoFor(t, "GtkImage")
	p, ok = girProp(img, "file")
	if !ok {
		t.Fatal("girProp(GtkImage, file) not found")
	}
	if got := girSetter(img, p).Setter; got != "" {
		t.Errorf("girSetter(GtkImage.file) = %q; want none", got)
	}
	// It is the static table that answers for it, and that answer is the
	// setter the gtk4 override bodies rely on.
	if got := gtkSetterFor("GtkImage", "file").Setter; got != "gtk_image_set_from_file" {
		t.Errorf("gtkSetterFor(GtkImage.file) = %q; want gtk_image_set_from_file", got)
	}
	// The event a body writes is the SNGL spelling of a GLib signal name.
	if got, ok := girSignal(girClassInfoFor(t, "GtkButton"), "clicked"); !ok || got.Name != "clicked" {
		t.Errorf("girSignal(GtkButton, clicked) = %+v, %v; want clicked, true", got, ok)
	}
	if got, ok := girSignal(box, "notAThing"); ok {
		t.Errorf("girSignal(GtkBox, notAThing) = %+v, true; want not found", got)
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

// TestClassInheritance_Merges pins the parent-chain merge and its precedence.
// Every GTK widget is a GtkWidget, and its properties and signals are declared
// only there — without this a GtkBox has no visible, no hexpand and no
// tooltip-text, and every stdlib override that needs one is unwritable.
func TestClassInheritance_Merges(t *testing.T) {
	box := girClassInfoFor(t, "GtkBox")

	// An inherited property is on the class, and a call to its setter casts
	// the widget to the ancestor that declares it: gtk_widget_set_tooltip_text
	// takes a GtkWidget*, and cgo refuses a GtkBox* there.
	p, ok := girProp(box, "tooltipText")
	if !ok {
		t.Fatal("GtkBox has no tooltipText; GtkWidget's properties did not merge")
	}
	if got := girSetter(box, p); got.Setter != "gtk_widget_set_tooltip_text" || got.RecvType != "GtkWidget" {
		t.Errorf("girSetter(GtkBox.tooltipText) = %+v; want gtk_widget_set_tooltip_text cast to GtkWidget", got)
	}

	// A class's own declaration wins over the one it would inherit, so the
	// setter is the class's own and there is no ancestor cast.
	cell := girClassInfoFor(t, "GtkColumnViewCell")
	p, ok = girProp(cell, "child")
	if !ok {
		t.Fatal("GtkColumnViewCell has no child prop")
	}
	if got := girSetter(cell, p); got.Setter != "gtk_column_view_cell_set_child" || got.RecvType != "" {
		t.Errorf("girSetter(GtkColumnViewCell.child) = %+v; want its own setter with no cast, not GtkListItem's", got)
	}

	// One member, one entry: the merge must not stack a shadowed copy behind
	// the class's own. girProp answers with the first match, so a duplicate
	// would be invisible there and would surface only as a declaration
	// carrying the same prop twice, which does not parse.
	if n := countProps(cell.Props, "child"); n != 1 {
		t.Errorf("GtkColumnViewCell carries %d child properties; want 1", n)
	}

	// Inheritance composes with the interface merge rather than fighting it,
	// and the order the two passes run in is what makes it work.
	// GtkListBase declares an orientation property of its own but no
	// set_orientation method, so its own entry has no setter at all; the
	// working one is GtkOrientable's, which the interface merge puts on
	// GtkListView. Run the parent merge first and GtkListView would take its
	// parent's setterless entry, and the interface merge would then skip the
	// name as already present — leaving a prop reachable only through the
	// generic GObject path.
	lb := girClassInfoFor(t, "GtkListBase")
	p, ok = girProp(lb, "orientation")
	if !ok || p.Setter != "" {
		t.Errorf("GtkListBase.orientation = %+v, %v; want its own setterless entry", p, ok)
	}
	lv := girClassInfoFor(t, "GtkListView")
	p, ok = girProp(lv, "orientation")
	if !ok {
		t.Fatal("GtkListView has no orientation")
	}
	// The interface tag is the one thing set on it, so girSetter's preference
	// for InterfaceName over OwnerCType never has to break a tie: the merge
	// decided which of the two a prop carries.
	if p.InterfaceName != "Orientable" || p.OwnerCType != "" {
		t.Errorf("GtkListView.orientation carries iface %q and owner %q; want the interface tag alone", p.InterfaceName, p.OwnerCType)
	}
	if got := girSetter(lv, p); got.Setter != "gtk_orientable_set_orientation" || got.RecvType != "GtkOrientable" {
		t.Errorf("girSetter(GtkListView.orientation) = %+v; want the GtkOrientable setter and cast", got)
	}

	// An inherited member carries its own flags, so the three rules that
	// withhold a member apply to it on those and not on the child's.
	//
	// Arity: GtkEntryBuffer.text's setter takes (text, len), which a
	// one-value cgo call site cannot reach, and the subclass inherits the
	// cleared name rather than a callable-looking one.
	buf := girClassInfoFor(t, "GtkPasswordEntryBuffer")
	p, ok = girProp(buf, "text")
	if !ok {
		t.Fatal("GtkPasswordEntryBuffer has no text prop")
	}
	if p.SetterValParams != 2 || p.Setter != "" {
		t.Errorf("GtkPasswordEntryBuffer.text = setter %q over %d params; want no setter recorded for a two-value one", p.Setter, p.SetterValParams)
	}

	// Construct-only: GtkWidget.css-name is inherited by every widget class,
	// and no widget may declare it — the widget exists before any prop is
	// assigned and GObject refuses the write.
	if p, ok := girProp(box, "cssName"); !ok {
		t.Fatal("GtkBox did not inherit css-name at all")
	} else if !p.ConstructOnly {
		t.Error("GtkWidget.css-name arrived on GtkBox without its construct-only flag")
	}

	// Connectability: GtkWidget::query-tooltip carries four arguments the
	// (instance, user_data) trampoline has no room for, and GtkWidget::destroy
	// is a void signal with none — so exactly one of the two is inheritable as
	// an event, and the reason travels with the signal rather than being
	// re-derived from the class it lands on.
	for _, tc := range []struct {
		signal string
		want   bool
	}{{"queryTooltip", false}, {"destroy", true}} {
		sig, ok := girSignal(box, tc.signal)
		if !ok {
			t.Errorf("GtkBox did not inherit the %s signal", tc.signal)
			continue
		}
		if sig.Connectable() != tc.want {
			t.Errorf("GtkBox.%s connectable = %v; want %v", tc.signal, sig.Connectable(), tc.want)
		}
	}
	// And the return type is carried for its own sake, not as a proxy for the
	// argument count: GtkWindow::close-request takes no arguments and is
	// unconnectable only because GTK reads a gboolean back out of it, which
	// the void trampoline never wrote.
	appWin := girClassInfoFor(t, "GtkApplicationWindow")
	sig, ok := girSignal(appWin, "closeRequest")
	if !ok {
		t.Fatal("GtkApplicationWindow did not inherit GtkWindow's close-request signal")
	}
	if sig.Params != 0 || sig.ReturnType != "gboolean" || sig.Connectable() {
		t.Errorf("inherited close-request = %d params returning %q, connectable %v; want 0 params returning gboolean and not connectable",
			sig.Params, sig.ReturnType, sig.Connectable())
	}

	// And the declaration says the same: an inherited property and event are
	// written, a construct-only or unconnectable one is not.
	reg, err := (&Generator{}).gir()
	if err != nil {
		t.Fatal(err)
	}
	decl := componentDecl(t, string(widgetSource(reg)), "GtkBox")
	for _, want := range []string{"\n    tooltipText string,\n", "\n    hexpand bool,\n", "\n    visible bool,\n", "\n    @destroy,\n"} {
		if !strings.Contains(decl, want) {
			t.Errorf("the GtkBox declaration is missing %q", want)
		}
	}
	for _, unwanted := range []string{"\n    cssName ", "\n    @queryTooltip,\n"} {
		if strings.Contains(decl, unwanted) {
			t.Errorf("the GtkBox declaration carries %q, which the merge must withhold", unwanted)
		}
	}
}

// componentDecl slices out one component declaration from generated source, so
// an assertion about a class's props cannot be satisfied by another class's.
func componentDecl(t *testing.T, src, name string) string {
	t.Helper()
	head := "\ncomponent " + name + "("
	i := strings.Index(src, head)
	if i < 0 {
		t.Fatalf("generated source declares no %s", name)
	}
	rest := src[i:]
	end := strings.Index(rest, ") list<component> {}")
	if end < 0 {
		t.Fatalf("the %s declaration does not end", name)
	}
	return rest[:end]
}

// countProps is how many entries in props carry this GIR name.
func countProps(props []gir.Prop, name string) int {
	n := 0
	for _, p := range props {
		if p.Name == name {
			n++
		}
	}
	return n
}
