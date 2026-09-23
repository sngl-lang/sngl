package lower

import (
	"slices"
	"testing"
)

// without is the Features under which only the named capabilities' passes run:
// everything claimed, less what the caller takes away. It is what an empty
// Caps plus one No-field used to say, and it is a test's spelling -- a target
// declares what it *can* do, so there is no production constructor for this.
//
// The names are `sngl:x/gen`'s, read through the same table a target's marks
// are, so a test names a capability the way the declaration does and a typo
// fails here rather than silently running a different set of passes.
func without(names ...string) Features {
	f := NoLowering()
	for _, n := range names {
		field, ok := capabilityField[n]
		if !ok {
			panic("lower: no capability " + n + "; see sngl:x/gen's Capability enum")
		}
		*field(&f) = false
	}
	return f
}

// wanting is without's counterpart for the five #[gen.wants] passes, which
// read the other way round: naming one turns its pass on.
func wanting(names ...string) Features {
	f := NoLowering()
	for _, n := range names {
		field, ok := passField[n]
		if !ok {
			panic("lower: no pass " + n + "; see sngl:x/gen's Pass enum")
		}
		*field(&f) = true
	}
	return f
}

// withAsyncPost and withInsertBefore are `without` plus one of the three
// capabilities a target *grants* rather than withholds: a platform that can
// reach its drawing thread, and one whose container can place a child. Both
// read the same way round as a want -- true is the capable answer -- so they
// cannot be spelled by taking something away.
func withAsyncPost(names ...string) Features {
	f := without(names...)
	f.AsyncPost = true
	return f
}

func withInsertBefore(names ...string) Features {
	f := without(names...)
	f.InsertBefore = true
	return f
}

// The zero value claims nothing, so every capability-gated pass runs for it.
// That is the whole of the polarity: a target that has not heard of a
// construct gets the pass that converts it away, which is what lets one be
// added without breaking every target at once.
//
// A want is the exception and has to be, which is the whole reason
// `#[gen.wants]` is a separate mark: a request reads the other way round, so
// silence cannot turn one on. What the zero value must cover is every
// *capability*-gated pass.
//
// Asserted against the pass registry rather than a written list, so a pass
// added later is covered by this without being added to it.
func TestZeroFeaturesRunsEveryCapabilityGatedPass(t *testing.T) {
	always := EnabledPasses(NoLowering())
	wanted := EnabledPasses(wanting("structComponents", "stdlibContextParam", "focusOrder", "canvas", "reactiveCanvas"))
	// The three capabilities a target *grants* read the same way round as a
	// want: a platform that can place a child rather than only append, one
	// that can reach its drawing thread, one whose language can spawn. Silence
	// withholds those too, so a pass behind one is no more the zero value's
	// than a request is -- passSlotChildInstances wants InsertBefore beside a
	// withdrawn Reactivity.
	granted := withInsertBefore("reactivity")
	granted.AsyncPost, granted.AsyncSpawn = true, true
	grantGated := EnabledPasses(granted)
	all := EnabledPasses(Features{})
	for _, p := range passes {
		switch {
		case slices.Contains(always, p.name):
			continue // ungated: it runs whatever the target said
		case slices.Contains(wanted, p.name):
			continue // a request, which silence correctly does not make
		case slices.Contains(grantGated, p.name) && !slices.Contains(all, p.name):
			continue // behind a grant, which silence also withholds
		}
		if !slices.Contains(all, p.name) {
			t.Errorf("pass %s does not run for the zero Features; a target that claims nothing must get every capability-gated lowering", p.name)
		}
	}
	if len(all) <= len(always) {
		t.Fatalf("the zero Features enabled %d passes and NoLowering enabled %d; the two must differ or nothing is gated", len(all), len(always))
	}
}

// And NoLowering is its opposite: every construct claimed, so only the passes
// that run for everyone do.
func TestNoLoweringRunsOnlyTheUngatedPasses(t *testing.T) {
	for _, p := range passes {
		if p.enabled(NoLowering()) && !p.enabled(Features{}) {
			t.Errorf("pass %s runs when every capability is held but not when none is; its predicate is inverted", p.name)
		}
	}
}

// A capability withdrawn turns on at least its own pass, which is what lets a
// test name one and reason about what arrives.
//
// At least, not exactly: order_test.go's soleGate is where the count is
// pinned, and two of these ask for more than one. Withdrawing Reactivity buys
// the whole instance runtime, and Declarative buys the escape analysis that
// only has something to analyse once the tree is flat.
func TestWithoutTurnsOnItsPass(t *testing.T) {
	base := EnabledPasses(NoLowering())
	for _, name := range []string{"ternary", "lambda", "unitType", "enumType", "reactivity", "declarative"} {
		t.Run(name, func(t *testing.T) {
			var added []string
			for _, p := range EnabledPasses(without(name)) {
				if !slices.Contains(base, p) {
					added = append(added, p)
				}
			}
			if len(added) == 0 {
				t.Errorf("withdrawing %s enabled no pass at all; a capability nothing gates is a word with no consequence", name)
			}
		})
	}
}

// InterpreterFeatures differs from NoLowering by exactly one capability. The
// interpreter has no ir.Effect case, so passEffect has to have run before it
// sees the tree -- and if that ever stops being the one difference, the
// comment on InterpreterFeatures is wrong rather than merely out of date.
func TestInterpreterWithholdsOnlyEffects(t *testing.T) {
	f := InterpreterFeatures()
	if f.Effects {
		t.Error("InterpreterFeatures holds Effects, so passEffect will not run and the interpreter meets a node it has no case for")
	}
	f.Effects = true
	if f != NoLowering() {
		t.Errorf("InterpreterFeatures differs from NoLowering by more than Effects:\n  got:  %s\n  want: %s", f, NoLowering())
	}
}
