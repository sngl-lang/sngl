package gtk4

import (
	"fmt"
	"io/fs"
	"slices"
	"strings"
	"testing/fstest"
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

// PackageFS returns the generated half of sngl://platforms/gtk4: one component
// declaration per GTK widget class the host's introspection data describes.
//
// nil when the GIR file is absent, which is the same answer the rest of the
// platform gives then — no declarations rather than declarations written
// against types nothing can resolve.
func (g *Generator) PackageFS() fs.FS {
	reg, err := g.gir()
	if err != nil {
		return nil
	}
	g.fsOnce.Do(func() {
		g.pkgFS = fstest.MapFS{widgetSourceFile: &fstest.MapFile{Data: widgetSource(reg)}}
	})
	return g.pkgFS
}

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
	b.WriteString("import . \"sngl://internal/marks\"\n")
	declared := map[string]bool{}
	for _, name := range names {
		info := reg.Classes[name]
		if declared[info.CType] {
			continue
		}
		declared[info.CType] = true
		fmt.Fprintf(&b, "\n#[intrinsic(%q)]\ncomponent %s(\n", intrinsicPrefix+info.CType, info.CType)
		taken := map[string]bool{stylePropName: true}
		for _, p := range info.Props {
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
		b.WriteString(") list<component> {}\n")
	}
	return []byte(b.String())
}

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
// "default-width" becomes "defaultWidth". It reports false for a name no
// declaration can carry — GTK's own `unit` property, which is a keyword here.
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
	if !isSnglIdent(out.String()) || parser.LookupIdent(out.String()) != parser.IDENT {
		return "", false
	}
	return out.String(), true
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

// girSetter is the C setter for one property of one class, with the casts it
// needs. An interface-inherited property binds to the interface's namespaced
// setter (gtk_orientable_set_orientation) and takes the interface as its
// receiver, not the class.
func girSetter(info *gir.ClassInfo, p gir.Prop) gtkSetterEntry {
	ns := lowerCType(info.CType)
	e := gtkSetterEntry{}
	if p.InterfaceName != "" {
		ns = lowerCType(p.InterfaceName)
		e.RecvType = "Gtk" + p.InterfaceName
	}
	e.Setter = "gtk_" + ns + "_set_" + strings.ReplaceAll(p.Name, "-", "_")
	if gt := p.GIRType; girTypeIsNamedNonPrimitive(gt) {
		e.ValType = "Gtk" + gt
	}
	return e
}

// girSignal returns the GLib signal name behind a SNGL event name.
func girSignal(info *gir.ClassInfo, event string) string {
	if info == nil {
		return ""
	}
	for _, s := range info.Signals {
		if n, ok := snglName(s.Name); ok && n == event {
			return s.Name
		}
	}
	return ""
}
