package interp

import (
	"errors"
	"fmt"

	"git.duckfam.us/jonathan/sngl/ir"
)

// A Stream is an iter<T> whose elements arrive one at a time from outside the
// program -- the lines of a file, what a process prints -- and are never held
// together. It is the one iterable that cannot say how long it is, so a loop
// over one asks its `else` by taking the first element.
//
// It is read once: a second loop over the same stream would see what the first
// left, which is no sequence anyone wrote.
type Stream struct {
	// Next returns the next element, or false at the end.
	Next func() (any, bool, error)
	// Stop is called when a loop leaves before the end, so the source can
	// finish what it records of itself.
	Stop func() error
	read bool
}

// errStreamRead is a second loop over one stream.
var errStreamRead = errors.New("a stream is read once, and this one has been")

// execStreamLoop runs a statement loop over a stream.
func (env *Env) execStreamLoop(s *ir.For, st *Stream) error {
	if st.read {
		return errStreamRead
	}
	st.read = true
	item, ok, err := st.Next()
	if err != nil {
		return err
	}
	if !ok {
		return env.execStmts(s.Else)
	}
	for i := 0; ; i++ {
		bindLoopElem(env, s, i, item, nil)
		done, err := env.runLoopBody(s)
		if err != nil {
			return err
		}
		if done {
			env.unbindLoopVars(s)
			if st.Stop != nil {
				return st.Stop()
			}
			return nil
		}
		if item, ok, err = st.Next(); err != nil {
			return err
		} else if !ok {
			break
		}
	}
	env.unbindLoopVars(s)
	return nil
}

// A BuildHost answers sngl:x/gen's host API: the reads a function folded at
// build time makes of the machine the build runs on. Only the build's
// evaluator supplies one, and a call with none is an error -- such a call has
// no meaning anywhere else.
type BuildHost interface {
	// Call answers the intrinsic id. caller is the innermost function the
	// call was written in, which is the package a grant is asked of.
	Call(id string, caller *ir.Func, call *ir.Call, args []any) (any, error)
}

// buildFrame is the host and the functions the interpreter is inside, shared
// by every snapshot of the env it was set on.
type buildFrame struct {
	host  BuildHost
	stack []*ir.Func
}

// SetBuildHost makes env answer sngl:x/gen's host API through h.
func (env *Env) SetBuildHost(h BuildHost) {
	env.build = &buildFrame{host: h}
}

// enterFunc records that fn's body is running, for a host call to know who
// made it. The returned func leaves it.
func (env *Env) enterFunc(fn *ir.Func) func() {
	if env.build == nil || fn == nil {
		return func() {}
	}
	b := env.build
	b.stack = append(b.stack, fn)
	return func() { b.stack = b.stack[:len(b.stack)-1] }
}

// enterFrame is enterFunc for a body run through a value, whose code may be
// the root's: a nil fn is pushed as such rather than skipped, so a host call in
// a lambda the root wrote is not asked of whichever function called it.
func (env *Env) enterFrame(fn *ir.Func) func() {
	if env.build == nil {
		return func() {}
	}
	b := env.build
	b.stack = append(b.stack, fn)
	return func() { b.stack = b.stack[:len(b.stack)-1] }
}

// runningFunc is the function whose body is running, nil at the root.
func (env *Env) runningFunc() *ir.Func {
	if env.build == nil || len(env.build.stack) == 0 {
		return nil
	}
	return env.build.stack[len(env.build.stack)-1]
}

// callBuildOnly answers a call to an intrinsic only the build's evaluator
// answers.
func (env *Env) callBuildOnly(call *ir.Call) (any, error) {
	fn := call.Func
	if env.build == nil {
		return nil, fmt.Errorf("%s answers only at build time", fn.Intrinsic)
	}
	args, err := env.boundArgs(fn, call.Args)
	if err != nil {
		return nil, err
	}
	return env.build.host.Call(fn.Intrinsic, env.runningFunc(), call, args)
}

// boundArgs evaluates a call's arguments into fn's parameter order, a
// parameter the call left out taking its default. A method's receiver comes
// first.
func (env *Env) boundArgs(fn *ir.Func, callArgs []ir.CallArg) ([]any, error) {
	var recv []any
	if fn.Receiver != "" && len(callArgs) == len(fn.Params)+1 {
		v, err := env.Eval(callArgs[0].Value)
		if err != nil {
			return nil, err
		}
		recv, callArgs = []any{v}, callArgs[1:]
	}
	out := make([]any, len(fn.Params))
	set := make([]bool, len(fn.Params))
	positional := 0
	for _, a := range callArgs {
		v, err := env.Eval(a.Value)
		if err != nil {
			return nil, err
		}
		i := positional
		if a.Name != "" {
			i = -1
			for j, p := range fn.Params {
				if p.Name == a.Name {
					i = j
				}
			}
			if i < 0 {
				return nil, fmt.Errorf("%s has no parameter %s", fn.Name, a.Name)
			}
		} else {
			positional++
		}
		if i < len(out) {
			out[i], set[i] = v, true
		}
	}
	for i, p := range fn.Params {
		if set[i] || p.Default == nil {
			continue
		}
		v, err := env.Eval(p.Default)
		if err != nil {
			return nil, err
		}
		out[i] = v
	}
	return append(recv, out...), nil
}
