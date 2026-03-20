package checker

import "maps"

// Scope tracks variable declarations with parent chain for nested contexts.
type Scope struct {
	parent *Scope
	vars   map[string]Type
}

// NewScope creates a new scope with an optional parent.
func NewScope(parent *Scope) *Scope {
	return &Scope{parent: parent, vars: make(map[string]Type)}
}

// Declare adds a variable to this scope.
func (s *Scope) Declare(name string, t Type) {
	s.vars[name] = t
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
