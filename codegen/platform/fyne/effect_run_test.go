package fyne

import "testing"

// effectModelSrc puts an effect on the Model: main is the root, so its
// settle funcs are Model methods.
const effectModelSrc = `
import . "sngl:ui"

window {
    var (
        n = 0
        log list<string> = []
    )

    effect(
        on=n,
        @mount { log.push("mount") },
        @unmount { log.push("unmount") },
    )

    button #bump(text="+", @click { n += 1 })
    text #out(value=log.join(","))
}
`

// effectModelDriver drives the settle the way the program does: once from
// New/BuildUI, then again through the click handler that changes the key.
const effectModelDriver = `package ui

import (
	"testing"

	"fyne.io/fyne/v2/test"
)

func TestTheModelsEffectSettles(t *testing.T) {
	test.NewApp()
	m := New()
	m.BuildUI()

	if got := m.log; len(got) != 1 || got[0] != "mount" {
		t.Fatalf("after the first settle log = %v, want [mount]", got)
	}
	m.bump.OnTapped()
	want := []string{"mount", "unmount", "mount"}
	if got := m.log; len(got) != len(want) {
		t.Fatalf("after the key changed log = %v, want %v", got, want)
	}
	for i := range want {
		if m.log[i] != want[i] {
			t.Fatalf("after the key changed log = %v, want %v", m.log, want)
		}
	}
}
`

// effectInstanceSrc puts the same effect inside a component the build cannot
// inline: a row under a dynamic `for`, which is a record with its own state.
const effectInstanceSrc = `
import . "sngl:ui"

component row(name = "") node {
    var (
        n = 0
        log list<string> = []
    )

    effect(
        on=n,
        @mount { log.push("mount " + name) },
        @unmount { log.push("unmount " + name) },
    )

    button(text=name, @click { n += 1 })
}

window {
    var names list<string> = ["a", "b"]
    for var nm = names {
        row(name=nm)
    }
}
`

// effectInstanceDriver reads each row's own log: an effect in a record settles
// per instance, so tapping one row must not touch the other's.
const effectInstanceDriver = `package ui

import (
	"testing"

	"fyne.io/fyne/v2/test"
)

func TestEachRowsEffectSettlesOnItsOwn(t *testing.T) {
	test.NewApp()
	m := New()
	m.BuildUI()

	if got := len(m.__inst0_live); got != 2 {
		t.Fatalf("expected 2 live instances, got %d", got)
	}
	first, second := m.__inst0_live[0], m.__inst0_live[1]
	if got := first.log; len(got) != 1 || got[0] != "mount a" {
		t.Fatalf("first row log = %v, want [mount a]", got)
	}
	if got := second.log; len(got) != 1 || got[0] != "mount b" {
		t.Fatalf("second row log = %v, want [mount b]", got)
	}
	second.__n0.OnTapped()
	if got := len(first.log); got != 1 {
		t.Errorf("first row log grew to %v when the second row's key changed", first.log)
	}
	want := []string{"mount b", "unmount b", "mount b"}
	if got := second.log; len(got) != len(want) {
		t.Fatalf("second row log = %v, want %v", got, want)
	}
	for i := range want {
		if second.log[i] != want[i] {
			t.Fatalf("second row log = %v, want %v", second.log, want)
		}
	}
}
`

// TestTheModelsEffectRuns and TestARecordsEffectRuns are fyne's first effect
// harness. Until they existed the platform had none, and an effect compiled
// into a settle nobody could call: every synthesized func was given a
// `container *fyne.Container` parameter meant for a slot render, so the
// emitted `m.__effects0_settle()` did not even build.
func TestTheModelsEffectRuns(t *testing.T) {
	runEmitted(t, "fyne-effect-model-", generateForFyne(t, effectModelSrc), effectModelDriver)
}

func TestARecordsEffectRuns(t *testing.T) {
	runEmitted(t, "fyne-effect-inst-", generateForFyne(t, effectInstanceSrc), effectInstanceDriver)
}
