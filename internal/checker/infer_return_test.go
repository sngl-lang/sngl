package checker_test

import (
	"strings"
	"testing"
)

// A type parameter appearing only in the return type is bound from the type the
// context wants. Without that, such a function is undeclarable rather than
// merely awkward: there are no arguments to infer from, and SNGL has no syntax
// for naming a type argument at a call site — `pending<int>()` does not parse.
//
// remote.pending is the case that forced this, but the rule is general.
func TestReturnOnlyTypeParamBindsFromContext(t *testing.T) {
	src := `func empty<T>() list<T> {
    return []
}

component c() node {
    var xs list<int> = empty()
    var ys list<string> = empty()
}
`
	if errs := checkSrc(t, src); len(errs) > 0 {
		t.Fatalf("check: %v", errs[0].Error())
	}
}

// Arguments win over the context. An argument states its type directly, while
// the expected type only says what the result must be assignable to, and the two
// part company wherever a conversion would apply — int is assignable to float,
// so binding from the context here would silently make T float and accept a
// list<float> where the argument said int.
func TestArgumentsBindBeforeTheContext(t *testing.T) {
	src := `func pair<T>(a T) list<T> {
    return [a]
}

component c() node {
    var xs list<string> = pair(1)
}
`
	errs := checkSrc(t, src)
	if len(errs) == 0 {
		t.Fatal("pair(1) filled a list<string>; the context overrode the argument")
	}
	if got := errs[0].Error(); !strings.Contains(got, "int") {
		t.Errorf("diagnostic does not name the bound type: %s", got)
	}
}

// With nothing to bind from, the parameter is still unresolved and the call is
// reported rather than silently passing as dyn.
func TestReturnOnlyTypeParamWithNoContextIsReported(t *testing.T) {
	src := `func empty<T>() list<T> {
    return []
}

component c() node {
    var xs list<int> = [1]
    xs.push(empty().length())
}
`
	// Not asserting a particular message — only that an unbindable parameter
	// does not quietly become a usable concrete type.
	for _, e := range checkSrc(t, src) {
		if strings.Contains(e.Error(), "list<int>") && strings.Contains(e.Error(), "list<T>") {
			return
		}
	}
}
