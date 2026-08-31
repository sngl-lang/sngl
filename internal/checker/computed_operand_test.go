package checker_test

import (
	"strings"
	"testing"
)

// A computed named without parentheses in an operand position means its result.
// `total.length()`, `doubled[0]` and `pt.x` are the three such positions, and
// each used to resolve against the *function* type, find nothing, and fall
// through to a dyn result with no diagnostic — so a wrong member checked clean
// and, because the operand stayed an identifier bound to the func rather than a
// call, nothing downstream could see which vars the computed reads. A prop
// reading one never re-rendered.
//
// codegen/platform/html/testdata/parity/asynclambda.golden is the other half of
// this: it recorded two dead spans until the resolution was fixed.

const computedOperandPrelude = `import . "sngl:ui"

struct Point {
    x int
}
`

// The wrong member on a computed is a diagnostic, and it names the *result*
// type, not the function type — which is the evidence the call was inserted.
func TestComputedReceiverResolvesAgainstItsResult(t *testing.T) {
	src := computedOperandPrelude + `
component main() {
    var key = "foo"

    func plain() => key + "!"

    text(value = "{plain.bogusMethod()}")
}
`
	errs := checkSrc(t, src)
	if len(errs) == 0 {
		t.Fatal("a bogus method on a computed checked clean; resolution never reached the result type")
	}
	if got := errs[0].Error(); !strings.Contains(got, "bogusMethod") || !strings.Contains(got, "string") {
		t.Errorf("diagnostic does not name the member and the result type: %s", got)
	}
}

// All three operand positions resolve, so a real member on the computed's result
// checks. Before the fix these checked too — by falling through to dyn — so the
// test above is what gives this one its teeth.
func TestComputedOperandPositionsResolve(t *testing.T) {
	for name, body := range map[string]string{
		"method receiver": `
    var key = "foo"
    func plain() => key + "!"
    text(value = "{plain.length()}")`,
		"index operand": `
    var xs list<int> = [1, 2, 3]
    func doubled() => xs.map(func(x int) => x * 2)
    text(value = "{doubled[0]}")`,
		"field operand": `
    var key = 1
    func pt() => Point{x = key}
    text(value = "{pt.x}")`,
	} {
		t.Run(name, func(t *testing.T) {
			src := computedOperandPrelude + "\ncomponent main() {" + body + "\n}\n"
			if errs := checkSrc(t, src); len(errs) > 0 {
				t.Errorf("%v", errs[0].Error())
			}
		})
	}
}

// A function that takes arguments is not a computed and must not be called
// behind the author's back, so the insertion is guarded on arity.
//
// The guard is observable through the diagnostic: a zero-argument func is called
// and a bogus member is reported against its *result*, while a parameterized one
// is left alone and still falls through to the old silent-dyn path — which this
// test documents rather than fixes. Reporting there means deciding what a method
// call on a bare function reference should say at all, a separate question from
// the elision.
//
// The teeth: drop the arity guard and `lookup.bogusMethod()` starts reporting
// against string, which fails here.
func TestParameterizedFuncIsNotCalledImplicitly(t *testing.T) {
	src := computedOperandPrelude + `
func lookup(k string) string {
    return k
}

component main() {
    text(value = "{lookup.bogusMethod()}")
}
`
	for _, e := range checkSrc(t, src) {
		if strings.Contains(e.Error(), "on type string") {
			t.Fatalf("the parameterized func was called implicitly: %s", e.Error())
		}
	}
}
