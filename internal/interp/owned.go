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
	// running now: the zero Key outside every effect, which is the
	// application's.
	retained map[*Handle]retention
	lifetime Key
}

type retention struct {
	at       ast.Pos
	lifetime Key
}

func (env *Env) owned() *handles {
	if env.handles == nil {
		env.handles = &handles{live: map[*Handle]bool{}}
	}
	return env.handles
}

func (h *handles) alloc(def *ir.StructDef, at ast.Pos) *Handle {
	h.next++
	hd := &Handle{ID: h.next, Def: def, At: at}
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
// value back once fn returns: the release reads what it is releasing.
func (env *Env) releaseCall(fn *ir.Func, args []any, run func() (any, error)) (any, error) {
	h := env.owned()
	var hd *Handle
	if len(args) == 1 {
		hd = handleOf(args[0])
	}
	if hd != nil && hd.moved {
		return nil, fmt.Errorf("%s released after it was moved: allocated at %s, moved at %s, released at %s",
			hd.Def.Name, hd.At, hd.ReleasedAt, h.site())
	}
	if r, ok := h.retained[hd]; hd != nil && ok {
		return nil, fmt.Errorf("%s released while a native retains it: allocated at %s, retained at %s, released at %s",
			hd.Def.Name, hd.At, r.at, h.site())
	}
	if hd != nil && hd.released {
		return nil, fmt.Errorf("%s released twice: allocated at %s, released at %s and again at %s",
			hd.Def.Name, hd.At, hd.ReleasedAt, h.site())
	}
	v, err := run()
	if err != nil || hd == nil {
		return v, err
	}
	hd.released, hd.ReleasedAt = true, h.site()
	delete(h.live, hd)
	return v, nil
}

func (env *Env) useAfterRelease(s *Struct, how string, at ast.Pos) error {
	hd := s.Handle
	if hd.moved {
		return fmt.Errorf("%s %s after it was moved: allocated at %s, moved at %s, %s at %s",
			hd.Def.Name, how, hd.At, hd.ReleasedAt, how, at)
	}
	return fmt.Errorf("%s %s after its release: allocated at %s, released at %s, %s at %s",
		hd.Def.Name, how, hd.At, hd.ReleasedAt, how, at)
}

// move gives hd to the native being called, which releases it itself.
func (h *handles) move(hd *Handle) {
	hd.released, hd.moved, hd.ReleasedAt = true, true, h.site()
	delete(h.live, hd)
}

// retain records that the native being called keeps hd until the lifetime
// running now ends.
func (h *handles) retain(hd *Handle) {
	if h.retained == nil {
		h.retained = map[*Handle]retention{}
	}
	h.retained[hd] = retention{at: h.site(), lifetime: h.lifetime}
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
	}
	var order []site
	count := map[site]int{}
	for _, hd := range live {
		s := site{hd.Def.Name, hd.At}
		if count[s] == 0 {
			order = append(order, s)
		}
		count[s]++
	}
	out := make([]string, len(order))
	for i, s := range order {
		out[i] = fmt.Sprintf("leak: the %s allocated at %s was never released", s.name, s.at)
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
