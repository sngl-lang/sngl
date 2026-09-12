package names_test

import (
	"testing"

	"git.duckfam.us/jonathan/sngl/internal/names"
)

func TestReservedNameIsNeverHandedOut(t *testing.T) {
	r := names.New("id", "ok")
	if got := r.Unique("id"); got != "id2" {
		t.Errorf("Unique(id) = %q, want id2", got)
	}
	if got := r.Unique("ok"); got != "ok2" {
		t.Errorf("Unique(ok) = %q, want ok2", got)
	}
	if got := r.Unique("sess"); got != "sess" {
		t.Errorf("Unique(sess) = %q, want sess", got)
	}
}

func TestUniqueReservesWhatItReturns(t *testing.T) {
	r := names.New()
	for i, want := range []string{"s", "s2", "s3", "s4"} {
		if got := r.Unique("s"); got != want {
			t.Errorf("Unique #%d = %q, want %q", i, got, want)
		}
	}
}

// The suffix skips a spelling a *later* candidate would have wanted, rather
// than renumbering: s2 is reserved before anyone asks for it.
func TestSuffixSkipsAnAlreadyReservedSuffix(t *testing.T) {
	r := names.New("s", "s2")
	if got := r.Unique("s"); got != "s3" {
		t.Errorf("Unique(s) = %q, want s3", got)
	}
}

func TestFreeTracksBothReserveAndUnique(t *testing.T) {
	r := names.New("a")
	if r.Free("a") {
		t.Error("Free(a) = true after Reserve")
	}
	if !r.Free("b") {
		t.Error("Free(b) = false before anything claimed it")
	}
	r.Unique("b")
	if r.Free("b") {
		t.Error("Free(b) = true after Unique")
	}
}

// Two runs over one ordered seed and one ordered request list agree. The
// registry never iterates its own map to produce a name, so the only thing a
// caller has to keep deterministic is the order it calls in.
func TestDeterministicAcrossRuns(t *testing.T) {
	run := func() []string {
		r := names.New()
		r.Reserve("step__mark", "helper", "helper2")
		var out []string
		for _, want := range []string{"step__mark", "helper", "helper", "fresh"} {
			out = append(out, r.Unique(want))
		}
		return out
	}
	first := run()
	second := run()
	for i := range first {
		if first[i] != second[i] {
			t.Fatalf("run %d differs: %q vs %q", i, first[i], second[i])
		}
	}
	want := []string{"step__mark2", "helper3", "helper4", "fresh"}
	for i := range want {
		if first[i] != want[i] {
			t.Errorf("allocation %d = %q, want %q", i, first[i], want[i])
		}
	}
}

func TestZeroValueIsUsable(t *testing.T) {
	var r names.Registry
	r.Reserve("x")
	if got := r.Unique("x"); got != "x2" {
		t.Errorf("Unique(x) = %q, want x2", got)
	}
}
