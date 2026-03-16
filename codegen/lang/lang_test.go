package lang_test

import (
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/internal/checker"
	"git.duckfam.us/jonathan/sngl/codegen"
	_ "git.duckfam.us/jonathan/sngl/codegen/lang/golang"
	_ "git.duckfam.us/jonathan/sngl/codegen/lang/javascript"
	"github.com/google/cel-go/cel"
)

type exprGroup struct {
	name        string
	scope       *checker.Scope
	modelFields map[string]bool
	vars        map[string]any // for CEL runtime eval
	exprs       []string
}

func TestExprTranslation(t *testing.T) {
	groups := []exprGroup{
		{
			name:        "pure",
			scope:       checker.NewScope(nil),
			modelFields: map[string]bool{},
			vars:        map[string]any{},
			exprs: []string{
				"1 + 2",
				"10 - 3",
				"4 * 5",
				"10 / 3",
				"10 % 3",
				"'hello' + ' ' + 'world'",
				"size('hello')",
				"1 < 2",
				"3 >= 3",
				"1 == 1",
				"1 != 2",
				"true && false",
				"true || false",
				"!true",
				"true ? 'yes' : 'no'",
				"string(42)",
				"(1 + 2) * 3",
			},
		},
		{
			name: "model",
			scope: func() *checker.Scope {
				s := checker.NewScope(nil)
				s.Declare("count", cel.IntType)
				s.Declare("name", cel.StringType)
				s.Declare("active", cel.BoolType)
				s.Declare("items", cel.ListType(cel.DynType))
				return s
			}(),
			modelFields: map[string]bool{"count": true, "name": true, "active": true, "items": true},
			vars:        map[string]any{"count": int64(5), "name": "World", "active": true, "items": []any{"a", "b", "c"}},
			exprs: []string{
				"count + 1",
				"name + '!'",
				"active ? 'on' : 'off'",
				"size(items)",
			},
		},
	}

	// Parse, check, and evaluate all expressions via CEL runtime.
	var allExprs []checkedExpr

	for i := range groups {
		g := &groups[i]
		env, err := checker.BuildCelEnv(g.scope, nil, nil)
		if err != nil {
			t.Fatalf("BuildCelEnv for %s: %v", g.name, err)
		}
		for _, expr := range g.exprs {
			celAst, iss := env.Parse(expr)
			if iss != nil && iss.Err() != nil {
				t.Fatalf("parse %q: %v", expr, iss.Err())
			}
			celAst, iss = env.Check(celAst)
			if iss != nil && iss.Err() != nil {
				t.Fatalf("check %q: %v", expr, iss.Err())
			}

			// Add LANGUAGE/PLATFORM constants required by env.
			evalVars := make(map[string]any, len(g.vars)+2)
			maps.Copy(evalVars, g.vars)
			evalVars["LANGUAGE"] = ""
			evalVars["PLATFORM"] = ""

			prg, err := env.Program(celAst)
			if err != nil {
				t.Fatalf("program %q: %v", expr, err)
			}
			val, _, err := prg.Eval(evalVars)
			if err != nil {
				t.Fatalf("eval %q: %v", expr, err)
			}
			truth := formatCelValue(val.Value())

			// Convert CEL AST to SNGL Node
			snglNode := ast.CELToSNGL(celAst.NativeRep().Expr())
			allExprs = append(allExprs, checkedExpr{cel: expr, truth: truth, sngl: snglNode, group: g})
		}
	}

	for _, langName := range codegen.Langs() {
		t.Run(langName, func(t *testing.T) {
			translator := codegen.LookupLang(langName)

			switch langName {
			case "js":
				runJSTest(t, translator, allExprs)
			case "go":
				runGoTest(t, translator, allExprs)
			default:
				t.Skipf("no runner for language %q", langName)
			}
		})
	}
}

func formatCelValue(v any) string {
	switch v := v.(type) {
	case string:
		return v
	default:
		return fmt.Sprintf("%v", v)
	}
}

type checkedExpr struct {
	cel   string
	truth string
	sngl  ast.Node
	group *exprGroup
}

func buildExprScope(group *exprGroup) *codegen.ExprScope {
	return &codegen.ExprScope{
		ModelFields:    group.modelFields,
		ComputedFields: map[string]bool{},
		LocalVars:      map[string]bool{},
	}
}

func runGoTest(t *testing.T, translator codegen.LangTranslator, exprs []checkedExpr) {
	t.Helper()
	dir := t.TempDir()

	// Write go.mod
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module test\n\ngo 1.26.1\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var lines []string
	lines = append(lines, `package main`)
	lines = append(lines, ``)
	lines = append(lines, `import "fmt"`)
	lines = append(lines, ``)
	lines = append(lines, `func ternary[T any](cond bool, a, b T) T {`)
	lines = append(lines, `	if cond { return a }`)
	lines = append(lines, `	return b`)
	lines = append(lines, `}`)
	lines = append(lines, ``)
	lines = append(lines, `type Model struct {`)
	lines = append(lines, `	Count  int`)
	lines = append(lines, `	Name   string`)
	lines = append(lines, `	Active bool`)
	lines = append(lines, `	Items  []any`)
	lines = append(lines, `}`)
	lines = append(lines, ``)
	lines = append(lines, `func main() {`)
	lines = append(lines, `	m := Model{Count: 5, Name: "World", Active: true, Items: []any{"a", "b", "c"}}`)
	lines = append(lines, `	_ = m`)

	for _, e := range exprs {
		scope := buildExprScope(e.group)
		translated := translator.TranslateExpr(e.sngl, scope)
		lines = append(lines, fmt.Sprintf("\tfmt.Println(%s)", translated))
	}

	lines = append(lines, `}`)

	src := strings.Join(lines, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := exec.Command("go", "run", filepath.Join(dir, "main.go")).CombinedOutput()
	if err != nil {
		t.Fatalf("go run failed:\n%s\n%v", out, err)
	}

	compareOutput(t, exprs, string(out))
}

func runJSTest(t *testing.T, translator codegen.LangTranslator, exprs []checkedExpr) {
	t.Helper()

	runtime := ""
	for _, name := range []string{"bun", "node"} {
		if _, err := exec.LookPath(name); err == nil {
			runtime = name
			break
		}
	}
	if runtime == "" {
		t.Skip("no JS runtime (bun or node) found")
	}

	dir := t.TempDir()

	var lines []string
	lines = append(lines, `const state = {count: 5, name: "World", active: true, items: ["a", "b", "c"]};`)

	for _, e := range exprs {
		scope := buildExprScope(e.group)
		translated := translator.TranslateExpr(e.sngl, scope)
		lines = append(lines, fmt.Sprintf("console.log(%s);", translated))
	}

	src := strings.Join(lines, "\n") + "\n"
	mainJS := filepath.Join(dir, "main.js")
	if err := os.WriteFile(mainJS, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := exec.Command(runtime, mainJS).CombinedOutput()
	if err != nil {
		t.Fatalf("%s run failed:\n%s\n%v", runtime, out, err)
	}

	compareOutput(t, exprs, string(out))
}

func compareOutput(t *testing.T, exprs []checkedExpr, output string) {
	t.Helper()
	outputLines := strings.Split(strings.TrimSpace(output), "\n")
	if len(outputLines) != len(exprs) {
		t.Fatalf("expected %d output lines, got %d:\n%s", len(exprs), len(outputLines), output)
	}
	for i, e := range exprs {
		got := strings.TrimSpace(outputLines[i])
		if got != e.truth {
			t.Errorf("expr %q: expected %q, got %q", e.cel, e.truth, got)
		}
	}
}
