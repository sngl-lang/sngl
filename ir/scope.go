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
	Root *Scope
}

func (SymbolTable) String() string { return "omitted" }

// NewSymbolTable creates an empty symbol table.
func NewSymbolTable() *SymbolTable {
	return &SymbolTable{Root: NewScope(NewBaseScope())}
}

// EachSymbol ranges over every symbol reachable from the root scope, innermost
// binding first, stopping when f returns false. A name bound in more than one
// scope is yielded once, by its innermost binding — the one a lookup answers
// with. Used by the passes that need every declaration in the build, including
// the stdlib's, which lives in a scope outside the package's own root.
func (st *SymbolTable) EachSymbol(f func(Symbol) bool) {
	seen := make(map[string]bool)
	for sc := st.Root; sc != nil; sc = sc.Parent {
		for name, sym := range sc.Symbols {
			if seen[name] {
				continue
			}
			seen[name] = true
			if !f(sym) {
				return
			}
		}
	}
}

// IsTypeDecl reports whether sym declares a named type — a struct, enum or
// unit. The three are interchangeable wherever a type name is expected.
func IsTypeDecl(sym Symbol) bool {
	switch sym.(type) {
	case *StructDef, *EnumDef, *UnitDef:
		return true
	}
	return false
}

// LookupType finds a named type declaration — struct, enum or unit — from the
// root scope outward. A predeclared universe name (TypeSym) is not a
// declaration and does not answer here.
func (st *SymbolTable) LookupType(name string) (Symbol, bool) {
	if sym, ok := st.Root.Lookup(name); ok && IsTypeDecl(sym) {
		return sym, true
	}
	return nil, false
}

// LookupComponent finds a component declaration from the root scope outward.
func (st *SymbolTable) LookupComponent(name string) (Symbol, bool) {
	if sym, ok := st.Root.Lookup(name); ok {
		if c, isComp := sym.(*Component); isComp {
			return c, true
		}
	}
	return nil, false
}

// methodTable returns the member table of the declaration sym, or nil when sym
// is not a declaration methods can attach to. The pointer lets a caller create
// the map on first write.
func methodTable(sym Symbol) *map[string]*Func {
	switch d := sym.(type) {
	case *StructDef:
		return &d.Methods
	case *EnumDef:
		return &d.Methods
	case *UnitDef:
		return &d.Methods
	case *Component:
		return &d.Methods
	}
	return nil
}

// LookupMethod finds a method on the declaration named recv, resolving recv
// from the package root. A caller holding a narrower scope should use
// LookupMethodIn with it, so a lookup answers with the same declaration
// AttachMethod wrote to.
func (st *SymbolTable) LookupMethod(recv, method string) (*Func, bool) {
	return LookupMethodIn(st.Root, recv, method)
}

// LookupMethodIn finds the method named method on the declaration that recv
// resolves to in scope. A namespace's members are the declarations of the
// package it names, so a namespace receiver resolves through that package
// rather than a table here.
func LookupMethodIn(scope *Scope, recv, method string) (*Func, bool) {
	sym, ok := scope.Lookup(recv)
	if !ok {
		return nil, false
	}
	if ns, isNS := sym.(*Namespace); isNS {
		if ns.Pkg == nil || ns.Pkg.Symbols == nil {
			return nil, false
		}
		member, found := ns.Pkg.Symbols.Root.LookupLocal(method)
		if !found {
			return nil, false
		}
		fn, isFunc := member.(*Func)
		return fn, isFunc
	}
	if tbl := methodTable(sym); tbl != nil {
		f, found := (*tbl)[method]
		return f, found
	}
	return nil, false
}

// AttachMethod attaches f to the declaration named recv as resolved from
// scope, reporting whether there was one to attach it to. It takes the scope
// rather than a SymbolTable because a declaration is registered before it is
// reachable from the package root — the stdlib loads into a scope that only
// later becomes the root's parent.
func AttachMethod(scope *Scope, recv string, f *Func) bool {
	sym, ok := scope.Lookup(recv)
	if !ok {
		return false
	}
	tbl := methodTable(sym)
	if tbl == nil {
		return false
	}
	if *tbl == nil {
		*tbl = make(map[string]*Func)
	}
	(*tbl)[f.Name] = f
	return true
}

// MethodOn returns the method named method already attached to the declaration
// recv names in scope. Unlike LookupMethodIn it does not follow a namespace to
// its package: it answers "does this declaration already carry this member",
// which is what a duplicate check asks.
func MethodOn(scope *Scope, recv, method string) (*Func, bool) {
	sym, ok := scope.Lookup(recv)
	if !ok {
		return nil, false
	}
	tbl := methodTable(sym)
	if tbl == nil {
		return nil, false
	}
	f, found := (*tbl)[method]
	return f, found
}
