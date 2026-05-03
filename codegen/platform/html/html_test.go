package html

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/internal/checker"
	"git.duckfam.us/jonathan/sngl/internal/lower"
	"git.duckfam.us/jonathan/sngl/internal/parser"
	"git.duckfam.us/jonathan/sngl/internal/testutil"

	_ "git.duckfam.us/jonathan/sngl/codegen/lang/javascript"
	_ "git.duckfam.us/jonathan/sngl/codegen/lang/none"
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

	lang := codegen.LookupLang("none")
	if lang == nil {
		t.Fatal("none language translator not registered")
	}

	gen := &Generator{}
	caps := gen.Capabilities().Merge(lang.Capabilities())
	if err := lower.Lower(pkg, caps, lower.Options{}); err != nil {
		t.Fatalf("lower: %v", err)
	}
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

	lang := codegen.LookupLang("none")
	if lang == nil {
		t.Fatal("none language translator not registered")
	}

	gen := &Generator{}
	caps := gen.Capabilities().Merge(lang.Capabilities())
	if err := lower.Lower(pkg, caps, lower.Options{}); err != nil {
		t.Fatalf("lower: %v", err)
	}
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
		"document.",
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
		"document.",
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

func TestLoweredReactivityWiring(t *testing.T) {
	const src = `component main {
    var n int = 0
    text(value=string(n))
    button(text="+", @click { n = n + 1 })
}`
	doc, err := parser.Parse("counter.sngl", []byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	pkg, diags := checker.Check(doc, &checker.Config{IsMain: true})
	if len(diags) > 0 {
		t.Fatalf("check: %v", diags[0])
	}

	lang := codegen.LookupLang("none")
	gen := &Generator{}
	caps := gen.Capabilities().Merge(lang.Capabilities())
	if err := lower.Lower(pkg, caps, lower.Options{}); err != nil {
		t.Fatalf("lower: %v", err)
	}

	resp, err := gen.Generate(&codegen.Request{
		Doc:  doc,
		Pkg:  pkg,
		Lang: lang,
	})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	var buf bytes.Buffer
	resp.Files[0].WriteTo(&buf)
	out := buf.String()

	// Static render must carry the data-sngl-id attribute for the
	// reactive text node so JS lang's IsElementRef path resolves it.
	if !strings.Contains(out, `data-sngl-id="__n0"`) {
		t.Errorf("missing data-sngl-id=\"__n0\" attr; output:\n%s", out)
	}
	// The click handler body must contain the prop-remapped DOM write
	// for the text node (textContent, not value).
	if !strings.Contains(out, `.textContent = String(state.n)`) &&
		!strings.Contains(out, `.textContent = String((state.n))`) {
		t.Errorf("missing .textContent = String(state.n) DOM write; output:\n%s", out)
	}
	// addTextUpdater must NOT have fired for __n0 — no $u_*_text
	// updater function should target it. (Match the legacy naming
	// pattern $u_<idsuffix>_text and rule it out.)
	if strings.Contains(out, "function $u___n0_text(") {
		t.Errorf("legacy $u___n0_text updater registered despite NoReactivity; output:\n%s", out)
	}
	// The __n* id should be cached as a top-level const, not
	// re-resolved per update.
	if !strings.Contains(out, `const __n0 = document.querySelector('[data-sngl-id="__n0"]')`) {
		t.Errorf("missing cached __n0 const; output:\n%s", out)
	}
	// The handler body should reference __n0 as a bare identifier,
	// not via inline document.querySelector.
	if strings.Contains(out, `document.querySelector('[data-sngl-id="__n0"]').textContent`) {
		t.Errorf("handler body still inlines querySelector instead of using cached const; output:\n%s", out)
	}
}
