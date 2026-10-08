package interp

import (
	"fmt"

	"duckfam.us/sngl/ir"
)

// Session is a program the interpreter is holding open: its scope, its mounted
// tree, and its timer schedule. Every operation on it answers in patches.
//
// Single-threaded, and Env holds no lock -- a host's events must reach it
// through a queue rather than from the toolkit's own goroutine.
type Session struct {
	Pkg    *ir.Package
	Comp   string
	Env    *Env
	Clock  Clock
	Timers *Timers

	view *View
	fx   *Effects
}

// NewSession checks nothing and lowers nothing: it takes a package the caller
// has already checked, and mounts it.
func NewSession(pkg *ir.Package, comp string, clock Clock) (*Session, error) {
	if clock == nil {
		clock = NewVirtual()
	}
	// The interpreter is a target, and a target gets the bodies its own
	// platform package declares. Nothing here lowers -- the interpreter is
	// written against checked IR -- so this is the one thing it still takes
	// from the pipeline: without it `sngl:time`'s `timer` is the empty stub
	// every stdlib component is before a platform implements it, and schedules
	// nothing.
	ir.SpecializeForTarget(pkg, InterpreterPlatform, "")
	env, err := sessionEnv(pkg, comp)
	if err != nil {
		return nil, err
	}
	timers := NewTimers(clock)
	// Settling here rather than after: a mount handler runs before anything has
	// seen the tree, so what a host is first handed already reflects it. An
	// effect that fetches has its request in flight before the first frame,
	// which is the only reading of "when the node enters the tree" that does
	// not show a frame the program never described.
	fx := NewEffects()
	view, err := Settle(fx, env)
	if err != nil {
		return nil, err
	}
	timers.Retarget(view)
	return &Session{Pkg: pkg, Comp: comp, Env: env, Clock: clock, Timers: timers, view: view, fx: fx}, nil
}

// sessionEnv scopes a session to comp, or to the program when comp is empty.
func sessionEnv(pkg *ir.Package, comp string) (*Env, error) {
	if comp == "" {
		return BuildProgramEnv(pkg), nil
	}
	return BuildEnv(pkg, comp)
}

// View is the tree as the session currently holds it -- what a host has
// mounted, assuming it applied every patch handed to it.
func (s *Session) View() *View { return s.view }

// SetComponentVar assigns a component variable by name. It is how a REPL, an
// inspector, or a test moves state without going through a handler.
func SetComponentVar(s *Session, name string, v any) bool {
	if s == nil || s.Env == nil || s.Env.Comp == nil {
		return false
	}
	for _, sym := range s.Env.Comp.Vars {
		if sym.SymName() == name {
			s.Env.Set(sym, v)
			return true
		}
	}
	return false
}

// Attach brings a host up to the session's current tree, as one batch of
// creations. A host joining a program that is already running -- a second
// window on one session, or an inspector beside it -- starts here.
func (s *Session) Attach(h Host) error {
	return Apply(h, Diff(nil, s.view))
}

// Sync re-mounts against current state and reports what changed. Every
// operation below ends in one, and a caller that mutates state directly (a
// REPL assigning a var) calls it itself.
func (s *Session) Sync() ([]Patch, error) {
	// Diffed per round rather than once at the end: a mount handler may write
	// state, and the patches a host has to apply are the ones between the tree
	// it holds and each tree that followed -- collapsing them would drop the
	// intermediate creations the later rounds' keys are relative to.
	var patches []Patch
	var budget settleBudget
	for {
		next, err := Mount(s.Env)
		if err != nil {
			return nil, err
		}
		patches = append(patches, Diff(s.view, next)...)
		s.view = next
		// Before the effects settle: a mount handler may write state a deadline
		// is positioned on, and the next round re-mounts anyway.
		s.Timers.Retarget(next)
		key, ran, err := s.fx.Reconcile(next, s.Env)
		if err != nil {
			return patches, err
		}
		if !ran {
			return patches, nil
		}
		if err := budget.spend(key); err != nil {
			return patches, err
		}
	}
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
// Args are what the widget reported -- the text a field now holds, say. A
// two-way binding desugars to a handler that assigns its parameter, so without
// them typing changes nothing.
//
// The handler runs in the node's own scope, which for a loop iteration is a
// snapshot of the enclosing one -- so mutations are written back afterwards,
// the same way a test's event trigger does it.
func (s *Session) Invoke(key Key, event string, args ...any) ([]Patch, error) {
	n, ok := s.view.At(key)
	if !ok {
		return nil, fmt.Errorf("no node at %s", key)
	}
	env := n.Env
	if env == nil {
		env = s.Env
	}
	if n.CompEnv.cellByEvent(n.Inst, event) && len(args) > 0 {
		n.CompEnv.writeCells(n.Inst, event, []any{coerceEventArg(eventPayload(n.Inst, event), args[0])})
		if !boundByEvent(n.Inst, event) && !hasHandler(n.Handlers, event) {
			s.Env.RebindFrom(env)
			return s.Sync()
		}
	}
	bound := boundByEvent(n.Inst, event)
	if bound && len(args) > 0 {
		if err := env.writeBindings(n.Inst, event, []any{coerceEventArg(eventPayload(n.Inst, event), args[0])}); err != nil {
			return nil, err
		}
	}
	for _, h := range n.Handlers {
		if h.Name != event {
			continue
		}
		fn, ok := h.Body.(*ir.Func)
		if !ok {
			return nil, fmt.Errorf("%s has no runnable body for @%s", key, event)
		}
		if _, err := env.underHandler(n.Context, func() (any, error) { return env.runEventHandlerValues(fn, args) }); err != nil {
			return nil, err
		}
		s.Env.RebindFrom(env)
		return s.Sync()
	}
	if bound {
		s.Env.RebindFrom(env)
		return s.Sync()
	}
	return nil, fmt.Errorf("%s declares no @%s", key, event)
}

func hasHandler(hs []Handler, event string) bool {
	for _, h := range hs {
		if h.Name == event {
			return true
		}
	}
	return false
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
//
// The running effects settle against the new package for the same reason the
// state is carried and the timers rebased: what the new source describes is
// what should be running. A bracket the edit removed ends -- in the program
// that declared it, before the swap, since that is where its teardown and its
// scope live. A bracket it added begins, in the final settle below. One that
// survives keeps its lifetime and takes the new source's handlers, because
// holding the old entry left it running a func of the package the reload threw
// away, in a scope whose symbols the session no longer binds.
func (s *Session) Reload(pkg *ir.Package) ([]Patch, error) {
	// The reloaded program is a program this target runs, so it takes this
	// platform's bodies exactly as the first one did. Without it a reload
	// silently dropped every timer: `timer` reverted to the empty stub.
	ir.SpecializeForTarget(pkg, InterpreterPlatform, "")
	env, err := sessionEnv(pkg, s.Comp)
	if err != nil {
		return nil, err
	}
	carry := func() {
		carried := s.bindings()
		for owner, syms := range varsOf(pkg, s.Comp) {
			for _, sym := range syms {
				prev, held := carried[VarKeyOf(owner, sym)]
				if !held || !sameType(prev.typ, sym.SymType()) {
					continue
				}
				env.Set(sym, prev.val)
			}
		}
	}
	carry()

	// Mounted first only for the key set Retarget needs: which brackets the new
	// source describes decides which of the running ones are ending.
	next, err := Mount(env)
	if err != nil {
		return nil, err
	}
	// Before the swap: an ending lifetime's handler and scope belong to the
	// program still installed, and s.Env is the root it exchanges state with.
	if err := s.fx.Retarget(next, s.Env); err != nil {
		return nil, err
	}
	// A teardown releases what its lifetime held, and it wrote that into the
	// old env. Carrying again is how it reaches the reloaded one -- the first
	// carry is a snapshot from before those handlers ran.
	carry()
	if next, err = Mount(env); err != nil {
		return nil, err
	}
	patches := Diff(s.view, next)
	s.Pkg, s.Env, s.view = pkg, env, next
	// Carried rather than rebuilt: Retarget keys on the mounted path, so a
	// deadline the edit did not move keeps its phase and saving a file does not
	// restart every timer in the program.
	s.Timers.Retarget(next)
	// And the setup half, which is the new program's: a bracket the edit added
	// mounts here, and whatever its handler writes is patched like any other
	// settle.
	settled, err := s.Sync()
	return append(patches, settled...), err
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
