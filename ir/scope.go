package ir

// Symbol is a named entity in the program. Implemented by all IR
// declaration types (Func, Var, Component, StructDef, EnumDef, UnitDef,
// Import, Param) and small helper types (LoopVar, Namespace).
type Symbol interface {
	SymName() string
	SymType() *Type
}

// LoopVar is a for-loop iteration variable.
type LoopVar struct {
	Name string
	Type *Type
}

func (v *LoopVar) SymName() string { return v.Name }
func (v *LoopVar) SymType() *Type  { return v.Type }

// Namespace is an import namespace alias pointing to a resolved package.
type Namespace struct {
	Name string
	Pkg  *Package
}

func (n *Namespace) SymName() string { return n.Name }
func (n *Namespace) SymType() *Type  { return nil }

// Scope is a lexical scope with parent chain.
type Scope struct {
	Parent  *Scope
	Symbols map[string]Symbol
}

// NewScope creates a child scope.
func NewScope(parent *Scope) *Scope {
	return &Scope{Parent: parent, Symbols: make(map[string]Symbol)}
}

// Declare adds a symbol to this scope.
func (s *Scope) Declare(sym Symbol) {
	s.Symbols[sym.SymName()] = sym
}

// Lookup walks the parent chain for a name.
func (s *Scope) Lookup(name string) (Symbol, bool) {
	if sym, ok := s.Symbols[name]; ok {
		return sym, true
	}
	if s.Parent != nil {
		return s.Parent.Lookup(name)
	}
	return nil, false
}

// LookupLocal checks only this scope, not parents.
func (s *Scope) LookupLocal(name string) (Symbol, bool) {
	sym, ok := s.Symbols[name]
	return sym, ok
}

// TypeSym represents a builtin type name in the base scope (int, string, etc.).
type TypeSym struct {
	Name string
	Type *Type
}

func (t *TypeSym) SymName() string { return t.Name }
func (t *TypeSym) SymType() *Type  { return t.Type }

// NewBaseScope creates a scope pre-populated with builtin type names.
// Fresh instances are created each call to avoid mutating shared state.
func NewBaseScope() *Scope {
	s := &Scope{Symbols: make(map[string]Symbol)}
	for _, entry := range []struct {
		name string
		typ  *Type
	}{
		{"bool", TypBool},
		{"int", TypInt},
		{"float", TypFloat},
		{"string", TypString},
		{"color", TypColor},
		{"date", TypDate},
		{"time", TypTime},
		{"dateTime", TypDateTime},
		{"duration", TypDuration},
		{"url", TypURL},
		{"email", TypEmail},
		{"uuid", TypUUID},
		{"regex", TypRegex},
		{"base64", TypBase64},
		{"ipv4", TypIPV4},
		{"ipv6", TypIPV6},
		{"hostname", TypHostname},
		{"decimal", TypDecimal},
	} {
		s.Symbols[entry.name] = &TypeSym{Name: entry.name, Type: entry.typ}
	}
	return s
}

// SymbolTable is the package-level symbol registry.
type SymbolTable struct {
	Root    *Scope
	Types   map[string]Symbol           // struct, enum, unit names
	Comps   map[string]Symbol           // component names
	Methods map[string]map[string]*Func // typeName → methodName → Func
}

func (SymbolTable) String() string { return "omitted" }

// NewSymbolTable creates an empty symbol table.
func NewSymbolTable() *SymbolTable {
	return &SymbolTable{
		Root:    NewScope(NewBaseScope()),
		Types:   make(map[string]Symbol),
		Comps:   make(map[string]Symbol),
		Methods: make(map[string]map[string]*Func),
	}
}

// LookupType finds a type declaration by name.
func (st *SymbolTable) LookupType(name string) (Symbol, bool) {
	sym, ok := st.Types[name]
	return sym, ok
}

// LookupComponent finds a component by name.
func (st *SymbolTable) LookupComponent(name string) (Symbol, bool) {
	sym, ok := st.Comps[name]
	return sym, ok
}

// LookupMethod finds a type-attached method.
func (st *SymbolTable) LookupMethod(typeName, method string) (*Func, bool) {
	if methods, ok := st.Methods[typeName]; ok {
		if f, ok := methods[method]; ok {
			return f, true
		}
	}
	return nil, false
}

// RegisterMethod registers a type-attached method.
func (st *SymbolTable) RegisterMethod(typeName string, f *Func) {
	if st.Methods[typeName] == nil {
		st.Methods[typeName] = make(map[string]*Func)
	}
	st.Methods[typeName][f.Name] = f
}
