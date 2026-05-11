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
	Name   string
	IRType *ir.Type
}

// ConstructorInfo holds the C identifier and parameters for a widget constructor.
type ConstructorInfo struct {
	Name   string             // e.g. "gtk_button_new_with_label"
	Params []ConstructorParam // positional params
}

// Prop is a writable property on a GIR class.
type Prop struct {
	Name   string
	IRType *ir.Type
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
	Props        []Prop            // writable properties
	Signals      []Signal
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
	Classes map[string]*ClassInfo
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
	reg := &TypeRegistry{Classes: make(map[string]*ClassInfo)}
	dec := xml.NewDecoder(bytes.NewReader(data))

	var (
		inNamespace  bool
		inClass      bool
		inCtor       bool // inside a <constructor> element
		acceptCtor   bool // whether this is the first (accepted) constructor
		inCtorParams bool
		inParam      bool
		inProp       bool

		currentClass *ClassInfo

		paramName     string
		paramTypeName string
		propName      string
		propWritable  string
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

			case local == "type" && inProp:
				propTypeName = attrVal(t.Attr, "", "name")

			case local == "property" && inClass && !inProp:
				inProp = true
				propName = attrVal(t.Attr, "", "name")
				propWritable = attrVal(t.Attr, "", "writable")
				propTypeName = ""

			case local == "signal" && inClass:
				sigName := attrVal(t.Attr, "", "name")
				currentClass.Signals = append(currentClass.Signals, Signal{Name: sigName})
			}

		case xml.EndElement:
			local := t.Name.Local
			switch {
			case local == "namespace":
				inNamespace = false

			case local == "class" && inClass:
				inClass = false
				currentClass = nil

			case local == "constructor" && inCtor:
				inCtor = false
				inCtorParams = false
				acceptCtor = false

			case local == "parameters" && inCtorParams:
				inCtorParams = false

			case local == "parameter" && inParam:
				inParam = false
				cp := ConstructorParam{
					Name:   paramName,
					IRType: girTypeToIR(paramTypeName),
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
				if propWritable == "1" {
					currentClass.Props = append(currentClass.Props, Prop{
						Name:   propName,
						IRType: girTypeToIR(propTypeName),
					})
				}
			}
		}
	}
	return reg, nil
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
