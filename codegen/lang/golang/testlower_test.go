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

func TestLowerTestFile_agentModeEmitsRegisterInit(t *testing.T) {
	src := `
component box {
    var count = 0
    text(value="x")
}

func testFoo(t Test, c box) {
    t.assert(c.count == 0)
}
`
	doc, err := parser.Parse("t.sngl", []byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	pkg, _ := checker.Check(doc, &checker.Config{IsMain: true})
	var fn *ir.Func
	for _, f := range pkg.Funcs {
		if f.IsTest {
			fn = f
			break
		}
	}
	if fn == nil {
		t.Fatal("no test func in package")
	}
	out := LowerTestFile("ui", []*ir.Func{fn}, []string{"Foo"}, nil, TestEmitAgent)
	if !strings.Contains(out, "import \"git.duckfam.us/jonathan/sngl/pkg/go/testagent\"") {
		t.Errorf("agent mode missing testagent import; got:\n%s", out)
	}
	if !strings.Contains(out, "func testFoo(t *testagent.T)") {
		t.Errorf("agent func signature missing; got:\n%s", out)
	}
	if !strings.Contains(out, "testagent.RegisterTest(\"Foo\", testFoo)") {
		t.Errorf("agent mode missing RegisterTest init; got:\n%s", out)
	}
}

func TestLowerTestFile_nativeModeEmitsTestingImport(t *testing.T) {
	src := `
component box {
    var count = 0
    text(value="x")
}

func testFoo(t Test, c box) {
    t.assert(c.count == 0)
}
`
	doc, _ := parser.Parse("t.sngl", []byte(src))
	pkg, _ := checker.Check(doc, &checker.Config{IsMain: true})
	var fn *ir.Func
	for _, f := range pkg.Funcs {
		if f.IsTest {
			fn = f
			break
		}
	}
	out := LowerTestFile("ui", []*ir.Func{fn}, []string{"Foo"}, nil, TestEmitNative)
	if !strings.Contains(out, "import \"testing\"") {
		t.Errorf("native mode missing testing import; got:\n%s", out)
	}
	if !strings.Contains(out, "func TestFoo(t *testing.T)") {
		t.Errorf("native func signature missing; got:\n%s", out)
	}
	if strings.Contains(out, "RegisterTest") {
		t.Errorf("native mode should not RegisterTest; got:\n%s", out)
	}
}
