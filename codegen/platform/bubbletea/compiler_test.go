package bubbletea

import (
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/internal/checker"
	"git.duckfam.us/jonathan/sngl/internal/testutil"
)

func compileAndVerify(t *testing.T, doc *ast.Document) []byte {
	t.Helper()
	src, err := Compile(doc, Config{})
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	// Verify the generated code parses as valid Go
	fset := token.NewFileSet()
	_, err = parser.ParseFile(fset, "generated.go", src, parser.AllErrors)
	if err != nil {
		t.Fatalf("generated code is not valid Go:\n%s\nerror: %v", src, err)
	}
	return src
}

func TestFixtures(t *testing.T) {
	testutil.RunFixtures(t, "../../../testdata", func(t *testing.T, path string, dirs []testutil.ErrorDirective) {
		if len(dirs) > 0 {
			return // skip all error fixtures
		}
		doc, err := testutil.ParseFile(path)
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		if err := checker.Check(doc, os.DirFS("../../../testdata"), "", checker.DefaultResolver(), nil, nil, true); err != nil {
			t.Fatalf("check: %v", err)
		}
		compileAndVerify(t, doc)
	})
}

func TestGettersSetters(t *testing.T) {
	doc, err := testutil.ParseFile("../../../testdata/data_extern_trigger.sngl")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if err := checker.Check(doc, os.DirFS("../../../testdata"), "", nil, nil, nil, true); err != nil {
		t.Fatalf("check: %v", err)
	}
	src := compileAndVerify(t, doc)
	code := string(src)

	checks := map[string]string{
		"getter":       "func (m Model) Todos() []Todo",
		"setter":       "func (m Model) SetTodos(v []Todo) Model",
		"trigger reg":  "func (m Model) OnTodosChanged(fn func([]Todo)) Model",
		"msg type":     "type setTodosMsg struct",
		"cmd func":     "func SetTodosCmd(",
		"update case":  "case setTodosMsg:",
		"count getter": "func (m Model) Count() int",
		"count setter": "func (m Model) SetCount(v int) Model",
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
	if err := checker.Check(doc, os.DirFS("../../../examples/todo"), "", nil, nil, nil, true); err != nil {
		t.Fatalf("check: %v", err)
	}
	compileAndVerify(t, doc)
}
