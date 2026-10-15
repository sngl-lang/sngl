package javascript

import (
	"strings"
	"testing"

	"duckfam.us/sngl/internal/checker"
	"duckfam.us/sngl/internal/parser"
	"duckfam.us/sngl/ir"
)

func TestJSLowerTestFile_agentModeEmitsRegisterAll(t *testing.T) {
	src := `
component box node {
    var count = 0
    text(value="x")
}

func testFoo(t Test, c box) {
    t.assert(c.count == 0)
}
`
	doc, err := parser.Parse("t.sngl", []byte(src))
	if err != nil {
		t.Fatal(err)
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
	out := LowerTestFile("ui", nil, []*ir.Func{fn}, []string{"Foo"}, nil, TestEmitAgent, "")
	if !strings.Contains(out, "import { Registry } from './testagent/testagent.js'") {
		t.Errorf("agent mode missing Registry import:\n%s", out)
	}
	if !strings.Contains(out, "export async function testFoo(t)") {
		t.Errorf("agent func signature missing:\n%s", out)
	}
	if !strings.Contains(out, "Registry.register('Foo', testFoo)") {
		t.Errorf("Registry.register call missing:\n%s", out)
	}
}

func TestJSLowerTestFile_nativeModeIsNoOp(t *testing.T) {
	out := LowerTestFile("ui", nil, nil, nil, nil, TestEmitNative, "")
	if !strings.Contains(out, "not supported") {
		t.Errorf("native mode should emit a no-op comment:\n%s", out)
	}
}
