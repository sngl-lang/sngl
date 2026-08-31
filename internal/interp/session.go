package interp

import (
	"fmt"

	"git.duckfam.us/jonathan/sngl/ir"
)

// Session is a program the interpreter is holding open: its scope, its mounted
// tree, and its timer schedule, with the operations that move it forward.
//
// Everything before this re-mounted per call -- ResolveElementRef and the
// snapshot printer each build a tree, read it, and drop it. That is correct and
// wasteful, and it is not what a window can do: a window needs the tree to
// persist so a change becomes a patch to what is already on screen. A Session
// is that persistence, and every operation on it answers in patches.
//
// It is deliberately single-threaded. A host owns its toolkit's loop and will
// deliver events from it; those must reach a Session through a queue, because
// Env is mutation-heavy and holds no lock.
type Session struct {
	Pkg    *ir.Package
	Comp   string
	Env    *Env
	Clock  Clock
	Timers *Timers

	view *View
}

// NewSession checks nothing and lowers nothing: it takes a package the caller
// has already checked, and mounts it.
func NewSession(pkg *ir.Package, comp string, clock Clock) (*Session, error) {
	if clock == nil {
		clock = NewVirtual()
	}
	env, err := BuildEnv(pkg, comp)
	if err != nil {
		return nil, err
	}
	timers, err := NewTimers(clock, env)
	if err != nil {
		return nil, err
	}
	view, err := Mount(env)
	if err != nil {
		return nil, err
	}
	return &Session{Pkg: pkg, Comp: comp, Env: env, Clock: clock, Timers: timers, view: view}, nil
}

// View is the tree as the session currently holds it -- what a host has
// mounted, assuming it applied every patch handed to it.
func (s *Session) View() *View { return s.view }

// Sync re-mounts against current state and reports what changed. Every
// operation below ends in one, and a caller that mutates state directly (a
// REPL assigning a var) calls it itself.
func (s *Session) Sync() ([]Patch, error) {
	next, err := Mount(s.Env)
	if err != nil {
		return nil, err
	}
	patches := Diff(s.view, next)
	s.view = next
	return patches, nil
}

// Tick advances the clock to the next timer deadline and fires what is due.
func (s *Session) Tick() ([]Patch, error) {
	if _, err := s.Timers.Tick(s.Env); err != nil {
		return nil, err
	}
	return s.Sync()
}

// FireDue fires the timers due at the clock's current time. This is what a
// window's event loop calls; Tick is what a test calls.
func (s *Session) FireDue() ([]Patch, error) {
	if _, err := s.Timers.FireDue(s.Env); err != nil {
		return nil, err
	}
	return s.Sync()
}

// Invoke runs a node's declared event handler and reports what it changed.
//
// The handler runs in the node's own scope, which for a loop iteration is a
// snapshot of the enclosing one -- so mutations are written back afterwards,
// the same way a test's event trigger does it.
func (s *Session) Invoke(key Key, event string) ([]Patch, error) {
	n, ok := s.view.At(key)
	if !ok {
		return nil, fmt.Errorf("no node at %s", key)
	}
	for _, h := range n.Handlers {
		if h.Name != event {
			continue
		}
		fn, ok := h.Body.(*ir.Func)
		if !ok {
			return nil, fmt.Errorf("%s has no runnable body for @%s", key, event)
		}
		env := n.Env
		if env == nil {
			env = s.Env
		}
		if _, err := env.runEventHandler(fn, nil, event); err != nil {
			return nil, err
		}
		s.Env.RebindFrom(env)
		return s.Sync()
	}
	return nil, fmt.Errorf("%s declares no @%s", key, event)
}

// Reload replaces the program with a freshly checked one and reports the
// patches that bring the mounted tree to it.
//
// State is carried across by VarKey -- the declaring scope's name and the
// variable's -- because the new package shares no symbol with the old. A
// binding whose name and type both survive keeps its value; one whose type
// changed is reinitialised, since carrying a value across a type change is how
// a session ends up holding something no expression in the program could have
// produced. A variable that appeared runs its initialiser, and one that is gone
// is simply not carried.
//
// Timer phase is rebased rather than reset, so saving a file does not restart
// every timer in the program.
func (s *Session) Reload(pkg *ir.Package) ([]Patch, error) {
	carried := s.bindings()

	env, err := BuildEnv(pkg, s.Comp)
	if err != nil {
		return nil, err
	}
	for owner, syms := range varsOf(pkg, s.Comp) {
		for _, sym := range syms {
			prev, held := carried[VarKeyOf(owner, sym)]
			if !held || !sameType(prev.typ, sym.SymType()) {
				continue
			}
			env.Set(sym, prev.val)
		}
	}

	timers, err := NewTimers(s.Clock, env)
	if err != nil {
		return nil, err
	}
	timers.Rebase(s.Timers)

	next, err := Mount(env)
	if err != nil {
		return nil, err
	}
	patches := Diff(s.view, next)
	s.Pkg, s.Env, s.Timers, s.view = pkg, env, timers, next
	return patches, nil
}

type binding struct {
	val any
	typ *ir.Type
}

// bindings projects the session's live state onto reload-stable keys.
func (s *Session) bindings() map[VarKey]binding {
	out := map[VarKey]binding{}
	for owner, syms := range varsOf(s.Pkg, s.Comp) {
		for _, sym := range syms {
			if v, ok := s.Env.Value(sym); ok {
				out[VarKeyOf(owner, sym)] = binding{val: v, typ: sym.SymType()}
			}
		}
	}
	return out
}

// varsOf lists the mutable bindings a reload has to carry, by declaring scope.
// Package vars are owned by "", a component's by its name -- two scopes may
// declare one name, which is why the owner is part of the key.
func varsOf(pkg *ir.Package, comp string) map[string][]ir.Symbol {
	out := map[string][]ir.Symbol{}
	if pkg == nil {
		return out
	}
	for _, v := range pkg.Vars {
		out[""] = append(out[""], v)
	}
	if c := FindComponent(pkg, comp); c != nil {
		for _, v := range c.Vars {
			out[c.Name] = append(out[c.Name], v)
		}
	}
	return out
}

// sameType compares two types by their written form. The new package's types
// are different values from the old one's, so nothing pointer-based would
// match, and a name is what a reload can meaningfully hold constant.
func sameType(a, b *ir.Type) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.String() == b.String()
}
