//go:build !js

package html

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/codegen/platform/html/internal/webtest"
	"git.duckfam.us/jonathan/sngl/internal/checker"
	"git.duckfam.us/jonathan/sngl/internal/lower"
	"git.duckfam.us/jonathan/sngl/internal/optimize"
	"git.duckfam.us/jonathan/sngl/internal/parser"
	"git.duckfam.us/jonathan/sngl/ir"
	rodproto "github.com/go-rod/rod/lib/proto"

	_ "git.duckfam.us/jonathan/sngl/codegen/lang/javascript"
	_ "git.duckfam.us/jonathan/sngl/codegen/lang/none"
)

// compileI18nHTML parses, checks, lowers, and generates HTML from src, then
// returns the complete HTML bytes. Fatals on any compilation error.
func compileI18nHTML(t *testing.T, src string) []byte {
	t.Helper()

	doc, err := parser.Parse("app.sngl", []byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	pkg, diags := checker.Check(doc, &checker.Config{
		IsMain:    true,
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
	// stdlib components (text, button) render through their `platform html { }`
	// bodies — the real path `sngl generate` takes.
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
		Doc:  doc,
		Pkg:  pkg,
		Lang: lang,
	}, mem); err != nil {
		t.Fatalf("generate: %v", err)
	}
	for name, content := range mem.Files() {
		if strings.HasSuffix(name, ".html") {
			return content
		}
	}
	t.Fatal("expected at least 1 .html output file")
	return nil
}

// TestBrowser_I18nPluralRendering exercises the JS i18n runtime end-to-end in
// a real browser:
//
//  1. A component renders $"You have {count, plural, one{# item} other{# items}}"
//     where count starts at 1. The i18n runtime (inlined at the top of the
//     <script> block) formats this as "You have 1 item".
//  2. Clicking "Add" increments count.
//  3. The DOM updates to "You have 2 items".
//
// The test skips gracefully when Chrome / Chromium is not available.
func TestBrowser_I18nPluralRendering(t *testing.T) {
	// Note: label is initialized to a literal string to avoid a state-initializer
	// self-reference (SNGL HTML codegen emits `let state = {count:1, label: f(state.count)}`
	// which would be a TDZ error). The click handler re-computes label via $"..."
	// so the i18n runtime is exercised on every increment.
	const snglSrc = `
import . "sngl://std"
component main {
    var count int = 1
    var label string = "You have 1 item"
    text(value=label)
    button(text="Add", @click {
        count = count + 1
        label = $"You have {count, plural, one{# item} other{# items}}"
    })
}
`
	htmlBytes := compileI18nHTML(t, snglSrc)
	htmlStr := string(htmlBytes)

	// Sanity: generated HTML must contain the i18n runtime preamble.
	if !strings.Contains(htmlStr, "getTranslator") {
		t.Fatalf("generated HTML missing i18n runtime (getTranslator);\nHTML:\n%s", htmlStr)
	}

	// Serve the HTML.
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(htmlBytes)
	})
	engine := webtest.New(mux)
	defer engine.Close()

	if testing.Short() {
		t.Skip("skipping browser test in -short mode")
	}
	browser, err := engine.StartHeadless(1280, 720)
	if err != nil {
		// Chrome not available in this environment — skip rather than fail.
		t.Skipf("browser not available: %v", err)
	}
	defer browser.Close()

	if err := browser.NavigateRaw(engine.BaseURL() + "/"); err != nil {
		t.Fatalf("navigate: %v", err)
	}

	page := browser.Page()

	// 1. Find the text span and verify the initial translated plural form.
	//    The SNGL mutation model initialises DOM text lazily on state change,
	//    so the span may be populated by an initial sync. Wait briefly.
	_ = browser.WaitStable(200 * time.Millisecond)

	span, err := page.Timeout(5 * time.Second).Element("[data-sngl-id]")
	if err != nil {
		t.Fatalf("find text span (data-sngl-id): %v\nHTML:\n%s", err, htmlStr)
	}
	initialText, err := span.Text()
	if err != nil {
		t.Fatalf("read initial span text: %v", err)
	}
	if initialText != "You have 1 item" {
		t.Fatalf("initial text: want %q got %q\nHTML:\n%s", "You have 1 item", initialText, htmlStr)
	}

	// 2. Click the "Add" button to increment count to 2.
	btn, err := page.Timeout(5 * time.Second).Element("button")
	if err != nil {
		t.Fatalf("find button: %v\nHTML:\n%s", err, htmlStr)
	}
	if err := btn.Click(rodproto.InputMouseButtonLeft, 1); err != nil {
		t.Fatalf("click button: %v", err)
	}

	// 3. Wait for the DOM update.
	_ = browser.WaitStable(200 * time.Millisecond)

	// 4. Assert the span now shows the plural form.
	spanText, err := span.Text()
	if err != nil {
		t.Fatalf("read span text after click: %v", err)
	}
	if spanText != "You have 2 items" {
		t.Fatalf("text after click: want %q got %q\nHTML:\n%s", "You have 2 items", spanText, htmlStr)
	}
}
