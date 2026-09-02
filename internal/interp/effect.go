package interp

import (
	"fmt"
	"reflect"

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
	live  map[Key]MountedEffect
	order []Key // mount order, so teardown can run in reverse
}

func NewEffects() *Effects {
	return &Effects{live: map[Key]MountedEffect{}}
}

// maxEffectRounds bounds the settle loop. A mount handler may legitimately
// write state that mounts another effect, so one pass is not enough; an effect
// that rekeys itself would never settle, and a bound reports that as an error
// instead of hanging the program.
const maxEffectRounds = 16

// Reconcile brings the running set to what v describes, and reports whether any
// handler ran.
//
// Teardown comes first and in reverse mount order. An effect that is going away
// holds something the program has to give back, and running the next lifetime's
// setup before the previous one's teardown is how a program ends up holding two
// of whatever it was: the fixture that pins this asserts the order, because a
// count alone cannot tell the two apart.
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

	ran := false
	for i := len(fx.order) - 1; i >= 0; i-- {
		k := fx.order[i]
		prev, held := fx.live[k]
		if !held {
			continue
		}
		if cur, still := next[k]; still && prev.sameLifetime(cur) {
			continue
		}
		if err := fx.run(prev, prev.Unmount, root); err != nil {
			return ran, err
		}
		ran = ran || prev.Unmount != nil
		delete(fx.live, k)
	}

	for _, k := range nextOrder {
		cur := next[k]
		if prev, held := fx.live[k]; held && prev.sameLifetime(cur) {
			continue
		}
		if err := fx.run(cur, cur.Mount, root); err != nil {
			return ran, err
		}
		ran = ran || cur.Mount != nil
		fx.live[k] = cur
	}
	fx.order = nextOrder
	return ran, nil
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

// Settle reconciles until nothing more runs, re-mounting between rounds because
// a handler that wrote state described a different tree by doing so. Returns the
// final view.
func Settle(fx *Effects, env *Env) (*View, error) {
	var v *View
	for round := 0; round < maxEffectRounds; round++ {
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
	return v, fmt.Errorf("effects did not settle in %d rounds; an effect is rekeying itself", maxEffectRounds)
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
