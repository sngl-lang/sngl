package checker_test

import (
	"strings"
	"testing"
)

// A function body is optional, and the checker requires the answer to come from
// somewhere rather than assuming it does.
//
// The rule replaces the shape it is easy to reach for instead: suppressing the
// missing-return diagnostic whenever an #[intrinsic] mark is present. That reads
// as "mark present, skip a rule", and it leaves the declaration claiming a body
// nobody reads — the fabricated `{ return 0 }` that looks exactly like a real
// implementation and becomes one the moment somebody adds `usable`.

// A signature with nothing to supply its body is reported.
func TestBodylessFuncNeedsASource(t *testing.T) {
	errs := checkSrc(t, "func f() int\n")
	if len(errs) == 0 {
		t.Fatal("a bodyless func with no intrinsic, foreign mark or override checked clean")
	}
	if got := errs[0].Error(); !strings.Contains(got, "no body") {
		t.Errorf("unexpected diagnostic: %s", got)
	}
}

// Void is not a loophole: the absence of a return type says nothing about where
// the body comes from.
func TestBodylessVoidFuncNeedsASource(t *testing.T) {
	if errs := checkSrc(t, "func f()\n"); len(errs) == 0 {
		t.Fatal("a bodyless void func checked clean")
	}
}

// An #[intrinsic] id is one of the three answers, and a signature carrying one
// needs no return even for a type with no literal to fabricate — which is the
// case that forced this: remote.pending could only have been written as an
// infinite self-recursion.
func TestBodylessIntrinsicIsAccepted(t *testing.T) {
	src := `import . "sngl:internal/marks"

#[intrinsic("string.length")]
func length(s string) int
`
	// Only asserting the body rule; the id belongs to another receiver here, so
	// ignore anything about the id itself.
	for _, e := range checkSrc(t, src) {
		if strings.Contains(e.Error(), "no body") || strings.Contains(e.Error(), "missing return") {
			t.Fatalf("a bodyless #[intrinsic] was reported: %s", e.Error())
		}
	}
}

// The converse rule, and the one that actually retires the hazard: a body on a
// non-`usable` intrinsic is a fiction, so writing one is an error rather than
// merely useless.
func TestIntrinsicWithoutUsableRejectsABody(t *testing.T) {
	src := `import . "sngl:internal/marks"

#[intrinsic("string.length")]
func length(s string) int {
    return 0
}
`
	errs := checkSrc(t, src)
	if len(errs) == 0 {
		t.Fatal("a non-usable intrinsic kept its body; the `usable` hazard is still reachable")
	}
	var found bool
	for _, e := range errs {
		if strings.Contains(e.Error(), "never read") {
			found = true
		}
	}
	if !found {
		t.Errorf("diagnostic does not explain that the body is unread: %s", errs[0].Error())
	}
}

// `usable` is the way to keep a body: it says the body computes the same answer
// the native implementation would, so a backend without the id may emit it.
func TestUsableIntrinsicKeepsItsBody(t *testing.T) {
	src := `import . "sngl:internal/marks"

#[intrinsic("string.length", usable)]
func length(s string) int {
    return 0
}
`
	for _, e := range checkSrc(t, src) {
		if strings.Contains(e.Error(), "never read") {
			t.Fatalf("a usable intrinsic was told to drop its body: %s", e.Error())
		}
	}
}
