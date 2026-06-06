//go:build !js

package html

import (
	"net/http"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/codegen/platform/html/internal/webtest"
	"git.duckfam.us/jonathan/sngl/internal/checker"
	"git.duckfam.us/jonathan/sngl/internal/lower"
	"git.duckfam.us/jonathan/sngl/internal/optimize"
	"git.duckfam.us/jonathan/sngl/internal/parser"
	"git.duckfam.us/jonathan/sngl/ir"

	_ "git.duckfam.us/jonathan/sngl/codegen/lang/none"
)

// componentDOMCase is one stdlib component rendered in isolation, with
// substrings that MUST appear in the live rendered DOM (outerHTML of body).
type componentDOMCase struct {
	name string
	src  string
	want []string // substrings required in document.body.outerHTML
}

var componentDOMCases = []componentDOMCase{
	{"text", `component main { text(value="HELLO") }`, []string{"<span", "HELLO"}},
	{"vbox", `component main { vbox { text(value="A") text(value="B") } }`, []string{"flex-direction:column", ">A<", ">B<"}},
	// Caller `style` on a stdlib wrapper must merge onto the wrapper's root
	// element alongside its structural style (forwardStyle in lower), and a
	// `#hex` color literal must render as a CSS color (colorStructToCSS).
	{"vbox-style", `component main { vbox(style={gap=8, background=#ff0000}) { text(value="A") } }`,
		[]string{"flex-direction:column", "gap:8px", "background-color:#ff0000"}},
	{"text-color", `component main { text(value="A", style={color=#112233}) }`, []string{"color:#112233"}},
	{"hbox", `component main { hbox { text(value="A") } }`, []string{"flex-direction:row"}},
	{"button", `component main { button(text="CLICK") }`, []string{"<button", "CLICK"}},
	{"input", `component main { var n = "Bob" input(:value=n) }`, []string{"<input"}},
	{"checkbox", `component main { var c = true checkbox(label="ok", checked=c) }`, []string{`type="checkbox"`, "ok"}},
	{"image", `component main { image(src="/x.png", alt="pic") }`, []string{"<img", `src="/x.png"`, `alt="pic"`}},
	{"link", `component main { link(text="Home", href="/") }`, []string{"<a", `href="/"`, "Home"}},
	{"select", `component main { var f = "b" select(options=["a","b","c"], :value=f, placeholder="pick") }`,
		[]string{"<select", "<option", ">a<", ">b<", ">c<", "pick"}},
	{"radio", `component main { var r = "y" radio(options=["x","y"], :value=r) }`,
		[]string{"<fieldset", `type="radio"`, `value="x"`, `value="y"`}},
	{"textarea", `component main { var t = "hi" textarea(:value=t, rows=3) }`, []string{"<textarea", `rows="3"`}},
	{"tabs", `component main { var sel = 0 tabs(items=["One","Two"], selected=sel) { text(value="panel") } }`,
		[]string{`role="tablist"`, "One", "Two", "panel"}},
	{"table", `component main { table(columns=["A","B"], rows=[["1","2"],["3","4"]]) }`,
		[]string{"<table", "<thead", "<tbody", ">A<", ">B<", ">1<", ">4<"}},
	{"tree", `component main { tree(items=["root","child"]) }`, []string{"<ul", "<li", "root", "child"}},
	{"modal", `component main { var o = true modal(open=o, title="Dialog") { text(value="body") } }`,
		[]string{"Dialog", "body"}},
	{"divider", `component main { divider() }`, []string{"<hr"}},
	{"badge", `component main { badge(value="3") }`, []string{"3"}},
}

func TestComponentDOM(t *testing.T) {
	for _, tc := range componentDOMCases {
		t.Run(tc.name, func(t *testing.T) {
			html := renderComponentHTML(t, tc.src)
			outer := bodyOuterHTMLInBrowser(t, html) // t.Skipf if no browser
			for _, want := range tc.want {
				if !strings.Contains(outer, want) {
					t.Errorf("%s: rendered DOM missing %q\n--- body.outerHTML ---\n%s", tc.name, want, outer)
				}
			}
		})
	}
}

// renderComponentHTML prepends the output block to src, compiles to HTML bytes,
// and returns the HTML string. Fails the test on any compile/check/lower/generate error.
func renderComponentHTML(t *testing.T, src string) string {
	t.Helper()

	fullSrc := "output { none { html() } }\n" + src

	fsys := fstest.MapFS{}

	doc, err := parser.Parse("app.sngl", []byte(fullSrc))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	resolver := &asyncBrowserResolver{fsys: fsys}
	pkg, diags := checker.Check(doc, &checker.Config{
		FS:        fsys,
		Dir:       ".",
		IsMain:    true,
		Resolver:  resolver,
		Platforms: []ir.Platform{&Generator{}},
	})
	for _, d := range diags {
		if d.Severity == ir.Error {
			t.Fatalf("check: %s", d.Error())
		}
	}

	lang := codegen.LookupLang("none")
	if lang == nil {
		t.Fatal("none language translator not registered")
	}

	gen := &Generator{}
	caps := gen.Capabilities(lang).ToLowerCaps()
	// Mirror the CLI generate pipeline (cmd/sngl/pipeline.go): optimize →
	// lower → optimize. Wiring Platforms above + these passes is what makes
	// stdlib components render through their `platform html { }` bodies (the
	// real path), instead of the legacy renderStaticX helpers — so this test
	// reflects what `sngl generate` actually emits.
	optCfg := &optimize.Config{Platform: gen.PlatformIdentifier(), Language: lang.LanguageIdentifier(), Dir: "."}
	if err := optimize.Optimize(pkg, optCfg); err != nil {
		t.Fatalf("optimize: %v", err)
	}
	if err := lower.Lower(pkg, caps, lower.Options{Platform: gen.PlatformIdentifier()}); err != nil {
		t.Fatalf("lower: %v", err)
	}
	if err := optimize.Optimize(pkg, optCfg); err != nil {
		t.Fatalf("optimize (post-lower): %v", err)
	}

	mem := codegen.NewMemSink()
	if err := gen.Generate(&codegen.Request{
		Doc:       doc,
		Pkg:       pkg,
		Lang:      lang,
		ProjectFS: fsys,
	}, mem); err != nil {
		t.Fatalf("generate: %v", err)
	}

	for name, content := range mem.Files() {
		if strings.HasSuffix(name, ".html") {
			return string(content)
		}
	}
	t.Fatal("expected at least 1 .html output file")
	return ""
}

// bodyOuterHTMLInBrowser serves the html at /, navigates a headless browser,
// and returns document.body.outerHTML. Skips the test if no browser is available.
func bodyOuterHTMLInBrowser(t *testing.T, html string) string {
	t.Helper()

	htmlBytes := []byte(html)
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(htmlBytes)
	})

	engine := webtest.New(mux)
	defer engine.Close()

	browser, err := engine.StartHeadless(1280, 720)
	if err != nil {
		t.Skipf("browser unavailable: %v", err)
	}
	defer browser.Close()

	if err := browser.NavigateRaw(engine.BaseURL() + "/"); err != nil {
		t.Fatalf("navigate: %v", err)
	}

	_ = browser.WaitStable(200 * time.Millisecond)

	page := browser.Page()
	// Normalize whitespace so that ">A<" assertions work even when the
	// renderer emits "<span>\nA</span>" with indentation whitespace.
	// We trim leading/trailing whitespace from every text node between tags.
	result := page.MustElement("body").MustEval(`() => {
		return this.outerHTML.replace(/>[ \t\n\r]+/g, '>').replace(/[ \t\n\r]+</g, '<');
	}`).String()
	return result
}
