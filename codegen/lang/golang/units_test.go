package golang

import (
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/ir"
)

// A duration literal's Go form is a product -- `time.Duration(1000) *
// time.Millisecond` -- so it has to carry its own parentheses to mean the same
// thing wherever it is substituted. Unbracketed, `d / 1s` came out as
// `d / time.Duration(1000) * time.Millisecond`, which Go groups as
// `(d / 1000) * time.Millisecond`: 5s/1s answered 5e12.
//
// Asserted here rather than in a golden because the claim is about the
// literal's emission and not about any one program: every division of one
// today goes through a `float64(...)` conversion that happens to bracket it,
// so no generated output shows the difference.
func TestADurationLiteralIsSelfContained(t *testing.T) {
	for _, suffix := range []string{"ms", "s", "m", "h"} {
		got := durationLiteralGo(&ir.Literal{Value: "1", Suffix: suffix}, 1)
		if !strings.HasPrefix(got, "(") || !strings.HasSuffix(got, ")") {
			t.Errorf("1%s emitted as %s, which regroups when it is an operand of * / or %%", suffix, got)
		}
	}
}
