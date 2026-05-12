package golang

import (
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/internal/checker"
	"git.duckfam.us/jonathan/sngl/internal/parser"
	"git.duckfam.us/jonathan/sngl/ir"
)

func TestLowerTestFunc_trivialAssert(t *testing.T) {
	src := `
component box {
    var count = 0
    text(value="x")
}

func testCountStartsZero(t Test, c box) {
    t.assert(c.count == 0)
}
`
	doc, err := parser.Parse("t.sngl", []byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	pkg, diags := checker.Check(doc, &checker.Config{IsMain: true})
	for _, d := range diags {
		if d.Severity == ir.Error {
			t.Fatalf("check: %s", d.Msg)
		}
	}
	if pkg == nil {
		t.Fatal("nil pkg")
	}
	var fn *ir.Func
	for _, f := range pkg.Funcs {
		if f.Name == "testCountStartsZero" {
			fn = f
			break
		}
	}
	if fn == nil {
		t.Fatal("test func not found in pkg.Funcs")
	}

	out := LowerTestFunc(fn, "counterTest", nil)
	if !strings.Contains(out, "func TestcounterTest(t *testing.T)") {
		t.Errorf("missing Go test func header in:\n%s", out)
	}
	if !strings.Contains(out, "t.Errorf") {
		t.Errorf("assert should lower to t.Errorf:\n%s", out)
	}
	if !strings.Contains(out, "c.count == 0") {
		t.Errorf("expected raw-field rendering of c.count:\n%s", out)
	}
}
