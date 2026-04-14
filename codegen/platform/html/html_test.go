package html

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/internal/checker"
	"git.duckfam.us/jonathan/sngl/internal/parser"
	"git.duckfam.us/jonathan/sngl/internal/testutil"

	_ "git.duckfam.us/jonathan/sngl/codegen/lang/javascript"
)

func generateHTMLFromSample(t *testing.T, s testutil.Sample) string {
	t.Helper()
	doc, err := parser.Parse(s.Filename, []byte(s.Source))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	pkg, diags := checker.Check(doc, &checker.Config{FS: s.FS, Dir: s.Dir, IsMain: true})
	if len(diags) > 0 {
		t.Fatalf("check: %v", diags[0])
	}

	lang := codegen.LookupLang("js")
	if lang == nil {
		t.Fatal("js language translator not registered")
	}

	gen := &Generator{}
	resp, err := gen.Generate(&codegen.Request{
		Doc:  doc,
		Pkg:  pkg,
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

func generateHTML(t *testing.T, path string) string {
	t.Helper()
	doc, err := testutil.ParseFile(path)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	dir := path[:strings.LastIndex(path, "/")]
	pkg, diags := checker.Check(doc, &checker.Config{FS: os.DirFS(dir), Dir: dir, IsMain: true})
	if len(diags) > 0 {
		t.Fatalf("check: %v", diags[0])
	}

	lang := codegen.LookupLang("js")
	if lang == nil {
		t.Fatal("js language translator not registered")
	}

	gen := &Generator{}
	resp, err := gen.Generate(&codegen.Request{
		Doc:  doc,
		Pkg:  pkg,
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
	for s := range testutil.TestdataSamples(t) {
		if len(s.Errors) > 0 {
			continue
		}
		t.Run(s.Name, func(t *testing.T) {
			html := generateHTMLFromSample(t, s)

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
