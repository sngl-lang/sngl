package checker

import "maps"

// Scope tracks variable declarations with parent chain for nested contexts.
type Scope struct {
	parent    *Scope
	vars      map[string]Type
	typeHints map[string]string // full type hint strings (e.g. "list:docs.Component")
}

// NewScope creates a new scope with an optional parent.
func NewScope(parent *Scope) *Scope {
	return &Scope{parent: parent, vars: make(map[string]Type), typeHints: make(map[string]string)}
}

// Declare adds a variable to this scope.
func (s *Scope) Declare(name string, t Type) {
	s.vars[name] = t
}

// DeclareHint adds a variable with its full type hint string.
func (s *Scope) DeclareHint(name string, t Type, hint string) {
	s.vars[name] = t
	if hint != "" {
		s.typeHints[name] = hint
	}
}

// LookupHint returns the type hint string for a variable, walking the parent chain.
func (s *Scope) LookupHint(name string) (string, bool) {
	if h, ok := s.typeHints[name]; ok {
		return h, true
	}
	if s.parent != nil {
		return s.parent.LookupHint(name)
	}
	return "", false
}

// Lookup returns the type of a variable, walking the parent chain.
func (s *Scope) Lookup(name string) (Type, bool) {
	if t, ok := s.vars[name]; ok {
		return t, true
	}
	if s.parent != nil {
		return s.parent.Lookup(name)
	}
	return Dyn, false
}

// All returns all variables in the chain (child overrides parent).
func (s *Scope) All() map[string]Type {
	all := make(map[string]Type)
	s.collect(all)
	return all
}

func (s *Scope) collect(all map[string]Type) {
	if s.parent != nil {
		s.parent.collect(all)
	}
	maps.Copy(all, s.vars)
}
