package gtk4

import (
	"fmt"
	"maps"
	"slices"
	"strings"
	"unicode"

	"duckfam.us/sngl/codegen/platform/gtk4/gir"
	"duckfam.us/sngl/internal/parser"
	"duckfam.us/sngl/ir"
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
	b.WriteString("import . \"sngl:internal/marks\"\nimport macro \"sngl:macro\"\nimport ui \"sngl:ui\"\nimport gen \"sngl:x/gen\"\nimport c \"sngl:x/c\"\n")
	declared := map[string]bool{}
	casts := map[string]string{}
	children := childOps{natives: map[string]bool{}, ptrs: map[string]bool{}}
	for _, name := range names {
		info := reg.Classes[name]
		if declared[info.CType] {
			continue
		}
		declared[info.CType] = true
		// A GTK widget is a GObject the program can hold: a `#id` on one names
		// something still there while it runs, so a prop read off that handle
		// asks the widget rather than being answered at build time with what
		// the prop was given. Written on every generated declaration because
		// it is true of every one of them -- these are widgets by
		// construction, the GIR classes this platform wraps.
		fmt.Fprintf(&b, "\n#[gen.renders(identity)]\n#[intrinsic(%q)]\ncomponent %s(\n", intrinsicPrefix+info.CType, info.CType)
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
			// A property whose GIR type has no SNGL spelling -- an object, a
			// boxed struct, a flags set -- is not declared: a prop typed to
			// take anything would type-check a value the C setter cannot be
			// handed.
			typ, ok := snglTypeName(p.IRType)
			if !ok && reg.Enums[p.GIRType] != nil {
				// An enumeration is written as its member's name, which the
				// emitter turns into the C constant (girEnumMember).
				typ, ok = "string", true
			}
			if !ok {
				continue
			}
			taken[n] = true
			fmt.Fprintf(&b, "    %s %s,\n", n, typ)
		}
		fmt.Fprintf(&b, "    %s ui.Style = ui.Style{},\n", stylePropName)
		// A signal hands its callback the instance it fired on, as GTK's own
		// signature does (`void toggled(GtkCheckButton *self, gpointer)`), so
		// a handler reads the widget that fired rather than reaching it
		// through a `#id` -- which names no one widget where the node is
		// rendered more than once.
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
			fmt.Fprintf(&b, "    @%s(self %s),\n", n, info.CType)
		}
		for _, prop := range notifyProps[info.CType] {
			if n, ok := snglName("notify-" + prop); ok && !events[n] {
				events[n] = true
				fmt.Fprintf(&b, "    @%s(self %s),\n", n, info.CType)
			}
		}
		// GIR does not say which widgets accept children, and declaring a
		// bound would refuse either every container or every leaf. The
		// emitter knows which parent types have a child-append API.
		// No block: the declaration is what this platform renders the widget
		// from, so a body would be emitted by nobody. The checker refuses one on
		// an #[intrinsic] component.
		fmt.Fprintf(&b, "    %s ...component ui.node,\n) ui.node\n", restSlotName)
		writeCreate(&b, reg, info, casts)
		children.write(&b, info)
		// Each action is a method reached through a `#id` on the widget,
		// declared as the C identifier it is: `bar.pulse()` is
		// gtk_progress_bar_pulse(bar). One whose name a prop, an event or the
		// slot already holds is left out rather than shadowing it.
		for _, a := range info.Actions {
			n, ok := snglName(a.Name)
			if !ok || taken[n] || events[n] {
				continue
			}
			if params, ok := actionParams(a.Params); ok {
				fmt.Fprintf(&b, "\n#[macro.cnative(%q)]\nfunc %s.%s(%s)\n", a.CIdent, info.CType, n, params)
			}
		}
	}
	children.writeCasts(&b)
	castTypes := make([]string, 0, len(casts))
	for cType := range casts {
		castTypes = append(castTypes, cType)
	}
	slices.Sort(castTypes)
	for _, cType := range castTypes {
		fmt.Fprintf(&b, "\n#[macro.cnative(\"C.%s\")]\nstruct %s {}\n", cType, castType(cType))
		fmt.Fprintf(&b, "\n#[macro.cnative(\"C.%s\")]\nconst func %s(v %s) %s\n", cType, castFunc(cType), casts[cType], castType(cType))
	}
	return []byte(b.String())
}

// writeCreate declares how a class is made: its primary constructor as a
// native, and the #[gen.protocol(create)] function lowering splices where the
// program renders the class, calling it with a zero per parameter -- the
// props are written once it returns. A class whose constructor has a
// parameter no zero can be spelled for gets neither (unconstructible says
// why). casts collects the C types a zero crosses through a cast to, with
// the SNGL type the cast takes.
func writeCreate(b *strings.Builder, reg *gir.TypeRegistry, info *gir.ClassInfo, casts map[string]string) {
	if len(info.Constructors) == 0 {
		return
	}
	ctor := pickPrimaryConstructorInfo(info)
	params := make([]string, len(ctor.Params))
	zeros := make([]string, len(ctor.Params))
	used := map[string]string{}
	for i, p := range ctor.Params {
		spelled, err := ctorParam(reg, p)
		if err != nil {
			return
		}
		name, ok := snglName(p.Name)
		if !ok {
			return
		}
		params[i] = name + " " + spelled.typ
		zeros[i] = spelled.zero
		if spelled.cast != "" {
			used[spelled.cast] = spelled.castFrom
		}
	}
	maps.Copy(casts, used)
	native := "_" + ctor.Name
	fmt.Fprintf(b, "\n#[macro.cnative(%q)]\nfunc %s(%s) %s\n", ctor.Name, native, strings.Join(params, ", "), info.CType)
	fmt.Fprintf(b, "\n#[gen.protocol(create)]\nfunc _create_%s() %s {\n    return %s(%s)\n}\n", info.CType, info.CType, native, strings.Join(zeros, ", "))
}

// childOps writes how each class takes a child in and gives one up: the C
// call GIR names, as a native taking the C pointers it declares, and the
// #[gen.protocol(append)] and #[gen.protocol(remove)] functions lowering
// splices where the program puts a node into the class or takes one out. The
// receiver is cast to the class that declares the call, so a subclass reaches
// its parent's adder, and the child to GtkWidget; each cast is a native of its
// own, `(*C.GtkBox)(unsafe.Pointer(w))`, since cgo converts no C pointer to
// another.
type childOps struct {
	natives map[string]bool
	ptrs    map[string]bool
}

// write declares info's append, and its remove where the class accumulates
// children: a class that holds one replaces it and has nothing to remove.
func (o *childOps) write(b *strings.Builder, info *gir.ClassInfo) {
	add := info.ChildAdd
	if add.Func == "" {
		return
	}
	o.op(b, info, "append", add)
	if rm := info.ChildRemove; rm.Func != "" && !add.Single {
		o.op(b, info, "remove", rm)
	}
	// Taking the class itself: handed a family value, Go would box it.
	fmt.Fprintf(b, "\n#[macro.cnative(\"unsafe.Pointer\")]\nconst func _ptr_%s(w %s) c.pointer\n", info.CType, info.CType)
}

func (o *childOps) op(b *strings.Builder, info *gir.ClassInfo, op string, call gir.ChildAdder) {
	owner := adderReceiver(call, info.CType)
	o.ptrs[owner] = true
	o.ptrs["GtkWidget"] = true
	native := "_" + call.Func
	if !o.natives[call.Func] {
		o.natives[call.Func] = true
		fmt.Fprintf(b, "\n#[macro.cnative(%q)]\nfunc %s(parent %s, child %s)\n", call.Func, native, ptrType(owner), ptrType("GtkWidget"))
	}
	fmt.Fprintf(b, "\n#[gen.protocol(%s)]\nfunc _%s_%s(parent %s, child ui.node) {\n    %s(%s(_ptr_%s(parent)), %s(_widget(child)))\n}\n",
		op, op, info.CType, info.CType, native, ptrFunc(owner), info.CType, ptrFunc("GtkWidget"))
}

// writeCasts declares the pointer types and casts the calls written so far
// take, and the way to a child's untyped pointer.
func (o *childOps) writeCasts(b *strings.Builder) {
	if len(o.ptrs) == 0 {
		return
	}
	b.WriteString("\n#[macro.cnative(\"unsafe.Pointer\")]\nconst func _widget(w ui.node) c.pointer\n")
	for _, cType := range slices.Sorted(maps.Keys(o.ptrs)) {
		fmt.Fprintf(b, "\n#[macro.cnative(\"*C.%s\")]\nstruct %s {}\n", cType, ptrType(cType))
		fmt.Fprintf(b, "\n#[macro.cnative(\"(*C.%s)\")]\nconst func %s(p c.pointer) %s\n", cType, ptrFunc(cType), ptrType(cType))
	}
}

// ptrType and ptrFunc name a C pointer type and the cast to it, prefixed as
// castType and castFunc are.
func ptrType(cType string) string { return "_p_" + cType }
func ptrFunc(cType string) string { return "_as_" + cType }

// spelledParam is one constructor parameter as declgen writes it: the SNGL
// type of the native's parameter, the zero the create hands it, and the C
// type it crosses through a cast to, with the SNGL type the cast takes, where
// CMarshal's own conversion would hand C the wrong type.
type spelledParam struct {
	typ, zero      string
	cast, castFrom string
}

// ctorParam spells one constructor parameter. A string is the empty string
// where GTK refuses NULL -- gtk_link_button_new returns no widget at all for
// one -- and null where it takes one; any other pointer is null, which only
// the parameter's c:type identifies, since a GIR name alone does not say
// whether GApplicationFlags is an integer or GFile is a pointer. A number
// crosses as cgo's `int` or `double` where its C type is that, and through a
// cast to its own C type otherwise, as an enumeration always does: cgo
// converts no named C numeric type to another on its own.
//
// It errs for a parameter whose zero cannot be spelled: a varargs or array
// form with no c:type at all, and a named type from a namespace other than
// Gtk, which the platform does not parse and so cannot tell a foreign enum
// from a foreign callback.
func ctorParam(reg *gir.TypeRegistry, p gir.ConstructorParam) (spelledParam, error) {
	switch p.GIRType {
	case "utf8", "filename", "gchararray":
		if p.Nullable {
			return spelledParam{typ: "option[string]", zero: "null"}, nil
		}
		return spelledParam{typ: "string", zero: `""`}, nil
	case "gboolean", "gint", "gint32", "gint64", "guint", "guint32", "guint64", "gsize":
		return numberParam(ctorScalarCast(p), "int", "int", "0"), nil
	case "gdouble", "gfloat":
		return numberParam(ctorScalarCast(p), "double", "float", "0.0"), nil
	}
	if reg != nil {
		if e := reg.Enums[p.GIRType]; e != nil {
			return spelledParam{typ: castType(e.CType), zero: castFunc(e.CType) + "(0)", cast: e.CType, castFrom: "int"}, nil
		}
	}
	// gpointer is void*, which cgo renders as unsafe.Pointer, even though the
	// c:type carries no star.
	if strings.HasSuffix(p.CType, "*") || p.CType == "gpointer" {
		return spelledParam{typ: "option[c.pointer]", zero: "null"}, nil
	}
	if p.CType == "" {
		return spelledParam{}, fmt.Errorf("which GIR describes with no C type of its own")
	}
	return spelledParam{}, fmt.Errorf("whose C type %s is from a namespace this platform does not parse, so its zero value cannot be spelled", p.CType)
}

// numberParam is a number crossing as cType: plain where CMarshal's own
// conversion already makes that type, cgo's `int` from an int and `double`
// from a float.
func numberParam(cType, marshaled, snglType, zero string) spelledParam {
	if cType == marshaled {
		return spelledParam{typ: snglType, zero: zero}
	}
	return spelledParam{typ: castType(cType), zero: castFunc(cType) + "(" + zero + ")", cast: cType, castFrom: snglType}
}

// castType names the struct a C type crosses as, and castFunc the cast to it.
// Prefixed, since a C type may spell a SNGL one: GTK's `float`.
func castType(cType string) string { return "_c_" + cType }
func castFunc(cType string) string { return "_to_" + cType }

// ctorScalarCast is the C type a numeric zero takes to satisfy one
// constructor parameter. It is the parameter's own c:type, not the GIR type's
// canonical cgo spelling -- GTK constructors take guint and float parameters
// of gint- and gdouble-named GIR types. A c:type that is not a plain cgo
// identifier ("unsigned int") falls back to the glib typedef, which cgo does
// accept. Mirrors girScalarCast, which does the same for a property's setter.
func ctorScalarCast(p gir.ConstructorParam) string {
	if c := p.CType; c != "" && !strings.ContainsAny(c, "* ") {
		return c
	}
	return p.GIRType
}

// unconstructible is the build error for a GIR class rendered with no create
// declared for it, saying which of writeCreate's reasons kept it.
func unconstructible(reg *gir.TypeRegistry, cType string, info *gir.ClassInfo) error {
	if len(info.Constructors) == 0 {
		return fmt.Errorf("gtk4: %s cannot be constructed: GTK declares no constructor for it", cType)
	}
	ctor := pickPrimaryConstructorInfo(info)
	for _, p := range ctor.Params {
		if _, err := ctorParam(reg, p); err != nil {
			return fmt.Errorf("gtk4: %s cannot be constructed: %s takes %s %s, %w", cType, ctor.Name, p.GIRType, p.Name, err)
		}
		if _, ok := snglName(p.Name); !ok {
			return fmt.Errorf("gtk4: %s cannot be constructed: %s's parameter %q has no SNGL spelling", cType, ctor.Name, p.Name)
		}
	}
	return fmt.Errorf("gtk4: %s cannot be constructed: the gir: package declares no create for it", cType)
}

// actionParams is the parameter list of an action's declaration, and false
// when a parameter's name has no SNGL spelling. A transfer-full parameter is
// #[moves]: GTK frees what it is handed, so the caller must not.
func actionParams(ps []gir.ActionParam) (string, bool) {
	out := make([]string, len(ps))
	for i, p := range ps {
		n, ok := snglName(p.Name)
		typ, typed := snglTypeName(gir.TypeToIR(p.GIRType))
		if !ok || !typed {
			return "", false
		}
		if p.Moves {
			n = "#[macro.moves] " + n
		}
		out[i] = n + " " + typ
	}
	return strings.Join(out, ", "), true
}

// notifyProps are the properties whose change a widget reports only through
// GObject's `notify::<prop>`, declared as `@notify<Prop>`. GtkSwitch's own
// `state-set` returns a gboolean and passes the state, which the connect
// trampoline cannot carry; GtkDropDown has no signal for a choice at all.
// Every property notifies, and declaring an event per property on every class
// would be most of the generated package.
var notifyProps = map[string][]string{
	"GtkSwitch":   {"active"},
	"GtkDropDown": {"selected"},
}

// notifySignal is the GObject signal behind a `@notify<Prop>` event, or ""
// when event is not one.
func notifySignal(cType, event string) string {
	for _, prop := range notifyProps[cType] {
		if n, ok := snglName("notify-" + prop); ok && n == event {
			return "notify::" + prop
		}
	}
	return ""
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
	// A nested namespace is not a widget: `gtk4:draw:arc` names this
	// platform's drawing op, and read as a C type it resolves to no GIR class
	// and fails the build.
	if strings.Contains(cType, ":") {
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

// snglTypeName is the source spelling of the type TypeToIR resolved, and
// false for a GIR type with no SNGL equivalent.
func snglTypeName(t *ir.Type) (string, bool) {
	if t == nil {
		return "", false
	}
	switch t.Kind {
	case ir.TypeString:
		return "string", true
	case ir.TypeBool:
		return "bool", true
	case ir.TypeInt:
		return "int", true
	case ir.TypeFloat:
		return "float", true
	}
	return "", false
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
