package html

import (
	"bytes"
	"os"
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
	if err := checker.Check(doc, os.DirFS(dir), "", checker.DefaultResolver(), nil, nil, true); err != nil {
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
	if len(resp.Files) < 1 {
		t.Fatal("expected at least 1 file")
	}
	var buf bytes.Buffer
	resp.Files[0].WriteTo(&buf)
	return buf.String()
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
	html := generateHTML(t, "../../../examples/todo/todo.sngl")

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

func TestJSStateAndUpdaters(t *testing.T) {
	html := generateHTML(t, "../../../examples/todo/todo.sngl")

	checks := []string{
		"state.todos",
		"state.newTodo",
		"addEventListener",
	}
	for _, check := range checks {
		if !strings.Contains(html, check) {
			t.Errorf("missing expected content: %q\n\ngenerated:\n%s", check, html)
		}
	}
	// Setters should NOT be emitted for fields without triggers/timers
	if strings.Contains(html, "$get_") {
		t.Error("unexpected getter function in output")
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
