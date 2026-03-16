package checker

import "github.com/google/cel-go/cel"

// Scope tracks variable declarations with parent chain for nested contexts.
type Scope struct {
	parent *Scope
	vars   map[string]*cel.Type
}

// NewScope creates a new scope with an optional parent.
func NewScope(parent *Scope) *Scope {
	return &Scope{parent: parent, vars: make(map[string]*cel.Type)}
}

// Declare adds a variable to this scope.
func (s *Scope) Declare(name string, t *cel.Type) {
	s.vars[name] = t
}

// Lookup walks the parent chain to find a variable's type.
func (s *Scope) Lookup(name string) (*cel.Type, bool) {
	for cur := s; cur != nil; cur = cur.parent {
		if t, ok := cur.vars[name]; ok {
			return t, true
		}
	}
	return nil, false
}

// EnvOpts returns cel.Variable options for all variables in the chain.
func (s *Scope) EnvOpts() []cel.EnvOption {
	// Collect all vars, child overrides parent.
	all := make(map[string]*cel.Type)
	s.collect(all)
	opts := make([]cel.EnvOption, 0, len(all))
	for name, t := range all {
		opts = append(opts, cel.Variable(name, t))
	}
	return opts
}

func (s *Scope) collect(all map[string]*cel.Type) {
	if s.parent != nil {
		s.parent.collect(all)
	}
	for name, t := range s.vars {
		all[name] = t
	}
}
