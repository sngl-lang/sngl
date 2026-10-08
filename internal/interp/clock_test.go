package interp

import (
	"testing"

	"duckfam.us/sngl/ir"
)

func envFor(t *testing.T, src, comp string) (*Env, *ir.Package) {
	t.Helper()
	pkg := check(t, src)
	env, err := BuildEnv(pkg, comp)
	if err != nil {
		t.Fatalf("BuildEnv: %v", err)
	}
	return env, pkg
}

// The interpreter's timer tests live in internal/interp/timertest: a schedule
// comes from sngl:platform/none's override of `sngl:time`'s `timer`, so a
// program that names one has to be checked with that platform registered --
// and importing the registry from an internal test here closes a cycle through
// internal/interp/testrunner.

// TestVirtualClockStartsAtAFixedEpoch: a snapshot that interpolates a date must
// not depend on when the suite ran.
func TestVirtualClockStartsAtAFixedEpoch(t *testing.T) {
	if got := NewVirtual().Now(); !got.Equal(Epoch) {
		t.Errorf("a fresh Virtual reads %v, want %v", got, Epoch)
	}
}

// TestAnAdvanceableClockMovesOnlyWhenAsked.
func TestAnAdvanceableClockMovesOnlyWhenAsked(t *testing.T) {
	c := NewVirtual()
	before := c.Now()
	if got := c.Now(); !got.Equal(before) {
		t.Errorf("reading the clock moved it to %v", got)
	}
	c.Advance(250)
	if got := c.Now().Sub(before); got != 250 {
		t.Errorf("clock advanced %v, want 250ns", got)
	}
}
