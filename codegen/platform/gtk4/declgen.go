package gtk4

import (
	"fmt"
	"slices"
	"strings"
	"unicode"

	"git.duckfam.us/jonathan/sngl/codegen/platform/gtk4/gir"
	"git.duckfam.us/jonathan/sngl/internal/parser"
	"git.duckfam.us/jonathan/sngl/ir"
)

// widgetSourceFile is the name the synthesized declarations are served under.
// It carries a .sngl suffix because the checker reads a lib package by
// directory listing, and only .sngl files in it are source.
const widgetSourceFile = "widgets.gir.sngl"

// stylePropName is the prop every widget declaration carries beyond what GIR
// describes: the stdlib override bodies forward `style={...style}` onto the
// widget root, and GTK has no property behind it (see the emitter's skip).
const stylePropName = "style"

// widgetSource writes the SNGL source for every class in reg. A GIR property
// becomes a typed prop and a GLib signal an event, both under the SNGL
// spelling of their name; #[intrinsic] names the C type, which is the key the
// emitter reads its per-prop setters back out of the same registry under.
func widgetSource(reg *gir.TypeRegistry) []byte {
	names := make([]string, 0, len(reg.Classes))
	for name, info := range reg.Classes {
		// A GIR <class> without a c:type is not a C type anything can
		// construct, so there is no widget to declare.
		if info.CType != "" {
			names = append(names, name)
		}
	}
	slices.Sort(names)

	var b strings.Builder
	b.WriteString("import . \"sngl:internal/marks\"\nimport ui \"sngl:ui\"\n")
	declared := map[string]bool{}
	for _, name := range names {
		info := reg.Classes[name]
		if declared[info.CType] {
			continue
		}
		declared[info.CType] = true
		fmt.Fprintf(&b, "\n#[intrinsic(%q)]\ncomponent %s(\n", intrinsicPrefix+info.CType, info.CType)
		taken := map[string]bool{stylePropName: true, restSlotName: true}
		for _, p := range info.Props {
			// A construct-only property is not declared: the widget exists
			// before any prop is assigned, and GObject refuses the write
			// after construction. Declaring it would type-check a binding
			// that silently does nothing at runtime.
			if p.ConstructOnly {
				continue
			}
			n, ok := snglName(p.Name)
			if !ok || taken[n] {
				continue
			}
			taken[n] = true
			fmt.Fprintf(&b, "    %s %s,\n", n, snglTypeName(p.IRType))
		}
		fmt.Fprintf(&b, "    %s dyn,\n", stylePropName)
		events := map[string]bool{}
		for _, s := range info.Signals {
			// A signal the fixed trampoline cannot carry is not declared, so
			// binding it is a checker error on an event that does not exist
			// rather than a connection that misdelivers at runtime. The
			// emitter says the same thing for a signal reached some other way.
			if !s.Connectable() {
				continue
			}
			n, ok := snglName(s.Name)
			if !ok || events[n] {
				continue
			}
			events[n] = true
			fmt.Fprintf(&b, "    @%s,\n", n)
		}
		// GIR does not say which widgets accept children, and declaring a
		// bound would refuse either every container or every leaf. The
		// emitter knows which parent types have a child-append API.
		fmt.Fprintf(&b, "    %s ...component ui.ui,\n) ui.ui {}\n", restSlotName)
	}
	return []byte(b.String())
}

// restSlotName is the rest slot every generated widget declares. Reserved
// against the property names too: a GTK property spelling the same word would
// otherwise collide with it in the one parameter list.
const restSlotName = "children"

// intrinsicPrefix namespaces gtk4's component intrinsic ids. The suffix is the
// widget's C type, so the id both dispatches the emitter and names the GIR
// class its metadata comes from.
const intrinsicPrefix = "gtk4:"

// widgetCType returns the C type comp's intrinsic id names, or "" when comp is
// not one of this platform's widget declarations.
func widgetCType(comp *ir.Component) string {
	if comp == nil {
		return ""
	}
	cType, ok := strings.CutPrefix(comp.Intrinsic, intrinsicPrefix)
	if !ok {
		return ""
	}
	return cType
}

// snglName is the SNGL spelling of a GIR property or signal name:
// "default-width" becomes "defaultWidth". A name that lands on a SNGL keyword
// — GTK's own `unit` property — takes a trailing underscore, which is a legal
// identifier here and cannot collide with a camel-cased name (no GIR name
// carries one). It reports false only for a name no identifier can be made
// of at all.
func snglName(girName string) (string, bool) {
	parts := strings.FieldsFunc(girName, func(r rune) bool { return r == '-' || r == '_' })
	if len(parts) == 0 {
		return "", false
	}
	var out strings.Builder
	out.WriteString(parts[0])
	for _, p := range parts[1:] {
		out.WriteString(strings.ToUpper(p[:1]) + p[1:])
	}
	name := out.String()
	if !isSnglIdent(name) {
		return "", false
	}
	if parser.LookupIdent(name) != parser.IDENT {
		name += "_"
	}
	return name, true
}

func isSnglIdent(s string) bool {
	for i, r := range s {
		switch {
		case r == '_' || unicode.IsLetter(r):
		case i > 0 && unicode.IsDigit(r):
		default:
			return false
		}
	}
	return s != ""
}

// snglTypeName is the source spelling of the type girTypeToIR resolved. A GIR
// type with no SNGL equivalent lands on dyn, which accepts the value and
// leaves the emitter to coerce it for the C setter.
func snglTypeName(t *ir.Type) string {
	if t == nil {
		return "dyn"
	}
	switch t.Kind {
	case ir.TypeString:
		return "string"
	case ir.TypeBool:
		return "bool"
	case ir.TypeInt:
		return "int"
	case ir.TypeFloat:
		return "float"
	}
	return "dyn"
}

// --- Reading the metadata back out ---
//
// A generated declaration says which props and events a widget has; what each
// one costs in C — the setter to call, the type to cast the widget to, the
// type to cast the value to — is derived per host install and cannot be
// written in SNGL. The emitter recovers it from the same registry the
// declaration was generated from, keyed by the C type the intrinsic id names.

// girProp returns the GIR property behind a SNGL prop name.
func girProp(info *gir.ClassInfo, snglProp string) (gir.Prop, bool) {
	if info == nil {
		return gir.Prop{}, false
	}
	for _, p := range info.Props {
		if n, ok := snglName(p.Name); ok && n == snglProp {
			return p, true
		}
	}
	return gir.Prop{}, false
}

// girSetter is the C setter GIR names for one property of one class, with the
// casts a call to it needs. An interface-inherited property binds to the
// interface's setter (gtk_orientable_set_orientation) and takes the interface
// as its receiver, not the class; a superclass-inherited one binds to that
// ancestor's setter (gtk_widget_set_visible) and takes the ancestor. Setter is
// empty for a property GIR names no setter for; there is no derived spelling
// to fall back on.
func girSetter(info *gir.ClassInfo, p gir.Prop) gtkSetterEntry {
	e := gtkSetterEntry{Setter: p.Setter}
	switch {
	case p.InterfaceName != "":
		e.RecvType = "Gtk" + p.InterfaceName
	case p.OwnerCType != "":
		e.RecvType = p.OwnerCType
	}
	if gt := p.GIRType; girTypeIsNamedNonPrimitive(gt) {
		e.ValType = "Gtk" + gt
	}
	return e
}

// propValueKind is the shape of value a property takes, which decides both
// how a SNGL value is coerced for the C setter and — for a property with no
// setter — which of the generic GObject helpers can carry it.
type propValueKind int

const (
	// propUnsettable is a property whose value no SNGL expression can
	// produce: a GObject, a boxed struct, a string array, or any type from
	// a namespace other than Gtk (the platform parses only Gtk-4.0.gir, so
	// it cannot tell a foreign enum from a foreign struct).
	propUnsettable propValueKind = iota
	propString
	propBool
	propInt
	propFloat
	// propEnum is a Gtk enumeration or bitfield: an int on the wire, and a
	// SNGL string naming one of its members.
	propEnum
)

// propKind classifies p's value type. reg supplies the enumerations, so a
// nil registry classifies nothing.
func propKind(reg *gir.TypeRegistry, p gir.Prop) propValueKind {
	switch p.GIRType {
	case "utf8", "gchararray", "filename":
		return propString
	case "gboolean":
		return propBool
	case "gint", "gint32", "gint64", "guint", "guint32", "guint64", "gsize":
		return propInt
	case "gdouble", "gfloat":
		return propFloat
	}
	if reg != nil && reg.Enums[p.GIRType] != nil {
		return propEnum
	}
	return propUnsettable
}

// girScalarCast is the cgo type a numeric value takes to satisfy the C
// parameter of p's setter. GIR names that parameter's own C type, which is not
// always the property's: GtkGrid's column-spacing property is a gint while its
// setter takes a guint, and GtkEntry's invisible-char is a guint while its
// setter takes a gunichar. A parameter whose C type is not a plain cgo
// identifier ("unsigned int", a pointer) falls back to the glib typedef of the
// property, which cgo does accept there.
func girScalarCast(p gir.Prop) string {
	if c := p.SetterCType; c != "" && !strings.ContainsAny(c, "* ") {
		return c
	}
	switch p.GIRType {
	case "gint":
		return "int"
	case "gdouble":
		return "double"
	}
	return p.GIRType
}

// girEnumMember is the cgo spelling of the enum member a SNGL string names —
// `C.GTK_ORIENTATION_VERTICAL` for "vertical" on a GtkOrientation property.
// Empty when the type is not an enumeration or names no such member.
func girEnumMember(reg *gir.TypeRegistry, girType, value string) string {
	if reg == nil {
		return ""
	}
	e := reg.Enums[girType]
	if e == nil {
		return ""
	}
	ident := e.Members[strings.ReplaceAll(value, "-", "_")]
	if ident == "" {
		return ""
	}
	return "C." + ident
}

// girSignal returns the GLib signal behind a SNGL event name. The registry
// keeps every signal, connectable or not, so the emitter can tell a name it
// has never heard of from one it is refusing.
func girSignal(info *gir.ClassInfo, event string) (gir.Signal, bool) {
	if info == nil {
		return gir.Signal{}, false
	}
	for _, s := range info.Signals {
		if n, ok := snglName(s.Name); ok && n == event {
			return s, true
		}
	}
	return gir.Signal{}, false
}

// girSignalReturnLabel spells a signal's return type for a diagnostic.
func girSignalReturnLabel(s gir.Signal) string {
	if s.ReturnType == "" || s.ReturnType == "none" {
		return "void"
	}
	return s.ReturnType
}
