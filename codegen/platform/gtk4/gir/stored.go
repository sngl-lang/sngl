package gir

// ParseMinimal parses the bundled introspection data afresh. Unlike Minimal
// the result is the caller's own, so Strip may be called on it.
func ParseMinimal() (*TypeRegistry, error) { return ParseGIRBytes(minimalGIR) }

// Strip clears the fields derived from others -- every IRType, which is a
// function of its GIRType, and ByCType, which indexes Classes -- so that what
// is left is the registry's data alone, fit to store as SNGL. It modifies r in
// place; Restore puts the fields back.
func (r *TypeRegistry) Strip() {
	r.ByCType = nil
	for _, c := range r.Classes {
		stripProps(c.Props)
		stripParams(c.Constructor.Params)
		for i := range c.Constructors {
			stripParams(c.Constructors[i].Params)
		}
	}
	for _, i := range r.Interfaces {
		stripProps(i.Props)
	}
}

// Restore recomputes what Strip cleared, on a registry read back from store.
func (r *TypeRegistry) Restore() {
	r.ByCType = map[string]*ClassInfo{}
	for _, c := range r.Classes {
		restoreProps(c.Props)
		restoreParams(c.Constructor.Params)
		for i := range c.Constructors {
			restoreParams(c.Constructors[i].Params)
		}
		if c.CType != "" {
			r.ByCType[c.CType] = c
		}
	}
	for _, i := range r.Interfaces {
		restoreProps(i.Props)
	}
	if r.Classes == nil {
		r.Classes = map[string]*ClassInfo{}
	}
	if r.Interfaces == nil {
		r.Interfaces = map[string]*InterfaceInfo{}
	}
	if r.Enums == nil {
		r.Enums = map[string]*EnumInfo{}
	}
}

func stripProps(ps []Prop) {
	for i := range ps {
		ps[i].IRType = nil
	}
}

func stripParams(ps []ConstructorParam) {
	for i := range ps {
		ps[i].IRType = nil
	}
}

func restoreProps(ps []Prop) {
	for i := range ps {
		ps[i].IRType = girTypeToIR(ps[i].GIRType)
	}
}

func restoreParams(ps []ConstructorParam) {
	for i := range ps {
		ps[i].IRType = girTypeToIR(ps[i].GIRType)
	}
}
