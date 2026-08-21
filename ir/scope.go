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
	Name    string
	Pkg     *Package
	Resolve func(identifier string) Symbol // optional fallback for unknown members
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

// RedeclaredError reports that a name was already bound in the same scope.
// Prev is the binding that was already there.
type RedeclaredError struct {
	Name string
	Prev Symbol
}

func (e *RedeclaredError) Error() string { return "redeclared: " + e.Name }

// Declare binds a symbol in this scope, refusing to overwrite an existing
// binding of the same name. Callers that mean to rebind must say so with
// Replace: a silent overwrite makes which declaration a name refers to depend
// on the order the checker happened to visit them in.
func (s *Scope) Declare(sym Symbol) error {
	name := sym.SymName()
	if prev, ok := s.Symbols[name]; ok {
		return &RedeclaredError{Name: name, Prev: prev}
	}
	s.Symbols[name] = sym
	return nil
}

// Replace binds a symbol, overwriting any existing binding of the same name.
// For the places where rebinding is the intent — shadowing a dot-imported
// name, splicing a platform override over the stdlib declaration it extends.
func (s *Scope) Replace(sym Symbol) {
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
	// Scalar universe names come from the shared built-in registry (builtins.go)
	// so this table can't drift from the type resolver / conversion switches.
	for _, b := range BuiltinScalars() {
		if b.Universe {
			s.Symbols[b.Name] = &TypeSym{Name: b.Name, Type: b.Type}
		}
	}
	// The bare generic constructors are predeclared as dyn-parameterized
	// defaults. "color", "date", "time", and "datetime" are intentionally
	// absent — they are provided exclusively as stdlib StructDefs
	// (lib/types.sngl). The stdlib scope sits between this base scope and user
	// code, so resolution finds the StructDef. Pre-stdlib lookups for these
	// names will fail, which is the correct behaviour.
	for _, entry := range []struct {
		name string
		typ  *Type
	}{
		{"list", ListOf(TypDyn)},
		{"option", OptionOf(TypDyn)},
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
