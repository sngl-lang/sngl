package bubbletea

import (
	"regexp"
	"strings"
	"testing"
)

// TestTheEntryIntoARecursiveCycleStatesItsDepth compiles the Go emitted for
// testdata/test_recursion_bound.sngl.
//
// passRecursionDepth gives each cycle member a `__depth` prop and rewrites the
// instantiations *inside* the cycle to pass the next value. The entry into the
// cycle got nothing: the site written outside it, and the residual
// instantiation the optimizer leaves when it stops unrolling, both left the
// prop unstated. A prop a site does not state is not a name the caller's scope
// declares, so bubbletea emitted nine references to a bare `__depth` inside a
// zero-parameter render func while the recursive callee it hands off to does
// take one -- the counter never reached the entry render, and the bound did not
// apply to the entry tree at all.
func TestTheEntryIntoARecursiveCycleStatesItsDepth(t *testing.T) {
	model := generateBubbleteaModel(t, fixtureSource(t, "test_recursion_bound.sngl"))

	// The recursive callee still counts.
	for _, want := range []string{
		"func (m Model) renderEndless(more bool, n int, __depth int) string {",
		"if __depth >= 512 {",
	} {
		if !strings.Contains(model, want) {
			t.Errorf("emitted Go missing %q\n--- model.go ---\n%s", want, model)
		}
	}
	// The entry hands it a number. Seeded as a literal 0, so the guards the
	// unroll spliced into the entry render fold away and what survives is the
	// count reached.
	if !regexp.MustCompile(`m\.renderEndless\(true, \d+, \d+\)`).MatchString(model) {
		t.Errorf("the entry into the cycle passes no depth\n--- model.go ---\n%s", model)
	}
	// Reading the prop where nothing declares it is the defect.
	for _, fn := range []string{"renderBoundReachesTheBoundary", "renderTerminatingRecursion"} {
		if body := methodBody(model, "func (m Model) "+fn+"()"); body != "" && strings.Contains(body, "__depth") {
			t.Errorf("%s takes no __depth yet reads one\n--- %s ---\n%s", fn, fn, body)
		}
	}
	buildGeneratedGo(t, "bt-recursion-seed-", model)
}

// methodBody is the text of one emitted method, from its signature to the
// closing brace in column zero.
func methodBody(src, sig string) string {
	i := strings.Index(src, sig)
	if i < 0 {
		return ""
	}
	rest := src[i:]
	if before, _, ok := strings.Cut(rest, "\n}\n"); ok {
		return before
	}
	return rest
}
