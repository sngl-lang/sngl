package bubbletea

import (
	goparser "go/parser"
	"go/token"
	"os"
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/internal/checker"
	"git.duckfam.us/jonathan/sngl/internal/lower"
	"git.duckfam.us/jonathan/sngl/internal/parser"
	"git.duckfam.us/jonathan/sngl/internal/testutil"
	"git.duckfam.us/jonathan/sngl/ir"
)

func hasErrors(diags []ir.Diagnostic) bool {
	for _, d := range diags {
		if d.Severity == ir.Error {
			return true
		}
	}
	return false
}

func firstError(diags []ir.Diagnostic) string {
	for _, d := range diags {
		if d.Severity == ir.Error {
			return d.Error()
		}
	}
	return ""
}

func compileAndVerify(t *testing.T, doc *ast.Document, pkg ...*ir.Package) []byte {
	t.Helper()
	var p *ir.Package
	if len(pkg) > 0 {
		p = pkg[0]
	}
	if p != nil {
		gen := &Generator{}
		if err := lower.Lower(p, gen.Capabilities(), lower.Options{Platform: "bubbletea"}); err != nil {
			t.Fatalf("lower: %v", err)
		}
	}
	ctx := codegen.NewCodegenCtx(&codegen.Request{Doc: doc, Pkg: p}, "bubbletea")
	src, err := CompileIR(ctx, Config{})
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	// Verify the generated code parses as valid Go
	fset := token.NewFileSet()
	_, err = goparser.ParseFile(fset, "generated.go", src, goparser.AllErrors)
	if err != nil {
		t.Fatalf("generated code is not valid Go:\n%s\nerror: %v", src, err)
	}
	return src
}

func TestFixtures(t *testing.T) {
	for s := range testutil.TestdataSamples(t) {
		if len(s.Errors) > 0 {
			continue
		}
		t.Run(s.Name, func(t *testing.T) {
			doc, err := parser.Parse(s.Filename, []byte(s.Source))
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			pkg, diags := checker.Check(doc, &checker.Config{FS: s.FS, Dir: s.Dir, IsMain: true})
			if hasErrors(diags) {
				t.Fatalf("check: %s", firstError(diags))
			}
			compileAndVerify(t, doc, pkg)
		})
	}
}

func TestGettersSetters(t *testing.T) {
	src := `struct Todo {
    text string
    done bool
}

component main {
    var (
        count = 0
        todos list<Todo>
    )
}
`
	doc, err := parser.Parse("test.sngl", []byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	pkg, diags := checker.Check(doc, &checker.Config{IsMain: true})
	if hasErrors(diags) {
		t.Fatalf("check: %s", firstError(diags))
	}
	out := compileAndVerify(t, doc, pkg)
	code := string(out)

	checks := map[string]string{
		"getter":       "func (m Model) Todos() []Todo",
		"setter":       "func (m *Model) SetTodos(v []Todo)",
		"msg type":     "type setTodosMsg struct",
		"cmd func":     "func SetTodosCmd(",
		"update case":  "case setTodosMsg:",
		"count getter": "func (m Model) Count() int",
		"count setter": "func (m *Model) SetCount(v int)",
	}
	for name, check := range checks {
		if !strings.Contains(code, check) {
			t.Errorf("missing %s: %q\n\ngenerated:\n%s", name, check, code)
		}
	}
}

func TestCompileTodo(t *testing.T) {
	doc, err := testutil.ParseFile("../../../examples/todo/todo.sngl")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	pkg, diags := checker.Check(doc, &checker.Config{FS: os.DirFS("../../../examples/todo"), IsMain: true})
	if hasErrors(diags) {
		t.Fatalf("check: %s", firstError(diags))
	}
	compileAndVerify(t, doc, pkg)
}
