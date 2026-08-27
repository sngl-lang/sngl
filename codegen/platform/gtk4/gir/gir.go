package gir

import (
	"bytes"
	"encoding/xml"
	"io"
	"os"

	"git.duckfam.us/jonathan/sngl/ir"
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
	Props        []Prop            // writable properties (after interface merge)
	Signals      []Signal
	Implements   []string // names of interfaces declared via <implements>
}

// InterfaceInfo holds resolved metadata for one GIR interface.
type InterfaceInfo struct {
	Props   []Prop // writable properties
	Signals []Signal
}

// ConstructorFor returns the constructor that matches the supplied
// prop set most closely. Preference order:
//
//  1. A constructor whose param names match a supplied prop one-for-one.
//  2. The zero-arg constructor when no props are supplied.
//  3. The first constructor (Constructor) as the last-resort fallback.
//
// supplied is the set of prop names the caller will pass to the
// constructor (e.g. {"label"} for `button(text=...)`).
func (c *ClassInfo) ConstructorFor(supplied map[string]bool) ConstructorInfo {
	if c == nil {
		return ConstructorInfo{}
	}
	ctors := c.Constructors
	if len(ctors) == 0 {
		return c.Constructor
	}
	// Best match: every param maps to a supplied prop.
	for _, ctor := range ctors {
		if len(ctor.Params) == 0 {
			continue
		}
		ok := true
		for _, p := range ctor.Params {
			if !supplied[p.Name] {
				ok = false
				break
			}
		}
		if ok {
			return ctor
		}
	}
	// No-arg form when caller has nothing to bind.
	if len(supplied) == 0 {
		for _, ctor := range ctors {
			if len(ctor.Params) == 0 {
				return ctor
			}
		}
	}
	return ctors[0]
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
				currentMethod = &methodInfo{ident: attrVal(t.Attr, "http://www.gtk.org/introspection/c/1.0", "identifier")}
				if _, seen := m[name]; !seen {
					m[name] = currentMethod
				}

			case local == "parameters" && inMethod:
				inMethodParams = true

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

			case (local == "method" || local == "function") && inMethod:
				inMethod = false
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
				dup := false
				for _, existing := range cls.Props {
					if existing.Name == ip.Name {
						dup = true
						break
					}
				}
				if !dup {
					tagged := ip
					if tagged.InterfaceName == "" {
						tagged.InterfaceName = ifaceName
					}
					cls.Props = append(cls.Props, tagged)
				}
			}
			for _, is := range iface.Signals {
				dup := false
				for _, existing := range cls.Signals {
					if existing.Name == is.Name {
						dup = true
						break
					}
				}
				if !dup {
					cls.Signals = append(cls.Signals, is)
				}
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
	valCType string
	// valParams counts the <parameter> elements after the instance — the
	// values a caller supplies.
	valParams int
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
