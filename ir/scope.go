package ir

import (
	"errors"
	"slices"
)

// Symbol is a named entity in the program. Implemented by all IR
// declaration types (Func, Var, Component, StructDef, EnumDef, UnitDef,
// Import, Param) and small helper types (LoopVar, Namespace).
type Symbol interface {
	SymName() string
	SymType() *Type
}

// TypeParamSym binds a type parameter's name in the scope its declaration
// opens. Nothing else declares one, so a repeat is the ordinary redeclaration.
type TypeParamSym struct{ Name string }

func (t *TypeParamSym) SymName() string { return t.Name }
func (t *TypeParamSym) SymType() *Type  { return &Type{Kind: TypeTypeParam, ParamName: t.Name} }

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
	// IsPackageRoot marks the root scope of a package, so a qualified lookup
	// can tell a package boundary from the ambient scope every package's root
	// eventually chains into. See SymbolTable.LookupMember.
	IsPackageRoot bool
	// Wildcards are the symbols in this scope that answer to names nobody
	// declared. A declared name always wins, and a wildcard is consulted only
	// when there is no name to find — and only by a caller resolving a
	// position a wildcard stands in (WildcardMatches), never by Lookup.
	Wildcards []Symbol
}

// WildcardSymbol is a symbol that also answers to every name its pattern
// matches. The pattern is RE2, matched against a whole name.
type WildcardSymbol interface {
	Symbol
	WildcardPattern() string
}

// NewScope creates a child scope.
func NewScope(parent *Scope) *Scope {
	return &Scope{Parent: parent, Symbols: make(map[string]Symbol)}
}

// noteWildcard records sym among the scope's wildcards when it is one. Binding
// a wildcard symbol binds its own name too: `element` is a name like any
// other, and is what a declaration of it in an inner scope shadows.
func (s *Scope) noteWildcard(sym Symbol) {
	w, ok := sym.(WildcardSymbol)
	if !ok || w.WildcardPattern() == "" {
		return
	}
	if slices.Contains(s.Wildcards, sym) {
		return
	}
	s.Wildcards = append(s.Wildcards, sym)
}

// WildcardMatches is every wildcard covering name in the innermost scope of
// the chain that has one. A wildcard shadows an outer one exactly as a
// declaration shadows an outer declaration, so the search stops at the first
// scope that matches at all. More than one match in that scope is an ambiguity
// the caller reports, which is why they are all returned rather than the first.
func (s *Scope) WildcardMatches(name string) []Symbol {
	for sc := s; sc != nil; sc = sc.Parent {
		if m := sc.localWildcardMatches(name); len(m) > 0 {
			return m
		}
	}
	return nil
}

func (s *Scope) localWildcardMatches(name string) []Symbol {
	var out []Symbol
	for _, sym := range s.Wildcards {
		w, ok := sym.(WildcardSymbol)
		if !ok || !MatchesWildcard(w.WildcardPattern(), name) {
			continue
		}
		out = append(out, sym)
	}
	return out
}

// lookupWildcard is LookupLocal's fallback. Lookup has none: see there.
func (s *Scope) lookupWildcard(name string) (Symbol, bool) {
	if m := s.localWildcardMatches(name); len(m) > 0 {
		return m[0], true
	}
	return nil, false
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
	// Re-registering the same declaration is not a redeclaration. A checker
	// pass may reach one declaration by more than one route.
	if prev, ok := s.Symbols[name]; ok && prev != sym {
		return &RedeclaredError{Name: name, Prev: prev}
	}
	s.Symbols[name] = sym
	s.noteWildcard(sym)
	return nil
}

// DeclareName binds a symbol under its own name only, without lifting a
// wildcard symbol's pattern into this scope. For a dot import: it lifts the
// declarations of a package, and a wildcard component's declaration is the
// name `element` — the open set of names it also answers to is reached by
// naming the package (`html.div`) or by being inside its platform's body, both
// of which go to the package's own scope. Lifting the set instead would mean
// the importing file has no undeclared node name left to misspell.
func (s *Scope) DeclareName(sym Symbol) error {
	name := sym.SymName()
	if prev, ok := s.Symbols[name]; ok && prev != sym {
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
	s.noteWildcard(sym)
}

// Lookup walks the parent chain for a name. Declared names only: it resolves
// every identifier in the language — a type, a variable, a func, a method
// receiver — and a wildcard stands for names in one specific position, a
// visual node's, a prop's, an event's. Answering all of them with one would
// make every misspelling a reference to it. The sites that resolve such a
// position consult WildcardMatches themselves, having first missed here, which
// is already how the qualified form works (`html.div`, via nsMember).
func (s *Scope) Lookup(name string) (Symbol, bool) {
	for sc := s; sc != nil; sc = sc.Parent {
		if sym, ok := sc.Symbols[name]; ok {
			return sym, true
		}
	}
	return nil, false
}

// LookupDeclaredLocal checks this scope's declared names only, skipping its
// wildcards. For asking whether a particular name was declared here — which a
// wildcard, standing for every name it matches, would always answer yes to.
func (s *Scope) LookupDeclaredLocal(name string) (Symbol, bool) {
	sym, ok := s.Symbols[name]
	return sym, ok
}

// LookupLocal checks only this scope, not parents.
func (s *Scope) LookupLocal(name string) (Symbol, bool) {
	if sym, ok := s.Symbols[name]; ok {
		return sym, true
	}
	return s.lookupWildcard(name)
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
		{"map", MapOf(TypDyn, TypDyn)},
	} {
		s.Symbols[entry.name] = &TypeSym{Name: entry.name, Type: entry.typ}
	}
	return s
}

// SymbolTable is the package-level symbol registry.
type SymbolTable struct {
	Root *Scope
}

// LookupMember resolves a name qualified by a package -- `html.div`, and the
// `html.platform` a target's identity const answers to. It walks the root's
// parent chain only while each scope is another package's root, which is how
// declareNS chains a target's package behind a stdlib namespace of the same
// name.
//
// An unbounded Lookup would keep going past the last package into the lib
// import scope, where every ambient declaration lives: `html.color` then
// resolved to the built-in color type, and `html.platform` to the built-in
// platform type rather than to html's own identity const.
func (t *SymbolTable) LookupMember(name string) (Symbol, bool) {
	if t == nil {
		return nil, false
	}
	for sc := t.Root; sc != nil; sc = sc.Parent {
		if sym, ok := sc.Symbols[name]; ok {
			return sym, true
		}
		if sc.Parent == nil || !sc.Parent.IsPackageRoot {
			return nil, false
		}
	}
	return nil, false
}

func (SymbolTable) String() string { return "omitted" }

// NewSymbolTable creates an empty symbol table.
func NewSymbolTable() *SymbolTable {
	root := NewScope(NewBaseScope())
	root.IsPackageRoot = true
	return &SymbolTable{Root: root}
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

// LookupMemberType is LookupType for a name qualified by a package -- the type
// half of LookupMember, and bounded the same way. Unqualified resolution wants
// the ambient scope at the end of the chain; `pkg.Type` must not, or every
// package answers to every built-in type name.
func (st *SymbolTable) LookupMemberType(name string) (Symbol, bool) {
	if sym, ok := st.LookupMember(name); ok && IsTypeDecl(sym) {
		return sym, true
	}
	return nil, false
}

// LookupDeclaredComponent finds a component bound to exactly this name,
// skipping wildcards. For callers that mean "is there a declaration called
// this", which a wildcard would always answer yes to.
func (st *SymbolTable) LookupDeclaredComponent(name string) (Symbol, bool) {
	for s := st.Root; s != nil; s = s.Parent {
		sym, ok := s.LookupDeclaredLocal(name)
		if !ok {
			continue
		}
		if c, isComp := sym.(*Component); isComp {
			return c, true
		}
		return nil, false
	}
	return nil, false
}

// LookupRootComponent finds a component for this name from a package's root
// scope outward, falling back to a wildcard component covering it. A component
// name is a node position, which is one of the positions a wildcard stands in,
// so this is one of the sites Scope.Lookup leaves the wildcard consult to.
// Callers asking whether a name was *declared* want LookupDeclaredComponent.
//
// The root is where a name qualified by a package resolves, and where an
// override's target resolves. A *bare* reference in a body is a different
// question and wants the lexical chain every other identifier uses --
// checker.lookupComponentInScope -- or a component declared inside a body is
// invisible to it.
//
// A name covered by two wildcards in one scope is an ambiguity, reported by
// the checker paths that hold a position to report it at (scopeWildcard); here
// the first still answers, as it did when Lookup itself fell back.
func (st *SymbolTable) LookupRootComponent(name string) (Symbol, bool) {
	sym, ok := st.Root.Lookup(name)
	if !ok {
		if m := st.Root.WildcardMatches(name); len(m) > 0 {
			sym, ok = m[0], true
		}
	}
	if ok {
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

var (
	// ErrUnknownReceiver reports that a receiver name resolves to no
	// declaration at all, so there is nothing for the method to be a member of.
	ErrUnknownReceiver = errors.New("unknown receiver")
	// ErrNoMethodHost reports that a receiver resolves to a declaration that
	// carries no member list — a namespace, whose members are the declarations
	// of the package it names.
	ErrNoMethodHost = errors.New("receiver has no member list")
)

// AttachMethod makes f a member of the declaration recv names in scope,
// refusing to overwrite a member already there. Attaching is a declaration
// like any other: a silent overwrite would make which of two declarations a
// member name refers to depend on the order the checker visited them in.
// ReplaceMethod is the explicit rebind.
//
// It takes the scope rather than a SymbolTable because a declaration is
// registered before it is reachable from the package root — the stdlib loads
// into a scope that only later becomes the root's parent.
func AttachMethod(scope *Scope, recv string, f *Func) error {
	tbl, err := memberList(scope, recv)
	if err != nil {
		return err
	}
	// Re-registering the same declaration is not a redeclaration. The checker
	// reaches a component's methods from more than one pass.
	if prev, exists := (*tbl)[f.Name]; exists && prev != f {
		return &RedeclaredError{Name: f.Name, Prev: prev}
	}
	(*tbl)[f.Name] = f
	return nil
}

// ReplaceMethod makes f a member of the declaration recv names, overwriting
// any member of the same name. For the one case where rebinding is the intent:
// a declaration that shadows one the standard library made.
func ReplaceMethod(scope *Scope, recv string, f *Func) error {
	tbl, err := memberList(scope, recv)
	if err != nil {
		return err
	}
	(*tbl)[f.Name] = f
	return nil
}

// memberList returns the member table of the declaration recv names, creating
// it on first use.
func memberList(scope *Scope, recv string) (*map[string]*Func, error) {
	sym, ok := scope.Lookup(recv)
	if !ok {
		return nil, ErrUnknownReceiver
	}
	tbl := methodTable(sym)
	if tbl == nil {
		return nil, ErrNoMethodHost
	}
	if *tbl == nil {
		*tbl = make(map[string]*Func)
	}
	return tbl, nil
}

// MemberOf returns the method named name declared on decl — the Decl carried
// by a resolved *Type, or any declaration value. Going to the declaration
// directly is what lets a member be found on a type reached through an import
// alias, whose name at the use site is not the name it was declared under.
func MemberOf(decl any, name string) (*Func, bool) {
	sym, ok := decl.(Symbol)
	if !ok {
		return nil, false
	}
	tbl := methodTable(sym)
	if tbl == nil {
		return nil, false
	}
	f, found := (*tbl)[name]
	return f, found
}
