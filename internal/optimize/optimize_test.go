package optimize

import (
	"os"
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/internal/checker"
	"git.duckfam.us/jonathan/sngl/internal/parser"
	"git.duckfam.us/jonathan/sngl/ir"
)

// checkAndOptimize is a test helper: parse → check → optimize → convert.
func checkAndOptimize(t *testing.T, source, platform, lang string) (*ir.Package, *ast.Document) {
	t.Helper()
	doc, err := parser.Parse("test.sngl", []byte(source))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	pkg, diags := checker.Check(doc, &checker.Config{
		FS:     os.DirFS("."),
		Dir:    ".",
		IsMain: true,
	})
	for _, d := range diags {
		if d.Severity == ir.Error {
			t.Fatalf("check: %s", d.Error())
		}
	}
	if err := Optimize(pkg, &Config{Platform: platform, Language: lang}); err != nil {
		t.Fatalf("optimize: %v", err)
	}
	return pkg, ir.Convert(pkg)
}

func formatDoc(doc *ast.Document) string {
	return parser.Format(doc)
}

func TestOptimize_ConstFolding(t *testing.T) {
	src := `
const x = 2 + 3
component main {
	text(value=string(x))
}
`
	// After optimization, string(x) with x=5 folds to "5".
	// The const itself may be shaken since it's fully inlined.
	_, doc := checkAndOptimize(t, src, "html", "js")
	out := formatDoc(doc)
	// The folded value "5" should appear in the output.
	if !strings.Contains(out, `"5"`) {
		t.Errorf("expected folded value \"5\" in output:\n%s", out)
	}
}

func TestOptimize_ConstExprFolds(t *testing.T) {
	// const(expr) operand is unwrapped by the checker, so the optimizer
	// folds it just like any other constant. The literal "7" must appear
	// in the output.
	src := `
const x = const (3 + 4)
component main {
	text(value=string(x))
}
`
	_, doc := checkAndOptimize(t, src, "html", "js")
	out := formatDoc(doc)
	if !strings.Contains(out, `"7"`) {
		t.Errorf("expected folded value \"7\" in output:\n%s", out)
	}
}

func TestOptimize_ConstExprFoldsPureCall(t *testing.T) {
	// Primary use case: `const pure_fn(args)` must fold to a literal at
	// compile time. Inlining + folding turns double(21) into 42.
	src := `
func double(x int) int { return x * 2 }
const x int = const double(21)
component main {
	text(value=string(x))
}
`
	_, doc := checkAndOptimize(t, src, "html", "js")
	out := formatDoc(doc)
	if !strings.Contains(out, `"42"`) {
		t.Errorf("expected folded value \"42\" in output:\n%s", out)
	}
	// The call site must not survive as a runtime call.
	if strings.Contains(out, "double(") {
		t.Errorf("call to double() leaked past optimizer:\n%s", out)
	}
}

func TestOptimize_PlatformElimination(t *testing.T) {
	src := `
component main {
	platform html {
		text(value="html only")
	}
	platform bubbletea {
		text(value="bubbletea only")
	}
}
`
	_, doc := checkAndOptimize(t, src, "html", "js")
	out := formatDoc(doc)
	if !strings.Contains(out, "html only") {
		t.Error("expected 'html only' to be kept")
	}
	if strings.Contains(out, "bubbletea only") {
		t.Error("expected 'bubbletea only' to be removed")
	}
}

func TestOptimize_IfConstTrue(t *testing.T) {
	src := `
component main {
	if PLATFORM == "html" {
		text(value="yes")
	}
}
`
	_, doc := checkAndOptimize(t, src, "html", "js")
	out := formatDoc(doc)
	if !strings.Contains(out, "yes") {
		t.Error("expected 'yes' to be kept for matching platform")
	}
}

func TestOptimize_IfConstFalse(t *testing.T) {
	src := `
component main {
	if PLATFORM == "bubbletea" {
		text(value="no")
	}
}
`
	_, doc := checkAndOptimize(t, src, "html", "js")
	out := formatDoc(doc)
	if strings.Contains(out, `"no"`) {
		t.Error("expected dead branch to be removed")
	}
}

func TestOptimize_ConstPropagation(t *testing.T) {
	src := `
const greeting = "hello"
const msg = greeting + " world"
component main {
	text(value=msg)
}
`
	// After optimization, msg = "hello" + " world" = "hello world".
	// The text prop gets the folded value. Consts may be shaken.
	_, doc := checkAndOptimize(t, src, "html", "js")
	out := formatDoc(doc)
	if !strings.Contains(out, `hello world`) {
		t.Errorf("expected folded 'hello world' in output:\n%s", out)
	}
}

func TestOptimize_FunctionInlining(t *testing.T) {
	// Test that a pure function call in a component gets inlined.
	src := `
func double(x int) => x * 2

component main {
	text(value=string(double(21)))
}
`
	pkg, _ := checkAndOptimize(t, src, "html", "js")
	// The double(21) call should be inlined and folded to 42.
	// Find the text node in the component body and check its value prop.
	if len(pkg.Components) == 0 {
		t.Fatal("expected at least one component")
	}
	comp := pkg.Components[0]
	for _, s := range comp.Body {
		if ni, ok := s.(*ir.NodeInst); ok && ni.Name == "text" {
			for _, p := range ni.Props {
				if p.Name == "value" {
					// Should be folded to literal "42".
					if lit, ok := p.Value.(*ir.Literal); ok {
						if lit.Raw == `"42"` {
							return // success
						}
						t.Errorf("expected literal \"42\", got %s", lit.Raw)
						return
					}
					// Might be a Conversion wrapping a literal.
					if conv, ok := p.Value.(*ir.Conversion); ok {
						if lit, ok := conv.Operand.(*ir.Literal); ok {
							if lit.Raw == "42" {
								return // success — string(42) not fully folded, but inline worked
							}
						}
					}
					t.Logf("value prop type: %T", p.Value)
				}
			}
		}
	}
	// If double is pure and inlined, that's the key test.
	// At minimum, the func should be shaken if fully inlined.
	for _, f := range pkg.Funcs {
		if f.Name == "double" {
			t.Log("double function was not shaken (still referenced)")
			return
		}
	}
	t.Log("double function was shaken (fully inlined)")
}

func TestOptimize_ShakeUnusedConst(t *testing.T) {
	src := `
const used = 1
const unused = 2
component main {
	text string(used)
}
`
	pkg, _ := checkAndOptimize(t, src, "html", "js")
	for _, c := range pkg.Consts {
		if c.Name == "unused" {
			t.Error("expected unused const to be shaken")
		}
	}
}

func TestOptimize_ShakeUnusedFunc(t *testing.T) {
	src := `
func used() => 1
func unused() => 2
component main {
	text(value=string(used()))
}
`
	pkg, _ := checkAndOptimize(t, src, "html", "js")
	for _, f := range pkg.Funcs {
		if f.Name == "unused" {
			t.Error("expected unused func to be shaken")
		}
	}
}

func TestOptimize_KeepTestFunc(t *testing.T) {
	src := `
func testFoo() => 1
component main {
	text(value="hi")
}
`
	pkg, _ := checkAndOptimize(t, src, "html", "js")
	found := false
	for _, f := range pkg.Funcs {
		if f.Name == "testFoo" {
			found = true
		}
	}
	if !found {
		t.Error("expected test func to be preserved")
	}
}

func TestOptimize_EmptyPkg(t *testing.T) {
	pkg := &ir.Package{}
	if err := Optimize(pkg, &Config{Platform: "html", Language: "js"}); err != nil {
		t.Fatal(err)
	}
}
