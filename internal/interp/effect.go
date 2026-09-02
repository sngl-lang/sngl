package interp

import (
	"fmt"
	"reflect"
	"sort"

	"git.duckfam.us/jonathan/sngl/ir"
)

// MountedEffect is one `effect` the current tree holds.
//
// It is not a Node. An effect renders nothing, so putting it in the tree would
// hand every host and every snapshot a node with nothing to draw; what it needs
// from the tree is only the one thing a Node would have given it, which is a
// lifetime. The mounter reaches an effect exactly when the branch it is written
// in is live, so the set of these IS that lifetime, and reconciling two of them
// is the whole of mount and unmount.
type MountedEffect struct {
	// Key is the mounted path, which is what says two effects are the same
	// effect across a re-mount.
	Key Key
	// On is the value the bracket is keyed on, or nil where the call site
	// named none. Identity, not a dependency: the same value is the same
	// effect however often the tree is rebuilt, and a different one is a
	// different effect whose lifetime has to begin.
	On    any
	HasOn bool
	// Mount and Unmount are the handler bodies, either of which may be nil --
	// one alone is a whole bracket.
	Mount   *ir.Func
	Unmount *ir.Func
	// Env is the scope the effect was written in, which is where its handlers
	// run and whose state they mutate.
	Env *Env
	// seq is when this lifetime began, counted across the whole set. Teardown
	// runs in reverse of it, and holding it on the entry means the running set
	// needs no second list kept in step with it.
	seq int
}

// sameLifetime reports whether b continues a's lifetime rather than beginning
// one. Deep equality because On is whatever the program keyed on -- a list or a
// struct is a perfectly good key, and == would panic on the first one.
func (a MountedEffect) sameLifetime(b MountedEffect) bool {
	if a.HasOn != b.HasOn {
		return false
	}
	if !a.HasOn {
		return true
	}
	return reflect.DeepEqual(a.On, b.On)
}

// Effects is the set of lifetimes currently running, and the reconciler that
// moves it to the set a new tree describes.
//
// Held apart from the View because it is the one thing about a render that must
// NOT be recomputed from scratch: the View is what the program says now, and
// this is what has already been done about it.
type Effects struct {
	live map[Key]MountedEffect
	next int // the seq to stamp on the next lifetime to begin
}

func NewEffects() *Effects {
	return &Effects{live: map[Key]MountedEffect{}}
}

// maxEffectSteps bounds the settle loop, counting handlers run rather than
// passes: one settle legitimately runs as many handlers as it has brackets to
// move, and each gets a pass of its own (see Reconcile). An effect that rekeys
// itself never settles, and the bound reports that instead of hanging.
const maxEffectSteps = 512

// Reconcile moves the running set one step towards what v describes, running at
// most one handler, and reports whether it ran one.
//
// One per call, because a handler's scope goes stale the moment another writes
// state. Every scope in v was built by a single Mount, so two handlers from one
// pass both read the state as it stood before either ran: two effects in a loop
// each incrementing a counter both read zero and both wrote one, and the second
// increment was simply lost. Settle re-mounts between steps, which is what makes
// each handler read what the last one left.
//
// Teardown comes first, and in reverse order of when the lifetimes began. An
// effect going away holds something the program has to give back, and setting up
// the next lifetime before tearing down the previous is how a program ends up
// holding two of whatever it was.
//
// A handler is passed the value ITS OWN lifetime is keyed on -- the ending one
// for `@unmount`, the beginning one for `@mount`. By teardown time the cell has
// already been written, so a handler reading it would see what it is not
// tearing down.
func (fx *Effects) Reconcile(v *View, root *Env) (bool, error) {
	if fx == nil {
		return false, nil
	}
	next := map[Key]MountedEffect{}
	var nextOrder []Key
	if v != nil {
		for _, e := range v.Effects {
			if _, dup := next[e.Key]; dup {
				continue
			}
			next[e.Key] = e
			nextOrder = append(nextOrder, e.Key)
		}
	}

	// Teardown. Dead brackets leave the running set whether or not they have a
	// handler to run, so a bracket with only an @mount does not cost a step.
	for _, prev := range fx.endingLifetimes(next) {
		delete(fx.live, prev.Key)
		if prev.Unmount != nil {
			return true, fx.run(prev, prev.Unmount, root)
		}
	}

	// Setup, in the order the tree wrote them.
	for _, k := range nextOrder {
		cur := next[k]
		if prev, held := fx.live[k]; held && prev.sameLifetime(cur) {
			continue
		}
		cur.seq = fx.next
		fx.next++
		fx.live[k] = cur
		if cur.Mount != nil {
			return true, fx.run(cur, cur.Mount, root)
		}
	}
	return false, nil
}

// endingLifetimes is every running bracket v no longer describes, newest first.
func (fx *Effects) endingLifetimes(next map[Key]MountedEffect) []MountedEffect {
	var out []MountedEffect
	for k, prev := range fx.live {
		if cur, still := next[k]; still && prev.sameLifetime(cur) {
			continue
		}
		out = append(out, prev)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].seq > out[j].seq })
	return out
}

// run executes one side of a bracket, passing the lifetime's own key value.
func (fx *Effects) run(e MountedEffect, fn *ir.Func, root *Env) error {
	if fn == nil {
		return nil
	}
	env := e.Env
	if env == nil {
		env = root
	}
	if env == nil {
		return fmt.Errorf("effect at %s has no scope to run in", e.Key)
	}
	// The scope was taken by the mount that placed this bracket, so its copy of
	// the program's state is as old as that mount. A teardown is where that
	// matters: an ending bracket is no longer in the tree, so no later mount
	// refreshes it, and writing the whole scope back would put the state back
	// with it.
	env.refreshFrom(root)
	var args []any
	if len(fn.Params) > 0 {
		args = []any{e.On}
	}
	if _, err := env.runEventHandlerValues(fn, args); err != nil {
		return err
	}
	if root != nil && env != root {
		root.RebindFrom(env)
	}
	return nil
}

// Settle reconciles until nothing more runs, re-mounting before each step
// because the previous handler's writes describe a different tree -- and,
// crucially, because the scopes the next handler will run in are built by that
// Mount. Returns the final view.
func Settle(fx *Effects, env *Env) (*View, error) {
	var v *View
	for range maxEffectSteps {
		next, err := Mount(env)
		if err != nil {
			return nil, err
		}
		v = next
		ran, err := fx.Reconcile(v, env)
		if err != nil {
			return nil, err
		}
		if !ran {
			return v, nil
		}
	}
	return v, fmt.Errorf("effects did not settle in %d steps; an effect is rekeying itself", maxEffectSteps)
}

// effectOf reads an effect declaration off a node instantiation, or reports
// false when the node is not one. Recognised by the kind its declaration
// carries, never by its name.
func effectOf(inst *ir.NodeInst, env *Env, key Key) (MountedEffect, bool) {
	if inst == nil || inst.Component == nil || inst.Component.Builtin != ir.BuiltinEffect {
		return MountedEffect{}, false
	}
	e := MountedEffect{Key: key, Env: env}
	for _, arg := range inst.Props {
		if arg.Name != effectKeyProp {
			continue
		}
		v, err := env.Eval(arg.Value)
		if err != nil {
			// A key that will not evaluate is not a reason to lose the
			// bracket: the lifetime still has to end when the node goes.
			break
		}
		e.On, e.HasOn = v, true
	}
	for _, h := range inst.Handlers {
		switch h.Name {
		case "mount":
			e.Mount = h.Func
		case "unmount":
			e.Unmount = h.Func
		}
	}
	return e, true
}

// effectKeyProp is the prop sngl:builtin's `effect` keys its identity on.
const effectKeyProp = "on"
