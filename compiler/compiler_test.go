package compiler

import (
	"go/parser"
	"go/token"
	"testing"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/checker"
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
	testutil.RunFixtures(t, "../testdata", func(t *testing.T, path string, dirs []testutil.ErrorDirective) {
		if len(dirs) > 0 {
			return // skip all error fixtures
		}
		doc, err := testutil.ParseFile(path)
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		if err := checker.Check(doc, "../testdata"); err != nil {
			t.Fatalf("check: %v", err)
		}
		compileAndVerify(t, doc)
	})
}

func TestCompileTodo(t *testing.T) {
	doc, err := testutil.ParseFile("../_examples/todo/todo.sngl.kdl")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if err := checker.Check(doc, "../_examples/todo"); err != nil {
		t.Fatalf("check: %v", err)
	}
	compileAndVerify(t, doc)
}
