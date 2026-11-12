package gtk4

import (
	"fmt"
	"maps"
	"slices"
	"strconv"
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

// lookPropName is the prop every widget declaration carries beyond what GIR
// describes: what gtk4rt.SetLook gives the widget, which the overrides build
// from a ui.Style in style.sngl.
const lookPropName = "look"

// lookDecl declares the record that prop takes, in the package the widgets
// are, since gtk4.sngl imports that package and it can import nothing back.
const lookDecl = `
#[go.native("duckfam.us/sngl/pkg/go/gtk4rt", "gtk4rt.Look")]
struct Look {
    css       string
    width     float
    height    float
    grow      float
    cross     int
    textAlign int
    lines     int
    wrap      int
    hidden    bool
    cursor    string
    justify   int
    spaced    bool
    spacing   int
    grid      bool
    columns   int
}
`

// widgetSource writes the SNGL source for every class in reg. A GIR property
// becomes a typed prop and a GLib signal an event, both under the SNGL
// spelling of their name; #[intrinsic] names the C type, which is the key the
// emitter reads its per-prop setters back out of the same registry under.
//
// cgo selects how a class is made, filled and written: through the C
// functions GIR names, or, by default, through pkg/go/gtk4rt, whose generic
// calls take the class, the property and the adder by name, so a program
// rendering only GIR classes has no cgo of its own.
func widgetSource(reg *gir.TypeRegistry, cgo bool) []byte {
	return []byte(widgetHeader + widgetDecls(reg, cgo, false))
}

// widgetHeader is the imports widgetDecls's declarations are written against.
const widgetHeader = "import marks \"sngl:internal/marks\"\nimport macro \"sngl:macro\"\nimport ui \"sngl:ui\"\nimport gen \"sngl:x/gen\"\nimport go \"sngl:language/go\"\nimport c \"sngl:x/c\"\n"

// WidgetSource is a placed platform's own widget declarations: every class
// of the bundled GIR, made, filled and written through the C functions GTK
// names, under header, the generated file's first lines. Nothing in them is
// gtk4rt's: the platform declares the Look every widget takes and the
// `_setLook` that gives it one, and a box's child is appended by the C
// runtime's sngl_box_append, which aligns it.
func WidgetSource(header string) ([]byte, error) {
	reg, err := gir.Minimal()
	if err != nil {
		return nil, err
	}
	return []byte(header + "\n" + widgetHeader + widgetDecls(reg, true, true)), nil
}

// widgetDecls is the declarations of every class in reg; placed says they are
// a placed platform's (WidgetSource).
func widgetDecls(reg *gir.TypeRegistry, cgo, placed bool) string {
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
	if !placed {
		b.WriteString(lookDecl)
	}
	declared := map[string]bool{}
	casts := map[string]string{}
	children := childOps{cgo: cgo, placed: placed, natives: map[string]bool{}, ptrs: map[string]bool{}, classPtrs: map[string]bool{}}
	for _, name := range names {
		info := reg.Classes[name]
		if declared[info.CType] {
			continue
		}
		// A class made with another object, a GtkApplicationWindow with its
		// application, is the runtime's to make on a placed platform: a Go
		// program cannot hand C an untyped pointer where it takes a typed one.
		if placed && ctorTakesPointer(reg, info, true) {
			continue
		}
		declared[info.CType] = true
		// A GTK widget is a GObject the program can hold: a `#id` on one names
		// something still there while it runs, so a prop read off that handle
		// asks the widget rather than being answered at build time with what
		// the prop was given. Written on every generated declaration because
		// it is true of every one of them -- these are widgets by
		// construction, the GIR classes this platform wraps.
		fmt.Fprintf(&b, "\n#[gen.renders(identity)]\n#[marks.intrinsic(%q)]\ncomponent %s(\n", intrinsicPrefix+info.CType, info.CType)
		taken := map[string]bool{lookPropName: true, restSlotName: true}
		var props []setProp
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
				// prop's set matches to the member's value (enumMatch).
				typ, ok = "string", true
			}
			if !ok {
				continue
			}
			taken[n] = true
			props = append(props, setProp{prop: p, name: n})
			fmt.Fprintf(&b, "    %s %s,\n", n, typ)
		}
		fmt.Fprintf(&b, "    %s Look = Look{},\n", lookPropName)
		// A signal hands its callback the instance it fired on, as GTK's own
		// signature does (`void toggled(GtkCheckButton *self, gpointer)`), so
		// a handler reads the widget that fired rather than reaching it
		// through a `#id` -- which names no one widget where the node is
		// rendered more than once.
		events := map[string]bool{}
		// A placed platform's test fires a signal as GTK would emit it.
		var fires [][2]string
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
			writeEvent(&b, n, s.Name, info.CType)
			if placed && !valuedFires[info.CType+"::"+s.Name] {
				fires = append(fires, [2]string{n, s.Name})
			}
		}
		var notifies [][2]string
		for _, prop := range notifyProps[info.CType] {
			if n, ok := snglName("notify-" + prop); ok && !events[n] {
				events[n] = true
				writeEvent(&b, n, "notify::"+prop, info.CType)
				notifies = append(notifies, [2]string{n, "notify::" + prop})
			}
		}
		// GIR does not say which widgets accept children, and declaring a
		// bound would refuse either every container or every leaf. The
		// emitter knows which parent types have a child-append API.
		// No block: the declaration is what this platform renders the widget
		// from, so a body would be emitted by nobody. The checker refuses one on
		// an #[intrinsic] component.
		fmt.Fprintf(&b, "    %s ...component ui.node,\n) ui.node\n", restSlotName)
		var ptrs map[string]bool
		if placed {
			ptrs = children.ptrs
		}
		writeCreate(&b, reg, info, casts, cgo, ptrs)
		for _, f := range fires {
			fmt.Fprintf(&b, "\n#[gen.fires(%q)]\nfunc _fire_%s_%s(w %s) {\n    _rtSignalEmit(w, %q)\n}\n", f[0], info.CType, f[0], info.CType, f[1])
		}
		switch {
		case placed:
			for _, nf := range notifies {
				fmt.Fprintf(&b, "\n#[gen.protocol(on)]\nfunc _on_%s_%s(w %s, %s func()) {\n    _rtNotifyConnect(w, %q, %s)\n}\n", info.CType, nf[0], info.CType, nf[0], nf[1], nf[0])
			}
		case cgo:
			for _, nf := range notifies {
				writeNotifyOn(&b, info.CType, nf[0], nf[1])
			}
		}
		children.write(&b, info)
		children.writeSets(&b, reg, info, props, casts)
		if placed {
			fmt.Fprintf(&b, "\n#[gen.protocol(set)]\nfunc _set_%s_%s(self %s, %s Look) {\n    _setLook(self, %s)\n}\n", info.CType, lookPropName, info.CType, lookPropName, lookPropName)
		}
		// Each action is a method reached through a `#id` on the widget,
		// declared as the C identifier it is: `bar.pulse()` is
		// gtk_progress_bar_pulse(bar). One whose name a prop, an event or the
		// slot already holds is left out rather than shadowing it.
		for _, a := range info.Actions {
			n, ok := snglName(a.Name)
			if !ok || taken[n] || events[n] {
				continue
			}
			params, ok := actionParams(a.Params)
			if !ok {
				continue
			}
			if !placed {
				fmt.Fprintf(&b, "\n#[macro.cnative(%q)]\nfunc %s.%s(%s)\n", a.CIdent, info.CType, n, params)
				continue
			}
			// On a placed platform the method casts its receiver to the class
			// the C function takes, as C and Go both need: a node is held as
			// the GtkWidget it is.
			children.ptrs[info.CType] = true
			children.classPtr(&b, info.CType)
			native, args := "_"+a.CIdent, []string{fmt.Sprintf("%s(_ptr_%s(w))", ptrFunc(info.CType), info.CType)}
			plain := params
			for _, ap := range a.Params {
				an, _ := snglName(ap.Name)
				args = append(args, an)
			}
			plain = strings.ReplaceAll(plain, "#[macro.moves] ", "")
			sep := ""
			if params != "" {
				sep = ", "
			}
			fmt.Fprintf(&b, "\n#[macro.cnative(%q)]\nfunc %s(w %s%s%s)\n", a.CIdent, native, ptrType(info.CType), sep, params)
			fmt.Fprintf(&b, "\nfunc %s.%s(w %s%s%s) {\n    %s(%s)\n}\n", info.CType, n, info.CType, sep, plain, native, strings.Join(args, ", "))
		}
	}
	children.writeCasts(&b)
	writeConnect(&b, cgo, placed)
	castTypes := make([]string, 0, len(casts))
	for cType := range casts {
		castTypes = append(castTypes, cType)
	}
	slices.Sort(castTypes)
	for _, cType := range castTypes {
		fmt.Fprintf(&b, "\n#[macro.cnative(%q)]\nstruct %s {}\n", cType, castType(cType))
		fmt.Fprintf(&b, "\n#[macro.cnative(%q, cast)]\nconst func %s(v %s) %s\n", cType, castFunc(cType), casts[cType], castType(cType))
	}
	return b.String()
}

// valuedFires is each signal whose handler reads a value off the widget, a
// check button's state or an entry's text, which a test hands its fire: the
// platform declares those fires itself, putting the value in place first.
var valuedFires = map[string]bool{
	"GtkCheckButton::toggled":   true,
	"GtkEntry::changed":         true,
	"GtkEntry::activate":        true,
	"GtkCalendar::day-selected": true,
}

// ctorTakesPointer reports whether info's primary constructor takes a
// pointer, which it is handed as an untyped one (ctorParam). typed leaves out
// a nullable typed pointer, which a placed platform hands its own type
// (typedNull).
func ctorTakesPointer(reg *gir.TypeRegistry, info *gir.ClassInfo, typed bool) bool {
	if len(info.Constructors) == 0 {
		return false
	}
	for _, p := range pickPrimaryConstructorInfo(info).Params {
		if spelled, err := ctorParam(reg, p); err == nil && spelled.typ == "option[c.pointer]" {
			if typed && typedNull(p) != "" {
				continue
			}
			return true
		}
	}
	return false
}

// typedNull is the C type a nullable pointer parameter points to, which a
// placed platform's create hands null as a pointer of: gtk_drop_down_new's
// model is a GListModel *, and Go hands C no untyped pointer where it takes a
// typed one. "" for any other parameter.
func typedNull(p gir.ConstructorParam) string {
	base, ok := strings.CutSuffix(p.CType, "*")
	if !ok || !p.Nullable || strings.ContainsAny(base, "* ") || base == "" {
		return ""
	}
	return base
}

// writeConnect declares signalConnect, which gtk4.sngl's connect protocol
// calls with every handler a program writes on a widget.
//
// By default it is gtk4rt's Connect, which copies the signal into C and
// connects through gtk4rt's trampolines itself, so a program connecting a
// handler has no cgo of its own and wrapped mode's cached build stands. With
// cgo it is GLib's g_signal_connect_data, the handler crossing as a callback
// sngl:x/c's #[context] hands back and released by GLib through the
// #[release] it is handed once the handler is disconnected -- with its
// widget, at the latest: the program is cgo already. GLib calls a
// handler with the instance, a property notification's with the GParamSpec
// after it, and the context last, so each shape is a declaration of its own.
func writeConnect(b *strings.Builder, cgo, placed bool) {
	if placed {
		b.WriteString(connectPlaced)
		return
	}
	if !cgo {
		b.WriteString(connectWrapped)
		return
	}
	b.WriteString(connectCgo)
}

const connectWrapped = `
// Connects handler to w's signal, as gtk4's connect protocol does.
#[go.native("duckfam.us/sngl/pkg/go/gtk4rt", "gtk4rt.Connect")]
func signalConnect(w ui.node, signal string, handler func())
`

const connectCgo = `
#[macro.cnative("GConnectFlags")]
struct _ConnectFlags = int

// GLib's untyped pointer, which cgo spells as a type of its own.
#[macro.cnative("gpointer")]
struct _gpointer {}

#[macro.cnative("gpointer", cast)]
const func _instance(w ui.node) _gpointer

#[macro.cnative("g_signal_connect_data")]
func _signalConnect(instance _gpointer, signal string, #[c.type("GCallback")] handler func(self c.pointer), #[c.context] data _gpointer, #[c.type("GClosureNotify")] #[c.release] destroy c.pointer, flags _ConnectFlags)

#[macro.cnative("g_signal_connect_data")]
func _notifyConnect(instance _gpointer, signal string, #[c.type("GCallback")] handler func(self c.pointer, pspec c.pointer), #[c.context] data _gpointer, #[c.type("GClosureNotify")] #[c.release] destroy c.pointer, flags _ConnectFlags)

// Connects handler to w's signal, as gtk4's connect protocol does. A property
// notification is its event's own protocol (writeNotifyOn).
func signalConnect(w ui.node, signal string, handler func()) {
    _signalConnect(_instance(w), signal, func(self c.pointer) {
        handler()
    }, 0)
}
`

// connectPlaced is writeConnect for a placed platform: the runtime's
// trampolines, which hand the handler only its context whatever GLib hands
// them, so every handler crosses as one callback shape.
const connectPlaced = `
#[macro.cnative("sngl_signal_connect")]
func _rtSignalConnect(
    w ui.node,
    signal string,
    #[c.type("SnglFn")] fn func(),
    #[c.context] ctx c.pointer,
    #[c.type("GDestroyNotify")] #[c.release] rel c.pointer,
)

#[macro.cnative("sngl_notify_connect")]
func _rtNotifyConnect(
    w ui.node,
    signal string,
    #[c.type("SnglFn")] fn func(),
    #[c.context] ctx c.pointer,
    #[c.type("GDestroyNotify")] #[c.release] rel c.pointer,
)

// Emits w's signal as GTK would, which a test fires an event with.
#[macro.cnative("sngl_signal_emit")]
func _rtSignalEmit(w ui.node, signal string)

// Connects handler to w's signal, as the connect protocol does. A property
// notification is its event's own protocol.
func signalConnect(w ui.node, signal string, handler func()) {
    _rtSignalConnect(w, signal, handler)
}
`

// writeNotifyOn declares, with the cgo option, how cType's event n, GTK's
// signal, is connected: GLib hands a property notification's handler the
// GParamSpec before the context, which the one connect does not take.
func writeNotifyOn(b *strings.Builder, cType, n, signal string) {
	fmt.Fprintf(b, "\n#[macro.cnative(\"gpointer\", cast)]\nconst func _instance_%s_%s(w %s) _gpointer\n", cType, n, cType)
	fmt.Fprintf(b, "\n#[gen.protocol(on)]\nfunc _on_%s_%s(w %s, %s func()) {\n    _notifyConnect(_instance_%s_%s(w), %q, func(self c.pointer, pspec c.pointer) {\n        %s()\n    }, 0)\n}\n", cType, n, cType, n, cType, n, signal, n)
}

// writeEvent declares the event n, which GTK knows as signal: the name the
// connect gtk4.sngl declares is handed (#[gen.connects]) where SNGL spells it
// otherwise.
func writeEvent(b *strings.Builder, n, signal, cType string) {
	if signal != n {
		fmt.Fprintf(b, "    #[gen.connects(%q)]\n", signal)
	}
	fmt.Fprintf(b, "    @%s(self %s),\n", n, cType)
}

// writeCreate declares how a class is made: the #[gen.protocol(create)]
// function lowering splices where the program renders the class, calling
// gtk4rt.New with the class's name, or under cgo its primary constructor as a
// native with a zero per parameter -- the props are written once it returns. A class whose constructor has a
// parameter no zero can be spelled for gets neither (unconstructible says
// why). casts collects the C types a zero crosses through a cast to, with
// the SNGL type the cast takes.
//
// ptrs, a placed platform's, collects the C type each nullable pointer
// parameter is handed null as (typedNull).
func writeCreate(b *strings.Builder, reg *gir.TypeRegistry, info *gir.ClassInfo, casts map[string]string, cgo bool, ptrs map[string]bool) {
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
		if base := typedNull(p); ptrs != nil && spelled.typ == "option[c.pointer]" && base != "" {
			ptrs[base] = true
			spelled.typ = "option[" + ptrType(base) + "]"
		}
		params[i] = name + " " + spelled.typ
		zeros[i] = spelled.zero
		if spelled.cast != "" {
			used[spelled.cast] = spelled.castFrom
		}
	}
	if !cgo {
		fmt.Fprintf(b, "\n#[go.native(%q, \"gtk4rt.New\")]\nfunc _rtNew_%s(typeName string) %s\n", gtk4rtPkg, info.CType, info.CType)
		fmt.Fprintf(b, "\n#[gen.protocol(create)]\nfunc _create_%s() %s {\n    return _rtNew_%s(%q)\n}\n", info.CType, info.CType, info.CType, info.CType)
		return
	}
	maps.Copy(casts, used)
	native := "_" + ctor.Name
	fmt.Fprintf(b, "\n#[macro.cnative(%q)]\nfunc %s(%s) %s\n", ctor.Name, native, strings.Join(params, ", "), info.CType)
	fmt.Fprintf(b, "\n#[gen.protocol(create)]\nfunc _create_%s() %s {\n    return %s(%s)\n}\n", info.CType, info.CType, native, strings.Join(zeros, ", "))
}

// childOps writes how each class takes a child in and gives one up: the
// #[gen.protocol(append)] and #[gen.protocol(remove)] functions lowering
// splices where the program puts a node into the class or takes one out,
// calling the C call GIR names through gtk4rt (rtOp), or under cgo as a native
// taking the C pointers it declares. There the receiver is cast to the class
// that declares the call, so a subclass reaches its parent's adder, and the
// child to GtkWidget; each cast is a `#[cnative(…, cast)]` of its own, which
// Go writes `(*C.GtkBox)(unsafe.Pointer(w))`, since cgo converts no C pointer
// to another.
type childOps struct {
	cgo     bool
	placed  bool
	natives map[string]bool
	ptrs    map[string]bool
	// classPtrs is each class a `_ptr_` cast is declared for.
	classPtrs map[string]bool
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
	o.classPtr(b, info.CType)
}

// classPtr declares the cast of a class to its untyped pointer, taking the
// class itself: handed a family value, Go would box it.
func (o *childOps) classPtr(b *strings.Builder, cType string) {
	if o.classPtrs[cType] {
		return
	}
	o.classPtrs[cType] = true
	fmt.Fprintf(b, "\n#[macro.cnative(\"void *\", cast)]\nconst func _ptr_%s(w %s) c.pointer\n", cType, cType)
}

func (o *childOps) op(b *strings.Builder, info *gir.ClassInfo, op string, call gir.ChildAdder) {
	// A box's alignItems is gtk4rt's to apply as a child arrives, so a box
	// takes one through gtk4rt on cgo too: GTK's own call left it unaligned.
	if o.placed && call.Func == "gtk_box_append" {
		o.ptrs["GtkWidget"] = true
		o.classPtr(b, info.CType)
		o.native(b, "_sngl_box_append", fmt.Sprintf("\n#[macro.cnative(\"sngl_box_append\")]\nfunc _sngl_box_append(parent %s, child %s)\n", ptrType("GtkWidget"), ptrType("GtkWidget")))
		fmt.Fprintf(b, "\n#[gen.protocol(%s)]\nfunc _%s_%s(parent %s, child ui.node) {\n    _sngl_box_append(%s(_ptr_%s(parent)), %s(_widget(child)))\n}\n",
			op, op, info.CType, info.CType, ptrFunc("GtkWidget"), info.CType, ptrFunc("GtkWidget"))
		// A child a render slot renders goes before the slot's anchor, which
		// the runtime puts in place as it puts any child of a box.
		if op == "append" {
			o.native(b, "_sngl_box_insert_before", fmt.Sprintf("\n#[macro.cnative(\"sngl_box_insert_before\")]\nfunc _sngl_box_insert_before(parent %s, anchor %s, child %s)\n", ptrType("GtkWidget"), ptrType("GtkWidget"), ptrType("GtkWidget")))
			fmt.Fprintf(b, "\n#[gen.protocol(insertBefore)]\nfunc _insert_%s(parent %s, child ui.node, before ui.node) {\n    _sngl_box_insert_before(%s(_ptr_%s(parent)), %s(_widget(before)), %s(_widget(child)))\n}\n",
				info.CType, info.CType, ptrFunc("GtkWidget"), info.CType, ptrFunc("GtkWidget"), ptrFunc("GtkWidget"))
		}
		return
	}
	if !o.cgo || call.Func == "gtk_box_append" {
		o.rtOp(b, info, op, call)
		return
	}
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

// rtOp is op over gtk4rt: its wrapper of the call where it has one, and
// ChildCall naming the call otherwise.
func (o *childOps) rtOp(b *strings.Builder, info *gir.ClassInfo, op string, call gir.ChildAdder) {
	var body string
	if fn, ok := rtChildWrappers[call.Func]; ok {
		native := "_rt" + fn
		o.native(b, native, fmt.Sprintf("\n#[go.native(%q, \"gtk4rt.%s\")]\nfunc %s(parent ui.node, child ui.node)\n", gtk4rtPkg, fn, native))
		body = fmt.Sprintf("%s(parent, child)", native)
	} else {
		o.native(b, "_rtChildCall", fmt.Sprintf("\n#[go.native(%q, \"gtk4rt.ChildCall\")]\nfunc _rtChildCall(fn string, parent ui.node, child ui.node)\n", gtk4rtPkg))
		body = fmt.Sprintf("_rtChildCall(%q, parent, child)", call.Func)
	}
	fmt.Fprintf(b, "\n#[gen.protocol(%s)]\nfunc _%s_%s(parent %s, child ui.node) {\n    %s\n}\n", op, op, info.CType, info.CType, body)
}

// writeCasts declares the pointer types and casts the calls written so far
// take, and the way to a child's untyped pointer.
func (o *childOps) writeCasts(b *strings.Builder) {
	if len(o.ptrs) == 0 {
		return
	}
	b.WriteString("\n#[macro.cnative(\"void *\", cast)]\nconst func _widget(w ui.node) c.pointer\n")
	for _, cType := range slices.Sorted(maps.Keys(o.ptrs)) {
		fmt.Fprintf(b, "\n#[macro.cnative(\"%s *\")]\nstruct %s {}\n", cType, ptrType(cType))
		fmt.Fprintf(b, "\n#[macro.cnative(\"%s *\", cast)]\nconst func %s(p c.pointer) %s\n", cType, ptrFunc(cType), ptrType(cType))
	}
}

// setProp is a prop a class declares, under its SNGL name.
type setProp struct {
	prop gir.Prop
	name string
}

// writeSets declares, for each prop of info a value can be written to, the
// #[gen.protocol(set)] function lowering splices wherever the program writes
// it. The call is gtk4rt's: its wrapper where it wraps the setter
// (rtSetterTable), and its set by the property's name otherwise. Under cgo it
// is GIR's setter, its receiver cast to the class or interface that declares
// it, and where GTK ships none, the preamble's sngl_set_prop_* by the
// property's name; either set by name is converted by GObject to the type the
// property's GParamSpec declares. A string handed to a C setter is converted
// by CMarshal and freed by ReleaseTemps; an enumeration's member is written as
// its name, which a match turns into the member's value and which folds where
// it is spliced.
func (o *childOps) writeSets(b *strings.Builder, reg *gir.TypeRegistry, info *gir.ClassInfo, props []setProp, casts map[string]string) {
	for _, sp := range props {
		p, n := sp.prop, sp.name
		kind := propKind(reg, p)
		if kind == propUnsettable {
			continue
		}
		var call string
		if rt, ok := rtSetterTable[p.Setter]; ok && p.Setter != "" && !o.cgo {
			call = o.rtSet(b, reg, p, n, rt.fn, rt.kind)
		} else if !o.cgo {
			call = o.rtPropSet(b, reg, p, n, kind)
		} else if p.Setter != "" {
			call = o.cSet(b, reg, info, p, n, casts)
		} else {
			call = o.propSet(b, reg, info, p, n, kind)
		}
		fmt.Fprintf(b, "\n#[gen.protocol(set)]\nfunc _set_%s_%s(self %s, %s %s) {\n    %s\n}\n", info.CType, n, info.CType, n, propTypeName(p), call)
	}
}

// propTypeName is the SNGL type a prop is declared as.
func propTypeName(p gir.Prop) string {
	if typ, ok := snglTypeName(p.IRType); ok {
		return typ
	}
	return "string"
}

func (o *childOps) native(b *strings.Builder, name, decl string) {
	if !o.natives[name] {
		o.natives[name] = true
		b.WriteString(decl)
	}
}

// rtSet is the call of gtk4rt's wrapper of p's setter, which takes the
// widget as a Handle and the value as Go holds it.
func (o *childOps) rtSet(b *strings.Builder, reg *gir.TypeRegistry, p gir.Prop, n, fn string, kind rtSetterKind) string {
	typ, value := map[rtSetterKind]string{rtStr: "string", rtBool: "bool", rtInt: "int", rtFloat: "float", rtOrient: "int"}[kind], n
	if kind == rtOrient {
		value = enumMatch(reg.Enums[p.GIRType], n, func(v int) string { return strconv.Itoa(v) })
	}
	native := "_rt" + fn
	o.native(b, native, fmt.Sprintf("\n#[go.native(%q, \"gtk4rt.%s\")]\nfunc %s(w ui.node, v %s)\n", gtk4rtPkg, fn, native, typ))
	return fmt.Sprintf("%s(self, %s)", native, value)
}

// rtPropSet is the call of gtk4rt's generic set of p by its name.
func (o *childOps) rtPropSet(b *strings.Builder, reg *gir.TypeRegistry, p gir.Prop, n string, kind propValueKind) string {
	fn, typ, value := "SetInt", "int", n
	switch kind {
	case propString:
		fn, typ = "SetString", "string"
	case propBool:
		fn, typ = "SetBool", "bool"
	case propFloat:
		fn, typ = "SetFloat", "float"
	case propEnum:
		value = enumMatch(reg.Enums[p.GIRType], n, func(v int) string { return strconv.Itoa(v) })
	}
	native := "_rt" + fn
	o.native(b, native, fmt.Sprintf("\n#[go.native(%q, \"gtk4rt.%s\")]\nfunc %s(w ui.node, prop string, v %s)\n", gtk4rtPkg, fn, native, typ))
	return fmt.Sprintf("%s(self, %q, %s)", native, p.Name, value)
}

// cSet is the call of p's GIR setter.
func (o *childOps) cSet(b *strings.Builder, reg *gir.TypeRegistry, info *gir.ClassInfo, p gir.Prop, n string, casts map[string]string) string {
	recv := info.CType
	if r := girSetter(info, p).RecvType; r != "" {
		recv = r
	}
	o.ptrs[recv] = true
	o.classPtr(b, info.CType)
	var typ, value string
	switch propKind(reg, p) {
	case propString:
		typ, value = "string", n
		if p.SetterNullable {
			typ = "option[string]"
		}
	case propBool:
		cType := girScalarCast(p)
		casts[cType] = "int"
		typ, value = castType(cType), fmt.Sprintf("%s(%s ? 1 : 0)", castFunc(cType), n)
	case propInt:
		sp := numberParam(girScalarCast(p), "int", "int", n)
		if sp.cast != "" {
			casts[sp.cast] = sp.castFrom
		}
		typ, value = sp.typ, sp.zero
	case propFloat:
		sp := numberParam(girScalarCast(p), "double", "float", n)
		if sp.cast != "" {
			casts[sp.cast] = sp.castFrom
		}
		typ, value = sp.typ, sp.zero
	case propEnum:
		e := reg.Enums[p.GIRType]
		casts[e.CType] = "int"
		typ = castType(e.CType)
		value = enumMatch(e, n, func(v int) string { return fmt.Sprintf("%s(%d)", castFunc(e.CType), v) })
	}
	native := "_" + p.Setter
	o.native(b, native, fmt.Sprintf("\n#[macro.cnative(%q)]\nfunc %s(w %s, v %s)\n", p.Setter, native, ptrType(recv), typ))
	return fmt.Sprintf("%s(%s(_ptr_%s(self)), %s)", native, ptrFunc(recv), info.CType, value)
}

// propSet is the call of the preamble helper that sets p by its name,
// for a property GTK ships no setter for.
func (o *childOps) propSet(b *strings.Builder, reg *gir.TypeRegistry, info *gir.ClassInfo, p gir.Prop, n string, kind propValueKind) string {
	fn := gObjectSetFn[kind]
	typ, value := map[propValueKind]string{propString: "string", propBool: "int", propInt: "int", propFloat: "float", propEnum: "int"}[kind], n
	switch kind {
	case propBool:
		value = n + " ? 1 : 0"
	case propEnum:
		value = enumMatch(reg.Enums[p.GIRType], n, func(v int) string { return strconv.Itoa(v) })
	}
	o.classPtr(b, info.CType)
	native := "_" + fn
	o.native(b, native, fmt.Sprintf("\n#[macro.cnative(%q)]\nfunc %s(w c.pointer, name string, v %s)\n", fn, native, typ))
	return fmt.Sprintf("%s(_ptr_%s(self), %q, %s)", native, info.CType, p.Name, value)
}

// enumMatch is a match of n, a member's name, to the member's value as
// spelled: a member GIR names with an underscore is written with a hyphen
// too, as a GTK property's own name is.
func enumMatch(e *gir.EnumInfo, n string, spell func(int) string) string {
	names := slices.Sorted(maps.Keys(e.Values))
	slices.SortStableFunc(names, func(a, b string) int { return e.Values[a] - e.Values[b] })
	var arms strings.Builder
	for _, m := range names {
		pats := []string{strconv.Quote(m)}
		if h := strings.ReplaceAll(m, "_", "-"); h != m {
			pats = append(pats, strconv.Quote(h))
		}
		fmt.Fprintf(&arms, "        %s => %s\n", strings.Join(pats, ", "), spell(e.Values[m]))
	}
	fallback := 0
	if len(names) > 0 {
		fallback = e.Values[names[0]]
	}
	return fmt.Sprintf("(match %s {\n%s        else => %s\n    })", n, arms.String(), spell(fallback))
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

// girSetter is the C setter GIR names for one property of one class, with
// the receiver a call to it is cast to. An interface-inherited property binds
// to the interface's setter (gtk_orientable_set_orientation) and takes the
// interface as its receiver, not the class; a superclass-inherited one binds
// to that ancestor's setter (gtk_widget_set_visible) and takes the ancestor.
// Setter is empty for a property GIR names no setter for; there is no derived
// spelling to fall back on.
func girSetter(info *gir.ClassInfo, p gir.Prop) gtkSetterEntry {
	e := gtkSetterEntry{Setter: p.Setter}
	switch {
	case p.InterfaceName != "":
		e.RecvType = "Gtk" + p.InterfaceName
	case p.OwnerCType != "":
		e.RecvType = p.OwnerCType
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
