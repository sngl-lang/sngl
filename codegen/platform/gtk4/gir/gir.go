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
	IRType  *ir.Type
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
	// InterfaceName, when non-empty, identifies the interface this prop was
	// inherited from (e.g. "Orientable"). Its setter takes the interface as
	// its receiver, so the codegen casts the widget to that type rather than
	// to its own class. Empty for direct class properties.
	InterfaceName string
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

		currentClass     *ClassInfo
		currentInterface *InterfaceInfo
		currentEnum      *EnumInfo
		// methods maps a class or interface to its methods by GIR name, so a
		// <property setter="set_focus"> reference can be resolved to the C
		// function it names and the type that function takes.
		methods       = map[any]map[string]*methodInfo{}
		currentMethod *methodInfo

		paramName     string
		paramTypeName string
		propName      string
		propWritable  string
		propSetter    string
		propTypeName  string
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

			// A setter takes one value beyond its instance parameter, which
			// is a separate element — so the first <parameter> is it.
			case local == "parameter" && inMethodParams && currentMethod.valCType == "":
				inMethodParam = true

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

			case local == "type" && inParam:
				paramTypeName = attrVal(t.Attr, "", "name")

			case local == "array" && inProp:
				inPropArray = true

			case local == "type" && inProp && !inPropArray:
				propTypeName = attrVal(t.Attr, "", "name")

			case local == "property" && (inClass || inInterface) && !inProp:
				inProp = true
				propName = attrVal(t.Attr, "", "name")
				propWritable = attrVal(t.Attr, "", "writable")
				propSetter = attrVal(t.Attr, "", "setter")
				propTypeName = ""

			case local == "signal" && inClass:
				sigName := attrVal(t.Attr, "", "name")
				currentClass.Signals = append(currentClass.Signals, Signal{Name: sigName})

			case local == "signal" && inInterface && currentInterface != nil:
				sigName := attrVal(t.Attr, "", "name")
				currentInterface.Signals = append(currentInterface.Signals, Signal{Name: sigName})
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

			case local == "parameter" && inParam:
				inParam = false
				cp := ConstructorParam{
					Name:    paramName,
					GIRType: paramTypeName,
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
						Name:    propName,
						Setter:  propSetter,
						GIRType: propTypeName,
						IRType:  girTypeToIR(propTypeName),
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
		props[i].Setter = m.ident
		props[i].SetterCType = m.valCType
	}
}

// methodInfo is one GIR <method>: the C function it names, and the C type of
// the first parameter after the instance — for a setter, the value.
type methodInfo struct {
	ident    string
	valCType string
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
