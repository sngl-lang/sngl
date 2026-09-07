package checker_test

import (
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/internal/checker"
	"git.duckfam.us/jonathan/sngl/internal/parser"
	"git.duckfam.us/jonathan/sngl/internal/testtargets"
	"git.duckfam.us/jonathan/sngl/ir"
)

// #[go.async] says a call blocks. The compiler records that as the same fact a
// promise-returning import records — a call that does not complete now — because
// the propagation rule is the same one, and the fixpoint that colours callers is
// already written against it.
func TestGoAsyncMarksTheDeclarationAsync(t *testing.T) {
	src := `import go "sngl:language/go"

#[go.native("time", "Sleep")]
#[go.async]
func nap()

func doWork() {
    nap()
}
`
	pkg := checkedPkgWithTargets(t, src)

	nap := findFn(t, pkg, "nap")
	if !nap.IsAsync {
		t.Error("nap.IsAsync = false; the mark says the call does not complete now")
	}
	// The native mark is still applied: two marks on one declaration are two
	// facts about it, not a choice between them.
	if nap.Foreign.Name != "Sleep" || nap.Foreign.Path != "time" {
		t.Errorf("nap.Foreign = %+v; want the native identifier alongside the async mark", nap.Foreign)
	}

	// And the existing fixpoint carries it outward with no new machinery.
	if work := findFn(t, pkg, "doWork"); !work.IsAsync {
		t.Error("doWork.IsAsync = false; a function that calls a blocking one blocks")
	}
}

// A struct has no call to describe, which is the same reason #[foreign]'s flags
// refuse one.
func TestGoAsyncRefusesADeclarationWithNoCall(t *testing.T) {
	src := `import go "sngl:language/go"

#[go.async]
struct Box {
    n int
}
`
	doc, err := parser.Parse("test.sngl", []byte(withStd(src)))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	langs, plats := testtargets.Targets()
	_, diags := checker.Check(doc, &checker.Config{IsMain: true, Languages: langs, Platforms: plats})
	var got []string
	for _, d := range diags {
		if d.Severity == ir.Error {
			got = append(got, d.Error())
		}
	}
	joined := strings.Join(got, "\n")
	if !strings.Contains(joined, "#[go.async]") {
		t.Errorf("diagnostics did not refuse the mark on a struct; got:\n%s", joined)
	}
}
