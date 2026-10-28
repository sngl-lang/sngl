package interp

import (
	"fmt"
	"slices"
	"strings"

	"duckfam.us/sngl/ast"
	"duckfam.us/sngl/ir"
)

// Handle is the identity of one owned value: a struct whose type declares
// #[owned(release)]. Every copy of the value carries the same Handle, so a
// copy released releases them all, as it does on a target where the value is
// a pointer.
type Handle struct {
	ID  int
	Def *ir.StructDef
	// At is where the value was allocated, and ReleasedAt where it was given
	// back; the zero Pos while it is live.
	At         ast.Pos
	ReleasedAt ast.Pos
	released   bool
	// moved says a native took the value at ReleasedAt rather than the
	// program releasing it there.
	moved bool
	// holders is how many hold the value now -- one from the allocation,
	// one more per retain -- and Holders how many ever did: the value is
	// given back when the last releases it.
	holders, Holders int
}

// handles is every owned value a program allocated and has not given back,
// shared by every scope of the program the way its write log is.
type handles struct {
	next int
	live map[*Handle]bool
	// calls is the call being made, innermost last: where a release or a use
	// happened.
	calls []*ir.Call
	// simulated is the platform a run simulates (BuildSimulatedEnv), empty on
	// the interpreter's own.
	simulated string
	// retained is each value a native keeps a pointer to, by where it was
	// handed over and the lifetime that call ran in, and lifetime is the one
	// running now: the zero Key outside every effect, where the lifetime is
	// the component instance the call ran in, or the application's outside
	// every component.
	retained map[*Handle]retention
	lifetime Key
	// hosted is each node a simulated host made through its protocol, by
	// its mounted path, and connect the platform's own connect, looked up
	// once (hostProtocols).
	hosted       map[Key]*hosted
	connect      *ir.Func
	connectFound bool
}

type retention struct {
	at       ast.Pos
	lifetime Key
	inst     *Env
}

func (env *Env) owned() *handles {
	if env.handles == nil {
		env.handles = &handles{live: map[*Handle]bool{}}
	}
	return env.handles
}

func (h *handles) alloc(def *ir.StructDef, at ast.Pos) *Handle {
	h.next++
	hd := &Handle{ID: h.next, Def: def, At: at, holders: 1, Holders: 1}
	h.live[hd] = true
	return hd
}

func (h *handles) call() *ir.Call {
	if len(h.calls) == 0 {
		return nil
	}
	return h.calls[len(h.calls)-1]
}

// site is where the call being made is written: its callee, since a call's
// own position is its parenthesis. A call a pass wrote -- an argument's
// `c.cstring` -- is written nowhere, and is the call it converts for.
func (h *handles) site() ast.Pos {
	for _, c := range slices.Backward(h.calls) {
		switch {
		case c.AST == nil:
			continue
		case c.AST.Func != nil:
			return *c.AST.Func.ExprPos()
		}
		return c.AST.Pos
	}
	return ast.Pos{}
}

// enterCall records call as the site of whatever it releases or uses.
func (env *Env) enterCall(call *ir.Call) func() {
	h := env.owned()
	h.calls = append(h.calls, call)
	return func() { h.calls = h.calls[:len(h.calls)-1] }
}

// handleOf is the Handle v carries, or nil when it is not an owned value.
func handleOf(v any) *Handle {
	if s, ok := v.(*Struct); ok && s != nil {
		return s.Handle
	}
	return nil
}

// checkPassed refuses an argument already given back. A release's own
// argument is releaseCall's to judge, as a release twice.
func (env *Env) checkPassed(args []any) error {
	if c := env.owned().call(); c != nil && ir.IsRelease(c.Func) {
		return nil
	}
	for _, a := range args {
		if hd := handleOf(a); hd != nil && hd.released {
			return env.useAfterRelease(a.(*Struct), "passed", env.owned().site())
		}
	}
	return nil
}

// releaseCall runs fn, the release of its argument's type, and gives the
// value back once fn returns: the release reads what it is releasing. A
// synthesized release gives back what its argument holds, by calling each
// held value's own, so it counts nothing itself: an option of an owned value
// is that value on the interpreter, and counted here as well it was released
// twice.
func (env *Env) releaseCall(fn *ir.Func, args []any, run func() (any, error)) (any, error) {
	h := env.owned()
	var hd *Handle
	if len(args) == 1 && fn.Releases == nil {
		hd = handleOf(args[0])
	}
	if hd != nil && hd.moved {
		return nil, fmt.Errorf("%s released after it was moved: allocated at %s, moved at %s, released %s",
			hd.Def.Name, hd.At, hd.ReleasedAt, where(h.site()))
	}
	if r, ok := h.retained[hd]; hd != nil && ok {
		return nil, fmt.Errorf("%s released while a native retains it: allocated at %s, retained at %s, released %s",
			hd.Def.Name, hd.At, r.at, where(h.site()))
	}
	if hd != nil && hd.released {
		return nil, fmt.Errorf("%s released twice: allocated at %s, released %s and again %s",
			hd.Def.Name, hd.At, where(hd.ReleasedAt), where(h.site()))
	}
	v, err := run()
	if err != nil || hd == nil {
		return v, err
	}
	if hd.holders > 1 {
		hd.holders--
		return v, nil
	}
	hd.released, hd.ReleasedAt = true, h.site()
	delete(h.live, hd)
	return v, nil
}

// retainCall runs fn, the retain of its argument's type, and counts the
// holder it gives the value: the host counts a reference, and the tracker
// counts with it.
func (env *Env) retainCall(fn *ir.Func, args []any, run func() (any, error)) (any, error) {
	if err := env.checkPassed(args); err != nil {
		return nil, err
	}
	v, err := run()
	if err != nil {
		return nil, err
	}
	if hd := handleOf(args[0]); hd != nil {
		hd.holders++
		hd.Holders++
	}
	return v, nil
}

func (env *Env) useAfterRelease(s *Struct, how string, at ast.Pos) error {
	hd := s.Handle
	if hd.moved {
		return fmt.Errorf("%s %s after it was moved: allocated at %s, moved at %s, %s at %s",
			hd.Def.Name, how, hd.At, hd.ReleasedAt, how, at)
	}
	return fmt.Errorf("%s %s after its release: allocated at %s, released %s, %s at %s",
		hd.Def.Name, how, hd.At, where(hd.ReleasedAt), how, at)
}

// where names a release site: a position, or the release a pass wrote, which
// was written nowhere.
func where(at ast.Pos) string {
	if at.IsValid() {
		return "at " + at.String()
	}
	return "where the compiler releases it"
}

// move gives hd to the native being called, which releases it itself.
func (h *handles) move(hd *Handle) {
	hd.released, hd.moved, hd.ReleasedAt = true, true, h.site()
	delete(h.live, hd)
}

// retain records that the native being called keeps hd until the lifetime
// running now ends -- the effect whose handler is running, else the node of
// inst, the instance the call ran in -- or until the same call in the same
// lifetime hands it another, which is the native's to hold in its place.
func (h *handles) retain(hd *Handle, inst *Env) {
	if h.retained == nil {
		h.retained = map[*Handle]retention{}
	}
	r := retention{at: h.site(), lifetime: h.lifetime}
	if r.lifetime == (Key{}) {
		r.inst = inst
	}
	for prev, q := range h.retained {
		if q == r {
			delete(h.retained, prev)
		}
	}
	h.retained[hd] = r
}

// letGo ends every retention made while the lifetime at k ran: its node is
// gone, so the native holding the pointer is too.
func (h *handles) letGo(k Key) {
	for hd, r := range h.retained {
		if r.lifetime == k {
			delete(h.retained, hd)
		}
	}
}

// letGoInstance ends every retention made in inst's node and the nodes
// under it: the instance is unmounted, so the natives holding the pointers
// are gone with it. Called as the instance is evicted, which is before its
// `@unmount` releases what the cells hold.
func (h *handles) letGoInstance(inst *Env) {
	if inst == nil {
		return
	}
	for hd, r := range h.retained {
		if r.inst == inst {
			delete(h.retained, hd)
		}
	}
	for _, child := range inst.childEnvs {
		h.letGoInstance(child)
	}
	for _, child := range inst.callChildEnvs {
		h.letGoInstance(child)
	}
}

// instance is the component instance env runs in: the scope its snapshots
// were taken from, when that is a component's. A handler, a timer's tick
// and a component function run in the instance's scope or a snapshot of it,
// and a function they call in a snapshot of that. Nil outside every
// component, which is the application's lifetime.
func (env *Env) instance() *Env {
	e := env
	for e.origin != nil {
		e = e.origin
	}
	if e.Comp == nil {
		return nil
	}
	return e
}

// within runs fn as part of the lifetime at k.
func (h *handles) within(k Key, fn func() error) error {
	prev := h.lifetime
	h.lifetime = k
	defer func() { h.lifetime = prev }()
	return fn()
}

// Unmounted ends every retention: the whole tree is gone, and every native
// holding a pointer with it.
func (env *Env) Unmounted() {
	env.owned().retained = nil
}

// Leaks reports every owned value the program still holds, one line per
// place that allocated them. Asked after teardown, each is one nothing will
// ever give back.
func (env *Env) Leaks() []string {
	if env.handles == nil {
		return nil
	}
	var live []*Handle
	for hd := range env.handles.live {
		live = append(live, hd)
	}
	slices.SortFunc(live, func(a, b *Handle) int { return a.ID - b.ID })
	type site struct {
		name string
		at   ast.Pos
		// left and of say a value retained more than once was released by
		// some of its holders and not the rest.
		left, of int
	}
	var order []site
	count := map[site]int{}
	for _, hd := range live {
		s := site{name: hd.Def.Name, at: hd.At}
		if hd.holders < hd.Holders {
			s.left, s.of = hd.holders, hd.Holders
		}
		if count[s] == 0 {
			order = append(order, s)
		}
		count[s]++
	}
	out := make([]string, len(order))
	for i, s := range order {
		if s.of > 0 {
			out[i] = fmt.Sprintf("leak: the %s allocated at %s has %d of its %d holders left", s.name, s.at, s.left, s.of)
		} else {
			out[i] = fmt.Sprintf("leak: the %s allocated at %s was never released", s.name, s.at)
		}
		if n := count[s]; n > 1 {
			out[i] += fmt.Sprintf(", %d times", n)
		}
	}
	return out
}

// LeakError is Leaks as one error, or nil when nothing leaked.
func (env *Env) LeakError() error {
	leaks := env.Leaks()
	if len(leaks) == 0 {
		return nil
	}
	return fmt.Errorf("%s", strings.Join(leaks, "\n"))
}
