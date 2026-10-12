package interp

import (
	"testing"
)

const clockSrc = `
import ui "sngl:ui"
import time "sngl:time"

component inner ui.node {
	var at = time.now()

	ui.text(value=string(at))
}

component main ui.node {
	var at = time.now()

	inner
}
`

// A run has one clock and every scope in it reads that clock. Both halves were
// wrong and each silently: BuildEnv evaluates a component's var initializers,
// so a caller assigning the field afterwards had already read the wall clock;
// and a child component's env is built field by field in render.go, so a scope
// below the root did not carry it at all. Either way a test handing in a
// Virtual clock got a real reading, which is a snapshot that changes every run.
//
// Asserted against Epoch rather than against "some fixed value", because that
// is the whole point of the Virtual clock: two runs of one suite agree.
func TestEveryScopeReadsTheSessionClock(t *testing.T) {
	want := Epoch.UTC().Format(snglDateTime)

	env, err := BuildEnv(check(t, clockSrc), "main", NewVirtual())
	if err != nil {
		t.Fatalf("BuildEnv: %v", err)
	}

	got, ok := lookupVarValue(env, "at")
	if !ok {
		t.Fatal("main declares no `at`")
	}
	if got != want {
		t.Errorf("the root scope read %v, want %v -- BuildEnv ran the initializer before the clock was in place", got, want)
	}

	if _, err := Mount(env); err != nil {
		t.Fatalf("Mount: %v", err)
	}
	for _, child := range env.childEnvs {
		got, ok := lookupVarValue(child, "at")
		if !ok {
			continue
		}
		if got != want {
			t.Errorf("a child component's scope read %v, want %v -- the clock did not reach it", got, want)
		}
	}
}

func lookupVarValue(env *Env, name string) (any, bool) {
	if env.Comp == nil {
		return nil, false
	}
	for _, v := range env.Comp.Vars {
		if v.Name == name {
			return env.Value(v)
		}
	}
	return nil, false
}
