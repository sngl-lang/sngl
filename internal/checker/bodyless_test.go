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

// A program cannot write #[intrinsic] at all, because it cannot import the
// package that declares it. That used to be only half true: the mark resolver
// read the import statement rather than the import, so the mark applied while
// the import beside it was rejected. Resolving marks through the scope closed
// that — a name a program may not import is not in its scope.
//
// The bodyless-intrinsic case the library relies on is exercised by the library
// itself, which is all signatures now; #[foreign(..., native)] is the form a
// program can write, in TestForeignNativeNeedsNoBody.
func TestCompilerTierMarkIsNotAvailableToAProgram(t *testing.T) {
	src := `import . "sngl:internal/marks"

#[intrinsic("string.length")]
func length(s string) int
`
	errs := checkSrc(t, src)
	if len(errs) == 0 {
		t.Fatal("a program used a compiler-tier mark")
	}
	var sawImport bool
	for _, e := range errs {
		if strings.Contains(e.Error(), "internal to the compiler") {
			sawImport = true
		}
	}
	if !sawImport {
		t.Errorf("the import was not the complaint: %s", errs[0].Error())
	}
}

// A written body is itself the assertion that it computes the same answer the
// native implementation would, so a backend without the id may emit it. There is
// no flag beside it: `usable` was a second record of one fact, and it sat one
// word away from promoting a fabricated `return 0` into a live wrong answer.
// Making the body optional is what let the flag go.
func TestIntrinsicMayCarryItsOwnImplementation(t *testing.T) {
	src := `import . "sngl:internal/marks"

#[intrinsic("string.length")]
func length(s string) int {
    return 0
}
`
	for _, e := range checkSrc(t, src) {
		if strings.Contains(e.Error(), "body") {
			t.Fatalf("a body on an intrinsic was reported: %s", e.Error())
		}
	}
}
