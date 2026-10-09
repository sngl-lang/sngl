package gir

import (
	"bytes"
	"encoding/xml"
	"io"
	"os"
	"sort"

	"duckfam.us/sngl/ir"
)

// ConstructorParam is one parameter in a GIR constructor.
type ConstructorParam struct {
	Name string
	// GIRType is the raw GIR <type name=…> value, e.g. "utf8",
	// "gint", "Orientation". Used by codegen to pick the right cgo
	// type and emit typed zero values.
	GIRType string
	// CType is the raw GIR <type c:type=…> value, e.g. "const char*",
	// "GtkTextTagTable*", "GApplicationFlags". Its trailing "*" is the only
	// thing in GIR that distinguishes a pointer parameter from an integer
	// typedef of a name that looks just as opaque: Gio.File is a GFile*,
	// Gio.ApplicationFlags is a GApplicationFlags. Empty for a parameter
	// GIR describes with <array> or <varargs> instead of a <type>.
	CType  string
	IRType *ir.Type
}

// ConstructorInfo holds the C identifier and parameters for a widget constructor.
type ConstructorInfo struct {
	Name   string             // e.g. "gtk_button_new_with_label"
	Params []ConstructorParam // positional params
}

// Prop is a writable property on a GIR class.
type Prop struct {
	Name string
	// Setter is the C identifier of the property's setter function, taken
	// from the GIR <property setter="…"> reference and resolved through the
	// owner's method list. Empty for a property GIR names no setter for —
	// construct-only ones, and ones only reachable through g_object_set.
	// It cannot be derived from the class and property names: eleven GTK
	// setters do not follow that shape (gtk_image_set_from_icon_name,
	// gtk_notebook_set_current_page, …).
	Setter string
	// SetterCType is the C type of the value parameter of that setter. GIR
	// does not promise it matches the property's own type — GtkGrid's
	// column-spacing property is a gint and its setter takes a guint — and
	// it is the setter's parameter that a cgo call has to satisfy.
	SetterCType string
	// SetterValParams is how many parameters beyond the instance the setter
	// GIR named takes. A cgo call site passes exactly one value, so only 1
	// is callable: gtk_text_buffer_set_text takes (text, len) and
	// gtk_actionable_set_action_target is variadic, which cgo cannot call at
	// all. Setter is cleared for those, and this records why — 0 means GIR
	// named no setter, anything but 1 means it named an uncallable one.
	SetterValParams int
	// InterfaceName, when non-empty, identifies the interface this prop was
	// inherited from (e.g. "Orientable"). Its setter takes the interface as
	// its receiver, so the codegen casts the widget to that type rather than
	// to its own class. Empty for direct class properties.
	InterfaceName string
	// OwnerCType, when non-empty, is the C type of the ancestor class this
	// prop was inherited from ("GtkWidget" for tooltip-text on a GtkBox).
	// Its setter takes that ancestor as its receiver — gtk_widget_set_visible
	// wants a GtkWidget*, and cgo rejects a GtkBox* there — so the codegen
	// casts to it. Empty for a property the class declares itself; an
	// interface-inherited one is named by InterfaceName instead, which wins
	// because the interface setter is the one GIR pointed at.
	OwnerCType string
	// ConstructOnly is GIR's construct-only="1": the property is writable,
	// but only as an argument to g_object_new. GObject refuses a later
	// g_object_set_property with a g_critical and leaves the value
	// unchanged, and this platform creates the widget before it assigns any
	// prop — so there is no order in which such a property can be set, and
	// neither a setter call nor the generic path reaches it.
	ConstructOnly bool
	// GIRType is the raw GIR <type name=…> value for the property's
	// value type, e.g. "utf8", "gint", "Orientation".
	GIRType string
	IRType  *ir.Type
}

// EnumInfo holds the members of a GIR <enumeration> or <bitfield>.
// Members maps the GIR member name ("vertical") to its C identifier
// ("GTK_ORIENTATION_VERTICAL"), which is how a SNGL string value reaches
// an enum-typed property.
type EnumInfo struct {
	CType   string
	Members map[string]string
}

// Signal is a GLib signal on a GIR class.
type Signal struct {
	Name string
	// Params is how many arguments the signal passes beyond the instance.
	// The C trampoline SNGL connects has a fixed (instance, user_data)
	// signature, so anything above zero would shift the user_data the
	// callback index rides in — GTK would hand the first real argument to
	// the dispatcher and it would fire an unrelated callback.
	Params int
	// ReturnType is the GIR name of the signal's return value, "none" for a
	// void signal. The trampoline returns void, so a signal GTK reads a
	// return value from (GtkWindow's close-request is a gboolean) would have
	// that value read out of an uninitialized register.
	ReturnType string
}

// Connectable reports whether the fixed (instance, user_data) trampoline is a
// correct callback for this signal. Only a void signal with no arguments of
// its own is; see the field comments for what goes wrong otherwise.
func (s Signal) Connectable() bool {
	return s.Params == 0 && (s.ReturnType == "none" || s.ReturnType == "")
}

// ClassInfo holds resolved metadata for one GTK widget class.
type ClassInfo struct {
	CType        string            // e.g. "GtkButton"
	Constructor  ConstructorInfo   // first constructor found (kept for legacy callers)
	Constructors []ConstructorInfo // every constructor found, in declaration order
	Props        []Prop            // writable properties (after interface and parent merge)
	Signals      []Signal
	Implements   []string // names of interfaces declared via <implements>
	// Parent is the GIR name of the superclass, from <class parent="…">.
	// A dotted name ("GObject.Object") is in another namespace, which this
	// parser does not read, so the chain ends there.
	Parent string
	// Actions are the class's own methods that take nothing beyond the
	// instance and return nothing -- gtk_progress_bar_pulse -- by GIR name,
	// sorted. A program calls one through a `#id`; a method taking a value is
	// a setter a prop already reaches, and one returning a value is a query
	// nothing reads, so neither is here.
	Actions []Action
	// ChildAdd is how this class takes a child widget, or the zero value when
	// it takes none this way. Derived from the introspection data rather than
	// listed in Go: a hard-coded list of container types silently re-parented
	// the children of every class outside it.
	ChildAdd ChildAdder
}

// Action is a method a program calls for its effect alone.
type Action struct {
	Name   string // GIR name, e.g. "pulse"
	CIdent string // C identifier, e.g. "gtk_progress_bar_pulse"
}

// ChildAdder is the C function a container's child is added through, and
// whether it holds one child or many.
//
// GTK's container APIs are not uniform, so this covers the three shapes that
// take a widget and nothing else: `append(child)` and `add_child(child)`,
// which accumulate, and a writable `child` property, which replaces. A class
// whose only way in needs more arguments -- gtk_grid_attach takes four, and
// gtk_notebook_append_page takes a tab label -- is not expressible as one call
// and is reported rather than guessed at.
type ChildAdder struct {
	// Func is the C identifier, e.g. "gtk_box_append".
	Func string
	// Single is true when the call replaces the child rather than appending:
	// a second child takes the first one's place, which is what the GTK
	// setter does.
	Single bool
}

// InterfaceInfo holds resolved metadata for one GIR interface.
type InterfaceInfo struct {
	Props   []Prop // writable properties
	Signals []Signal
}

// TypeRegistry maps GIR class name (e.g. "Button") to ClassInfo.
type TypeRegistry struct {
	Classes    map[string]*ClassInfo
	Interfaces map[string]*InterfaceInfo
	// Enums holds every <enumeration> and <bitfield> in the namespace,
	// keyed by GIR name ("Orientation") — the same spelling a property's
	// type carries.
	Enums map[string]*EnumInfo
	// ByCType indexes the same entries under the C type name
	// ("GtkButton"), which is what generated declarations and emitted
	// code carry. Classes with no c:type are absent.
	ByCType map[string]*ClassInfo
}

// ParseGIR reads and parses a GIR file at path.
func ParseGIR(path string) (*TypeRegistry, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return ParseGIRBytes(data)
}

// ParseGIRBytes parses GIR XML from a byte slice (used in tests).
// It uses a token-based decoder so that namespace-prefixed elements
// like <glib:signal> are matched by local name only.
func ParseGIRBytes(data []byte) (*TypeRegistry, error) {
	reg := &TypeRegistry{
		Classes:    make(map[string]*ClassInfo),
		Interfaces: make(map[string]*InterfaceInfo),
		Enums:      make(map[string]*EnumInfo),
		ByCType:    make(map[string]*ClassInfo),
	}
	dec := xml.NewDecoder(bytes.NewReader(data))

	var (
		inNamespace    bool
		inClass        bool
		inInterface    bool
		inCtor         bool // inside a <constructor> element
		acceptCtor     bool // whether this is the first (accepted) constructor
		inCtorParams   bool
		inParam        bool
		inProp         bool
		inMethod       bool
		inMethodReturn bool
		inMethodParams bool
		inMethodParam  bool
		// inPropArray records that the property being parsed carries an
		// <array> type (GStrv and friends). Its inner <type> names the
		// element, not the property, so the property keeps no GIR type at
		// all — nothing may read it as that primitive.
		inPropArray bool

		inEnum bool

		// A <glib:signal> is parsed across its own <return-value> and
		// <parameters> children, which is why it needs a scope of its own
		// rather than an attribute read at the start element.
		currentSignal  *Signal
		inSignalParams bool
		inSignalReturn bool

		currentClass     *ClassInfo
		currentInterface *InterfaceInfo
		currentEnum      *EnumInfo
		// methods maps a class or interface to its methods by GIR name, so a
		// <property setter="set_focus"> reference can be resolved to the C
		// function it names and the type that function takes.
		methods       = map[any]map[string]*methodInfo{}
		currentMethod *methodInfo

		paramName         string
		paramTypeName     string
		propName          string
		propWritable      string
		propConstructOnly string
		propSetter        string
		propTypeName      string
		paramCType        string
	)

	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			local := t.Name.Local
			switch {
			case local == "namespace" && !inNamespace:
				inNamespace = true

			case local == "class" && inNamespace && !inClass:
				inClass = true
				info := &ClassInfo{}
				info.CType = attrVal(t.Attr, "http://www.gtk.org/introspection/c/1.0", "type")
				info.Parent = attrVal(t.Attr, "", "parent")
				name := attrVal(t.Attr, "", "name")
				currentClass = info
				reg.Classes[name] = info

			case local == "interface" && inNamespace && !inInterface:
				inInterface = true
				info := &InterfaceInfo{}
				name := attrVal(t.Attr, "", "name")
				currentInterface = info
				reg.Interfaces[name] = info

			case (local == "enumeration" || local == "bitfield") && inNamespace && !inEnum:
				inEnum = true
				currentEnum = &EnumInfo{
					CType:   attrVal(t.Attr, "http://www.gtk.org/introspection/c/1.0", "type"),
					Members: map[string]string{},
				}
				reg.Enums[attrVal(t.Attr, "", "name")] = currentEnum

			case local == "member" && inEnum:
				currentEnum.Members[attrVal(t.Attr, "", "name")] =
					attrVal(t.Attr, "http://www.gtk.org/introspection/c/1.0", "identifier")

			case (local == "method" || local == "function") && (inClass || inInterface) && !inCtor && !inMethod:
				owner := any(currentClass)
				if inInterface {
					owner = currentInterface
				}
				m := methods[owner]
				if m == nil {
					m = map[string]*methodInfo{}
					methods[owner] = m
				}
				inMethod = true
				name := attrVal(t.Attr, "", "name")
				currentMethod = &methodInfo{
					ident:    attrVal(t.Attr, "http://www.gtk.org/introspection/c/1.0", "identifier"),
					instance: local == "method",
					// What a program cannot call as declared: one the bindings
					// are told to skip, one a newer API replaces, and one that
					// reports through a GError.
					skip: attrVal(t.Attr, "", "introspectable") == "0" ||
						attrVal(t.Attr, "", "deprecated") == "1" ||
						attrVal(t.Attr, "", "throws") == "1",
				}
				if _, seen := m[name]; !seen {
					m[name] = currentMethod
				}

			case local == "parameters" && inMethod:
				inMethodParams = true

			case local == "return-value" && inMethod:
				inMethodReturn = true

			case local == "type" && inMethodReturn:
				currentMethod.returns = attrVal(t.Attr, "", "name")
				inMethodReturn = false

			// The instance is a separate element (<instance-parameter>), so
			// every <parameter> here is a value the caller has to supply.
			// Only the first one's type is recorded; the count is what says
			// whether a one-value cgo call site can reach this method at all.
			case local == "parameter" && inMethodParams:
				currentMethod.valParams++
				inMethodParam = currentMethod.valParams == 1

			case local == "type" && inMethodParam:
				currentMethod.valCType = attrVal(t.Attr, "http://www.gtk.org/introspection/c/1.0", "type")
				inMethodParam = false

			case local == "implements" && inClass:
				name := attrVal(t.Attr, "", "name")
				if name != "" {
					currentClass.Implements = append(currentClass.Implements, name)
				}

			case local == "constructor" && inClass && !inCtor:
				inCtor = true
				ident := attrVal(t.Attr, "http://www.gtk.org/introspection/c/1.0", "identifier")
				// Open a fresh ctor slot at the end of Constructors;
				// param StartElements below append into the last entry.
				currentClass.Constructors = append(currentClass.Constructors, ConstructorInfo{Name: ident})
				if currentClass.Constructor.Name == "" {
					// First constructor — record on the legacy field too.
					currentClass.Constructor.Name = ident
				}
				acceptCtor = true

			case local == "parameters" && inCtor:
				inCtorParams = true

			case local == "parameter" && inCtorParams && acceptCtor:
				inParam = true
				paramName = attrVal(t.Attr, "", "name")
				paramTypeName = ""
				paramCType = ""

			case local == "type" && inParam && paramTypeName == "":
				paramTypeName = attrVal(t.Attr, "", "name")
				paramCType = attrVal(t.Attr, "http://www.gtk.org/introspection/c/1.0", "type")

			case local == "array" && inProp:
				inPropArray = true

			case local == "type" && inProp && !inPropArray:
				propTypeName = attrVal(t.Attr, "", "name")

			case local == "property" && (inClass || inInterface) && !inProp:
				inProp = true
				propName = attrVal(t.Attr, "", "name")
				propWritable = attrVal(t.Attr, "", "writable")
				propConstructOnly = attrVal(t.Attr, "", "construct-only")
				propSetter = attrVal(t.Attr, "", "setter")
				propTypeName = ""

			case local == "signal" && (inClass || inInterface) && currentSignal == nil:
				currentSignal = &Signal{Name: attrVal(t.Attr, "", "name")}

			case local == "return-value" && currentSignal != nil:
				inSignalReturn = true

			case local == "type" && inSignalReturn:
				currentSignal.ReturnType = attrVal(t.Attr, "", "name")
				inSignalReturn = false

			// A signal's <parameters> carries no <instance-parameter>: the
			// instance is implicit. So every <parameter> is an argument the
			// callback signature has to carry beyond it.
			case local == "parameters" && currentSignal != nil:
				inSignalParams = true

			case local == "parameter" && inSignalParams:
				currentSignal.Params++
			}

		case xml.EndElement:
			local := t.Name.Local
			switch {
			case local == "namespace":
				inNamespace = false

			case (local == "enumeration" || local == "bitfield") && inEnum:
				inEnum = false
				currentEnum = nil

			case local == "class" && inClass:
				inClass = false
				currentClass = nil

			case local == "interface" && inInterface:
				inInterface = false
				currentInterface = nil

			case local == "return-value" && inMethodReturn:
				inMethodReturn = false

			case (local == "method" || local == "function") && inMethod:
				inMethod = false
				inMethodReturn = false
				inMethodParams = false
				inMethodParam = false
				currentMethod = nil

			case local == "parameters" && inMethodParams:
				inMethodParams = false

			case local == "constructor" && inCtor:
				inCtor = false
				inCtorParams = false
				acceptCtor = false

			case local == "parameters" && inCtorParams:
				inCtorParams = false

			case local == "return-value" && inSignalReturn:
				inSignalReturn = false

			case local == "parameters" && inSignalParams:
				inSignalParams = false

			case local == "signal" && currentSignal != nil:
				switch {
				case inClass && currentClass != nil:
					currentClass.Signals = append(currentClass.Signals, *currentSignal)
				case inInterface && currentInterface != nil:
					currentInterface.Signals = append(currentInterface.Signals, *currentSignal)
				}
				currentSignal = nil
				inSignalParams = false
				inSignalReturn = false

			case local == "parameter" && inParam:
				inParam = false
				cp := ConstructorParam{
					Name:    paramName,
					GIRType: paramTypeName,
					CType:   paramCType,
					IRType:  girTypeToIR(paramTypeName),
				}
				// Append to the in-progress (last) Constructors entry.
				if n := len(currentClass.Constructors); n > 0 {
					currentClass.Constructors[n-1].Params = append(currentClass.Constructors[n-1].Params, cp)
				}
				// Mirror onto the legacy first-ctor slot only while the
				// first constructor is still being parsed.
				if len(currentClass.Constructors) == 1 {
					currentClass.Constructor.Params = append(currentClass.Constructor.Params, cp)
				}

			case local == "property" && inProp:
				inProp = false
				inPropArray = false
				if propWritable == "1" {
					p := Prop{
						Name:          propName,
						Setter:        propSetter,
						ConstructOnly: propConstructOnly == "1",
						GIRType:       propTypeName,
						IRType:        girTypeToIR(propTypeName),
					}
					switch {
					case currentClass != nil:
						currentClass.Props = append(currentClass.Props, p)
					case currentInterface != nil:
						currentInterface.Props = append(currentInterface.Props, p)
					}
				}
			}
		}
	}

	// Resolve every <property setter="…"> reference — a GIR method name —
	// to the C identifier of that method, before the interface merge copies
	// resolved props onto implementing classes. A reference nothing declares
	// leaves the prop with no setter rather than a name that does not exist.
	for _, cls := range reg.Classes {
		resolveSetters(cls.Props, methods[any(cls)])
		cls.Actions = actionsOf(methods[any(cls)])
	}
	for _, iface := range reg.Interfaces {
		resolveSetters(iface.Props, methods[any(iface)])
	}

	// Post-pass: merge interface properties into each implementing class.
	// Skip names that already exist on the class so direct declarations win.
	for _, cls := range reg.Classes {
		for _, ifaceName := range cls.Implements {
			iface, ok := reg.Interfaces[ifaceName]
			if !ok {
				continue
			}
			for _, ip := range iface.Props {
				if hasProp(cls.Props, ip.Name) {
					continue
				}
				tagged := ip
				if tagged.InterfaceName == "" {
					tagged.InterfaceName = ifaceName
				}
				cls.Props = append(cls.Props, tagged)
			}
			for _, is := range iface.Signals {
				if hasSignal(cls.Signals, is.Name) {
					continue
				}
				cls.Signals = append(cls.Signals, is)
			}
		}
	}

	// Post-pass: fold each class's ancestors into it. It runs after the
	// interface merge has finished for every class, because what a class
	// inherits is its parent's *merged* surface: GtkListView gets
	// orientation because GtkListBase implements GtkOrientable, and only a
	// parent whose interfaces are already folded in can pass it down.
	//
	// Precedence, nearest declaration first: a class's own properties, then
	// the interfaces it declares itself, then its parent's merged set (own,
	// its interfaces, its parent's, …). Appending only names not already
	// present is what implements it, since the interface merge left the
	// class's own props at the front.
	inherited := map[string]bool{}
	var inherit func(name string)
	inherit = func(name string) {
		// Marked before recursing, so a parent cycle in malformed GIR stops
		// here rather than recursing forever.
		if inherited[name] {
			return
		}
		inherited[name] = true
		cls := reg.Classes[name]
		if cls == nil {
			return
		}
		parent := reg.Classes[cls.Parent]
		if parent == nil {
			// No parent, or one in a namespace this parser does not read.
			return
		}
		inherit(cls.Parent)
		for _, pp := range parent.Props {
			if hasProp(cls.Props, pp.Name) {
				continue
			}
			tagged := pp
			if tagged.InterfaceName == "" && tagged.OwnerCType == "" {
				// The receiver a call to this setter needs. Without a C type
				// to name there is no cast to emit, so the setter is dropped
				// and the property falls through to the generic GObject path,
				// which needs no receiver type at all.
				if parent.CType == "" {
					tagged.Setter = ""
				}
				tagged.OwnerCType = parent.CType
			}
			cls.Props = append(cls.Props, tagged)
		}
		for _, ps := range parent.Signals {
			if hasSignal(cls.Signals, ps.Name) {
				continue
			}
			cls.Signals = append(cls.Signals, ps)
		}
	}
	for name := range reg.Classes {
		inherit(name)
	}

	// How each class takes a child, from the methods and properties parsed
	// above. After the inherit pass, so a subclass reads its parent's answer:
	// GtkApplicationWindow takes its child through GtkWindow's `child`.
	for owner, cls := range reg.Classes {
		cls.ChildAdd = childAdder(cls, methods[cls])
		_ = owner
	}
	for _, cls := range reg.Classes {
		if cls.ChildAdd.Func != "" {
			continue
		}
		for p := reg.Classes[cls.Parent]; p != nil; p = reg.Classes[p.Parent] {
			if p.ChildAdd.Func != "" {
				cls.ChildAdd = p.ChildAdd
				break
			}
		}
	}

	for _, cls := range reg.Classes {
		if cls.CType != "" {
			reg.ByCType[cls.CType] = cls
		}
	}

	return reg, nil
}

// childAdder picks the call a container's child is added through. An
// accumulating method wins over the replacing property: a class declaring both
// (GtkStack has add_child and no child property; GtkOverlay has both) holds
// many children, and the property would silently drop all but the last.
func childAdder(cls *ClassInfo, m map[string]*methodInfo) ChildAdder {
	for _, name := range []string{"append", "add_child"} {
		if mi := m[name]; mi != nil && mi.ident != "" && mi.valParams == 1 && isWidgetCType(mi.valCType) {
			return ChildAdder{Func: mi.ident}
		}
	}
	for _, p := range cls.Props {
		if p.Name != "child" || p.Setter == "" || p.SetterValParams != 1 {
			continue
		}
		if !isWidgetCType(p.SetterCType) {
			continue
		}
		return ChildAdder{Func: p.Setter, Single: true}
	}
	return ChildAdder{}
}

// isWidgetCType reports whether a C type names a widget pointer. A container's
// way in takes a widget; a method called `append` that takes a string is
// GtkStringList's, and appending a child through it would not compile.
func isWidgetCType(c string) bool {
	return c == "GtkWidget*" || c == "GtkWidget"
}

// hasProp reports whether props already carries a property of this GIR name.
func hasProp(props []Prop, name string) bool {
	for _, p := range props {
		if p.Name == name {
			return true
		}
	}
	return false
}

// hasSignal reports whether signals already carries one of this GIR name.
func hasSignal(signals []Signal, name string) bool {
	for _, s := range signals {
		if s.Name == name {
			return true
		}
	}
	return false
}

// resolveSetters rewrites each prop's Setter from the GIR method name the
// property referenced to that method's C identifier.
func resolveSetters(props []Prop, methods map[string]*methodInfo) {
	for i := range props {
		if props[i].Setter == "" {
			continue
		}
		m := methods[props[i].Setter]
		if m == nil {
			props[i].Setter = ""
			continue
		}
		props[i].SetterValParams = m.valParams
		// One value is all a cgo call site passes. Keep the name only when
		// that is all the setter wants; the rest fall through to the generic
		// GObject path, which reaches them by property name and needs no
		// signature.
		if m.valParams != 1 {
			props[i].Setter = ""
			continue
		}
		props[i].Setter = m.ident
		props[i].SetterCType = m.valCType
	}
}

// methodInfo is one GIR <method>: the C function it names, how many values it
// takes beyond the instance, and the C type of the first of them — for a
// setter, the value.
type methodInfo struct {
	ident    string
	instance bool   // a <method>, called on an instance, rather than a <function>
	returns  string // the GIR name of the return type, "none" for void
	skip     bool
	valCType string
	// valParams counts the <parameter> elements after the instance — the
	// values a caller supplies.
	valParams int
}

// actionsOf is the methods a program calls for their effect alone: on an
// instance, taking nothing else and returning nothing.
func actionsOf(methods map[string]*methodInfo) []Action {
	var out []Action
	for name, m := range methods {
		if m.instance && !m.skip && m.valParams == 0 && m.returns == "none" && m.ident != "" {
			out = append(out, Action{Name: name, CIdent: m.ident})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// attrVal finds an attribute value by namespace URI and local name.
// Pass ns="" to match attributes in any namespace (including none).
func attrVal(attrs []xml.Attr, ns, local string) string {
	for _, a := range attrs {
		if a.Name.Local == local && (ns == "" || a.Name.Space == ns) {
			return a.Value
		}
	}
	return ""
}

// girTypeToIR maps a GIR type name to the closest ir.Type.
func girTypeToIR(name string) *ir.Type {
	switch name {
	case "utf8", "gchararray", "filename":
		return &ir.Type{Kind: ir.TypeString}
	case "gboolean":
		return &ir.Type{Kind: ir.TypeBool}
	case "gint", "gint32", "gint64", "guint", "guint32", "guint64", "gsize":
		return &ir.Type{Kind: ir.TypeInt}
	case "gdouble", "gfloat":
		return &ir.Type{Kind: ir.TypeFloat}
	case "none":
		return nil
	default:
		return &ir.Type{Kind: ir.TypeDyn}
	}
}
