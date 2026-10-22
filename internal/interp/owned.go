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
}

// handles is every owned value a program allocated and has not given back,
// shared by every scope of the program the way its write log is.
type handles struct {
	next int
	live map[*Handle]bool
	// calls is the call being made, innermost last: where a release or a use
	// happened.
	calls []*ir.Call
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
// own position is its parenthesis.
func (h *handles) site() ast.Pos {
	c := h.call()
	switch {
	case c == nil || c.AST == nil:
		return ast.Pos{}
	case c.AST.Func != nil:
		return *c.AST.Func.ExprPos()
	}
	return c.AST.Pos
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
	return fmt.Errorf("%s %s after its release: allocated at %s, released at %s, %s at %s",
		hd.Def.Name, how, hd.At, hd.ReleasedAt, how, at)
}

// Leaks reports every owned value the program still holds. Asked after
// teardown, each is one nothing will ever give back.
func (env *Env) Leaks() []string {
	if env.handles == nil {
		return nil
	}
	var live []*Handle
	for hd := range env.handles.live {
		live = append(live, hd)
	}
	slices.SortFunc(live, func(a, b *Handle) int { return a.ID - b.ID })
	out := make([]string, len(live))
	for i, hd := range live {
		out[i] = fmt.Sprintf("leak: the %s allocated at %s was never released", hd.Def.Name, hd.At)
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
