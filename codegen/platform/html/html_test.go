package html

import (
	"git.duckfam.us/jonathan/sngl/ir"
	"os"
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/internal/checker"
	"git.duckfam.us/jonathan/sngl/internal/lower"
	"git.duckfam.us/jonathan/sngl/internal/optimize"
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
	pkg, diags := checker.Check(doc, &checker.Config{
		FS: s.FS, Dir: s.Dir, IsMain: true,
		Platforms: []ir.Platform{&Generator{}},
		Targets:   []ir.StaticTarget{{Platform: "html", Language: "none"}},
	})
	if len(diags) > 0 {
		t.Fatalf("check: %v", diags[0])
	}

	lang := codegen.LookupLang("none")
	if lang == nil {
		t.Fatal("none language translator not registered")
	}

	gen := &Generator{}
	caps := gen.Capabilities(lang).ToLowerCaps()
	if err := optimize.Optimize(pkg, &optimize.Config{
		Platform: gen.PlatformIdentifier(),
		Language: lang.LanguageIdentifier(),
	}); err != nil {
		t.Fatalf("optimize: %v", err)
	}
	if err := lower.Lower(pkg, caps, lower.Options{Platform: gen.PlatformIdentifier()}); err != nil {
		t.Fatalf("lower: %v", err)
	}
	mem := codegen.NewMemSink()
	if err := gen.Generate(&codegen.Request{
		Doc:  doc,
		Pkg:  pkg,
		Lang: lang,
	}, mem); err != nil {
		t.Fatalf("generate: %v", err)
	}
	for name, content := range mem.Files() {
		if strings.HasSuffix(name, ".html") {
			return string(content)
		}
	}
	t.Fatal("expected at least 1 .html file")
	return ""
}

func generateHTML(t *testing.T, path string) string {
	t.Helper()
	doc, err := testutil.ParseFile(path)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	dir := path[:strings.LastIndex(path, "/")]
	pkg, diags := checker.Check(doc, &checker.Config{
		FS: os.DirFS(dir), Dir: dir, IsMain: true,
		Platforms: []ir.Platform{&Generator{}},
		Targets:   []ir.StaticTarget{{Platform: "html", Language: "none"}},
	})
	if len(diags) > 0 {
		t.Fatalf("check: %v", diags[0])
	}

	lang := codegen.LookupLang("none")
	if lang == nil {
		t.Fatal("none language translator not registered")
	}

	gen := &Generator{}
	caps := gen.Capabilities(lang).ToLowerCaps()
	if err := optimize.Optimize(pkg, &optimize.Config{
		Platform: gen.PlatformIdentifier(),
		Language: lang.LanguageIdentifier(),
	}); err != nil {
		t.Fatalf("optimize: %v", err)
	}
	if err := lower.Lower(pkg, caps, lower.Options{Platform: gen.PlatformIdentifier()}); err != nil {
		t.Fatalf("lower: %v", err)
	}
	mem := codegen.NewMemSink()
	if err := gen.Generate(&codegen.Request{
		Doc:  doc,
		Pkg:  pkg,
		Lang: lang,
	}, mem); err != nil {
		t.Fatalf("generate: %v", err)
	}
	for name, content := range mem.Files() {
		if strings.HasSuffix(name, ".html") {
			return string(content)
		}
	}
	t.Fatal("expected at least 1 .html file")
	return ""
}

func TestFixtures(t *testing.T) {
	// Fixtures the optimizer can't handle: mutually-recursive user funcs
	// trip inlineCall's lack of cycle detection (separate optimize bug,
	// out of scope for codegen tests).
	skipOptimize := map[string]bool{
		"checker_mutual_recursion": true,
	}
	for s := range testutil.TestdataSamples(t) {
		if len(s.Errors) > 0 {
			continue
		}
		if skipOptimize[s.Name] {
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
			if !strings.Contains(html, "state = {") {
				t.Error("missing state initialization")
			}
		})
	}
}

func TestTodoApp(t *testing.T) {
	html := generateHTML(t, "../../../examples/todo/todo.sngl")

	checks := []string{
		"<!DOCTYPE html>",
		"state = {",
		"state.todos",
		"state.newTodo",
		`function main_status(`,
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
		"state = {",
		"state.count",
		`function main_greeting(`,
		`function main_isAdult(`,
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
		"state = {",
		"state.count",
		"state.name",
		`function main_greeting(`,
		`function main_doubled(`,
	}
	for _, check := range checks {
		if !strings.Contains(html, check) {
			t.Errorf("missing expected content: %q\n\ngenerated:\n%s", check, html)
		}
	}
}

func TestLoweredReactivityWiring(t *testing.T) {
	const src = `import . "sngl:ui"
component main {
    var n int = 0
    text(value=string(n))
    button(text="+", @click { n = n + 1 })
}`
	doc, err := parser.Parse("counter.sngl", []byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	// Configured with the platform it then generates for. Without it, html's
	// package never loads, no `component sngl.text { ... }`
	// override is merged, and `text` reaches codegen abstract -- which no real
	// build produces and which kept a name-matching fallback alive.
	pkg, diags := checker.Check(doc, &checker.Config{
		IsMain:    true,
		Platforms: []ir.Platform{&Generator{}},
		Targets:   []ir.StaticTarget{{Platform: "html", Language: "none"}},
	})
	if len(diags) > 0 {
		t.Fatalf("check: %v", diags[0])
	}

	lang := codegen.LookupLang("none")
	gen := &Generator{}
	caps := gen.Capabilities(lang).ToLowerCaps()
	if err := optimize.Optimize(pkg, &optimize.Config{
		Platform: gen.PlatformIdentifier(),
		Language: lang.LanguageIdentifier(),
	}); err != nil {
		t.Fatalf("optimize: %v", err)
	}
	if err := lower.Lower(pkg, caps, lower.Options{Platform: gen.PlatformIdentifier()}); err != nil {
		t.Fatalf("lower: %v", err)
	}

	mem := codegen.NewMemSink()
	if err := gen.Generate(&codegen.Request{
		Doc:  doc,
		Pkg:  pkg,
		Lang: lang,
	}, mem); err != nil {
		t.Fatalf("generate: %v", err)
	}
	var out string
	for name, content := range mem.Files() {
		if strings.HasSuffix(name, ".html") {
			out = string(content)
			break
		}
	}
	if out == "" {
		t.Fatal("expected at least 1 .html file")
	}

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
	if !strings.Contains(out, `__n0 = document.querySelector('[data-sngl-id="__n0"]')`) {
		t.Errorf("missing cached __n0 binding; output:\n%s", out)
	}
	// The handler body should reference __n0 as a bare identifier,
	// not via inline document.querySelector.
	if strings.Contains(out, `document.querySelector('[data-sngl-id="__n0"]').textContent`) {
		t.Errorf("handler body still inlines querySelector instead of using cached const; output:\n%s", out)
	}
}
