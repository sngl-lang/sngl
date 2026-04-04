package checker

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/internal/parser"
	"git.duckfam.us/jonathan/sngl/internal/testutil"
)

func TestFixtures(t *testing.T) {
	testutil.RunFixtures(t, "../../testdata", func(t *testing.T, path string, dirs []testutil.ErrorDirective) {
		parseErrs := testutil.Filter(dirs, "parse")
		if len(parseErrs) > 0 {
			return // skip files that test parser errors
		}
		checkErrs := testutil.Filter(dirs, "check")
		doc, err := testutil.ParseFile(path)
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		err = Check(doc, os.DirFS("../../testdata"), "", DefaultResolver(), nil, nil, true)
		// When check error directives exist, also merge CheckTests
		// diagnostics so ERROR(check) directives on test blocks match.
		if len(checkErrs) > 0 {
			diags := CheckTests(doc)
			if len(diags) > 0 {
				var msgs []string
				if err != nil {
					msgs = append(msgs, err.Error())
				}
				for _, d := range diags {
					if d.Pos.Line > 0 {
						msgs = append(msgs, fmt.Sprintf("%d:%d: %s", d.Pos.Line, d.Pos.Column, d.Msg))
					} else {
						msgs = append(msgs, d.Msg)
					}
				}
				err = fmt.Errorf("%s", strings.Join(msgs, "\n"))
			}
		}
		testutil.AssertErrors(t, err, checkErrs)
	})
}

func TestSchemeImport(t *testing.T) {
	mockResolver := func(scheme, uri, dir string) (*ast.NativeDecls, error) {
		if scheme != "test" {
			return nil, fmt.Errorf("unknown scheme %q", scheme)
		}
		return &ast.NativeDecls{
			Structs: []*ast.StructDef{
				{Name: "Todo", Fields: []*ast.StructField{
					{Name: "id", Type: "int"},
					{Name: "title", Type: "string"},
				}},
			},
			Data: []*ast.Data{
				{Name: "SaveTodo", Extern: true, IsFunc: true, ParamTypes: []string{"todo"}, ReturnType: "", Init: ast.Expr{TypeHint: "func:todo"}},
				{Name: "FetchAll", Extern: true, IsFunc: true, ParamTypes: nil, ReturnType: "list:todo", Init: ast.Expr{TypeHint: "func~list:todo"}},
				{Name: "Items", Extern: true, Init: ast.Expr{TypeHint: "list:todo"}},
			},
		}, nil
	}

	src := `import "test://myapi/api"

component main {
    var count = 0
    text(value=string(count))
    button(text="Save", @click={ count += 1 })
}
`
	doc, err := parser.Parse("test.sngl", strings.NewReader(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if err := Check(doc, nil, ".", nil, mockResolver, nil, true); err != nil {
		t.Fatalf("check: %v", err)
	}

	// Verify import was parsed correctly
	if len(doc.Imports) != 1 {
		t.Fatalf("expected 1 import, got %d", len(doc.Imports))
	}
	imp := doc.Imports[0]
	if imp.Scheme != "test" {
		t.Errorf("scheme = %q, want %q", imp.Scheme, "test")
	}
	if imp.Namespace != "api" {
		t.Errorf("namespace = %q, want %q", imp.Namespace, "api")
	}
}

func TestSchemeImportUnknownScheme(t *testing.T) {
	src := `import "unknown://foo/bar"

component main {
    text(value="hello")
}
`
	doc, err := parser.Parse("test.sngl", strings.NewReader(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	err = Check(doc, nil, "", nil, nil, nil, true)
	if err == nil {
		t.Fatal("expected error for unknown scheme, got nil")
	}
	if !strings.Contains(err.Error(), "scheme imports not supported") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestCallStmtInHandler(t *testing.T) {
	src := `component main {
    var saveTodo func(string) extern
    button(text="Save", @click={ saveTodo("test") })
}
`
	doc, err := parser.Parse("test.sngl", strings.NewReader(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if err := Check(doc, nil, "", nil, nil, nil, true); err != nil {
		t.Fatalf("expected no error for CallStmt in handler, got: %v", err)
	}
}

func TestCheckTests(t *testing.T) {
	testutil.RunFixtures(t, "../../testdata", func(t *testing.T, path string, dirs []testutil.ErrorDirective) {
		base := strings.TrimSuffix(path, ".sngl")
		isTestFile := strings.Contains(base, "test_")
		isCheckTestError := strings.HasSuffix(base, "error_unknown_test_component")
		if !isTestFile && !isCheckTestError {
			return
		}
		// Skip test files with runtime error directives — those intentionally
		// reference undefined vars which CheckTests may also flag.
		if isTestFile && len(testutil.Filter(dirs, "test")) > 0 {
			return
		}
		doc, err := testutil.ParseFile(path)
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		diags := CheckTests(doc)

		checkErrs := testutil.Filter(dirs, "check")
		if len(checkErrs) == 0 {
			if len(diags) > 0 {
				t.Errorf("expected no diagnostics, got %v", diags)
			}
			return
		}
		var msgs []string
		for _, d := range diags {
			if d.Pos.Line > 0 {
				msgs = append(msgs, fmt.Sprintf("%d:%d: %s", d.Pos.Line, d.Pos.Column, d.Msg))
			} else {
				msgs = append(msgs, d.Msg)
			}
		}
		var diagErr error
		if len(msgs) > 0 {
			diagErr = fmt.Errorf("%s", strings.Join(msgs, "\n"))
		}
		testutil.AssertErrors(t, diagErr, checkErrs)
	})
}
