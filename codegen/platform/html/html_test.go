package html

import (
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/internal/checker"
	"git.duckfam.us/jonathan/sngl/internal/testutil"

	_ "git.duckfam.us/jonathan/sngl/codegen/lang/javascript"
)

func generateHTML(t *testing.T, path string) string {
	t.Helper()
	doc, err := testutil.ParseFile(path)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	dir := path[:strings.LastIndex(path, "/")]
	if err := checker.Check(doc, dir, checker.DefaultResolver(), nil, true); err != nil {
		t.Fatalf("check: %v", err)
	}

	lang := codegen.LookupLang("js")
	if lang == nil {
		t.Fatal("js language translator not registered")
	}

	gen := &Generator{}
	resp, err := gen.Generate(&codegen.Request{
		Doc:  doc,
		Lang: lang,
	})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if resp.Error != "" {
		t.Fatalf("generate error: %s", resp.Error)
	}
	if len(resp.Files) != 1 {
		t.Fatalf("expected 1 file, got %d", len(resp.Files))
	}
	if resp.Files[0].Name != "index.html" {
		t.Fatalf("expected index.html, got %s", resp.Files[0].Name)
	}
	return string(resp.Files[0].Content)
}

func TestFixtures(t *testing.T) {
	testutil.RunFixtures(t, "../../../testdata", func(t *testing.T, path string, dirs []testutil.ErrorDirective) {
		if len(dirs) > 0 {
			return // skip error fixtures
		}
		html := generateHTML(t, path)

		// Basic structure checks
		if !strings.Contains(html, "<!DOCTYPE html>") {
			t.Error("missing <!DOCTYPE html>")
		}
		if !strings.Contains(html, "<script>") {
			t.Error("missing <script> tag")
		}
		if !strings.Contains(html, "let state = {") {
			t.Error("missing state initialization")
		}
	})
}

func TestTodoApp(t *testing.T) {
	html := generateHTML(t, "../../../_examples/todo/todo.sngl")

	checks := []string{
		"<!DOCTYPE html>",
		"let state = {",
		"state.todos",
		"state.newTodo",
		`function $status()`,
		`function Todo(`,
		"function String(v)",
		"document.getElementById",
		"addEventListener",
		"push(",
	}
	for _, check := range checks {
		if !strings.Contains(html, check) {
			t.Errorf("missing expected content: %q\n\ngenerated:\n%s", check, html)
		}
	}
}

func TestFullExample(t *testing.T) {
	html := generateHTML(t, "../../../testdata/full_example.sngl")

	checks := []string{
		"<!DOCTYPE html>",
		"let state = {",
		"state.count",
		`function $greeting()`,
		`function $isAdult()`,
		"document.getElementById",
		"addEventListener",
	}
	for _, check := range checks {
		if !strings.Contains(html, check) {
			t.Errorf("missing expected content: %q\n\ngenerated:\n%s", check, html)
		}
	}
}

func TestJSGettersSetters(t *testing.T) {
	html := generateHTML(t, "../../../_examples/todo/todo.sngl")

	checks := []string{
		"$set_todos",
		"$get_todos",
		"$set_newTodo",
		"$get_newTodo",
	}
	for _, check := range checks {
		if !strings.Contains(html, check) {
			t.Errorf("missing expected content: %q\n\ngenerated:\n%s", check, html)
		}
	}
}

func TestFullFixture(t *testing.T) {
	html := generateHTML(t, "../../../testdata/full.sngl")

	checks := []string{
		"<!DOCTYPE html>",
		"let state = {",
		"state.count",
		"state.name",
		`function $greeting()`,
		`function $doubled()`,
	}
	for _, check := range checks {
		if !strings.Contains(html, check) {
			t.Errorf("missing expected content: %q\n\ngenerated:\n%s", check, html)
		}
	}
}
