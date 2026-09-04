package interp

import (
	"fmt"
	"strings"
	"testing"
)

// manyBracketsSrc places n brackets in one scope, all mounting once.
//
// A loop over a counted sequence rather than 600 written-out effects: the
// number is the whole point, and the shape a program reaches for is the loop.
func manyBracketsSrc(n int) string {
	return fmt.Sprintf(`import . "sngl:ui"
import "sngl:seq"

component main {
    var mounted = 0

    for var i = seq.count(%d) {
        effect(on=i, @mount { mounted += 1 })
    }

    text #out(value="{mounted}")
}
`, n)
}

// TestManyBracketsSettle: how many brackets a program holds is not a bound.
//
// The settle loop used to count handlers run, and one settle legitimately runs
// a handler per bracket it has to move -- so a scope holding more brackets than
// the bound was told "an effect is rekeying itself", which is a false
// accusation about a program doing nothing of the kind. Eight brackets passed
// and 512 failed, with nothing between them but the number.
//
// The compiled targets said nothing at all here, which is the other half: the
// interpreter refused a program every one of them generated fine.
func TestManyBracketsSettle(t *testing.T) {
	for _, n := range []int{8, 600} {
		s := sessionFor(t, manyBracketsSrc(n), "main")
		out := s.View().Find("out")
		if len(out) != 1 {
			t.Fatalf("n=%d: #out resolved to %d nodes", n, len(out))
		}
		if got, want := out[0].Props["value"], fmt.Sprint(n); got != want {
			t.Errorf("n=%d: %d brackets mounted, want %v", n, got, want)
		}
	}
}

// selfRekeySrc is the program the bound actually exists for: one bracket whose
// mount handler changes the key it is mounted on, so the settle describes a
// different tree every pass.
const selfRekeySrc = `import . "sngl:ui"

component main {
    var n = 0

    effect(on=n, @mount { n += 1 })

    text #out(value="{n}")
}
`

// TestASelfRekeyingBracketIsStillReported: the bound moved from "handlers run"
// to "lifetimes one bracket began", and the case it was written for still
// trips it -- naming the bracket, which counting handlers could not.
func TestASelfRekeyingBracketIsStillReported(t *testing.T) {
	_, err := NewSession(check(t, selfRekeySrc), "main", NewVirtual())
	if err == nil {
		t.Fatal("a bracket rekeying itself settled")
	}
	if !strings.Contains(err.Error(), "rekeying itself") {
		t.Fatalf("error = %v, want it to say the effect is rekeying itself", err)
	}
	if !strings.Contains(err.Error(), "main:") {
		t.Errorf("error = %v, want it to name the bracket's key", err)
	}
}
