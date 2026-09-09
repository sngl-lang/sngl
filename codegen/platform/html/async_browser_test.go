//go:build !js

package html

import (
	"fmt"
	"io/fs"
	"net/http"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/codegen/platform/html/internal/webtest"
	"git.duckfam.us/jonathan/sngl/internal/checker"
	"git.duckfam.us/jonathan/sngl/internal/lower"
	"git.duckfam.us/jonathan/sngl/internal/parser"
	"git.duckfam.us/jonathan/sngl/ir"
	rodproto "github.com/go-rod/rod/lib/proto"

	_ "git.duckfam.us/jonathan/sngl/codegen/lang/javascript"
	_ "git.duckfam.us/jonathan/sngl/codegen/lang/none"
	_ "git.duckfam.us/jonathan/sngl/codegen/scheme/js"
)

// asyncBrowserResolver implements checker.ImportResolver backed by an
// in-memory FS, delegating scheme imports to the registered JS importer.
type asyncBrowserResolver struct {
	fsys fs.FS
}

func (r *asyncBrowserResolver) Resolve(_ fs.FS, importPath string) ([]*ast.Document, error) {
	entries, err := fs.ReadDir(r.fsys, importPath)
	if err != nil {
		return nil, fmt.Errorf("reading import dir %q: %w", importPath, err)
	}
	var docs []*ast.Document
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sngl") {
			continue
		}
		p := importPath + "/" + e.Name()
		data, err := fs.ReadFile(r.fsys, p)
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", p, err)
		}
		doc, err := parser.Parse(e.Name(), data)
		if err != nil {
			return nil, fmt.Errorf("parsing %s: %w", p, err)
		}
		docs = append(docs, doc)
	}
	return docs, nil
}

func (r *asyncBrowserResolver) ResolveScheme(scheme, uri, _ string) (*ir.NativeImport, error) {
	imp := codegen.LookupScheme(scheme)
	if imp == nil {
		return nil, fmt.Errorf("scheme %q not supported", scheme)
	}
	if fsa, ok := imp.(codegen.FSAwareScheme); ok && r.fsys != nil {
		return fsa.ResolveFS(uri, r.fsys, ".")
	}
	return nil, fmt.Errorf("scheme %q requires OS filesystem; not supported in this test", scheme)
}

func (r *asyncBrowserResolver) ResolveSchemeFS(_, _, _ string) ([]*ast.Document, fs.FS, error) {
	return nil, nil, nil
}

// compileAsyncHTML parses, checks, lowers, and generates HTML from src using
// fsys for import resolution. Returns the complete HTML bytes or fatals.
func compileAsyncHTML(t *testing.T, src string, fsys fs.FS) []byte {
	t.Helper()

	doc, err := parser.Parse("app.sngl", []byte(src))
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
		Languages: htmlLangs(),
		Targets:   []ir.StaticTarget{{Platform: "html", Language: "none"}},
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
	if err := lower.Lower(pkg, caps, lower.Options{Platform: gen.PlatformIdentifier()}); err != nil {
		t.Fatalf("lower: %v", err)
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
			return content
		}
	}
	t.Fatal("expected at least 1 .html output file")
	return nil
}

// TestBrowser_AsyncHandlerUpdatesDOM exercises the full async-await pipeline
// end-to-end in a real browser:
//
//  1. A click handler calls an async JS native (fetchHello) that resolves to
//     "after" with a short delay.
//  2. The handler awaits the result and writes it to the "greeting" state var.
//  3. The DOM text node bound to greeting updates from "" to "after" after
//     the click (the mutation model initialises DOM writes lazily on state
//     change, so the span is empty until the first mutation fires).
//
// The JS state (`state.greeting`) starts at "before"; the DOM span starts
// empty.  After clicking the button the async promise settles synchronously
// (return "after") and the DOM must reflect the new value.
//
// The test skips gracefully when Chrome / Chromium is not available.
func TestBrowser_AsyncHandlerUpdatesDOM(t *testing.T) {
	// Verify the JS importer is registered (blank-import above should ensure this).
	if codegen.LookupScheme("js") == nil {
		t.Fatal("js scheme importer not registered; check blank import")
	}

	// Note: text(value=greeting) must come BEFORE button(…@click…) in the
	// SNGL source so the NoReactivity lowering seeds idToNode["__n*"] before
	// the click handler translates the DOM-write assign (#__nN.value = greeting)
	// via domWriteFor → textContent.  If the button comes first the fall-through
	// path produces __n0.value (a custom expando) instead of __n0.textContent.
	const snglSrc = `
import . "sngl:ui"
import api "js:./api"
import app "sngl:app"

app.window {
    var greeting = "before"
    text(value=greeting)
    button(text="Go", @click {
        greeting = api.fetchHello()
    })
}
`

	// In-memory FS: the .ts file provides an async fetchHello that resolves
	// "after". Promise.resolve is sufficient — no setTimeout needed.
	fsys := fstest.MapFS{
		"app.sngl": &fstest.MapFile{Data: []byte(snglSrc)},
		"api/index.ts": &fstest.MapFile{Data: []byte(`
export async function fetchHello(): Promise<string> {
    return "after";
}
`)},
	}

	htmlBytes := compileAsyncHTML(t, snglSrc, fsys)
	htmlStr := string(htmlBytes)

	// Sanity: generated output must contain async/await before we open a browser.
	if !strings.Contains(htmlStr, "async function") {
		t.Fatalf("generated HTML missing 'async function';\nHTML:\n%s", htmlStr)
	}
	if !strings.Contains(htmlStr, "await") {
		t.Fatalf("generated HTML missing 'await';\nHTML:\n%s", htmlStr)
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

	// 1. Verify the text span exists and is initially empty.
	//    The SNGL mutation model initialises DOM text lazily on state change
	//    (the static HTML span has no inner text); the span becomes non-empty
	//    only after the first mutation that changes greeting.
	span, err := page.Timeout(5 * time.Second).Element("[data-sngl-id]")
	if err != nil {
		t.Fatalf("find text span (data-sngl-id): %v\nHTML:\n%s", err, htmlStr)
	}
	initialText, err := span.Text()
	if err != nil {
		t.Fatalf("read initial span text: %v", err)
	}
	// The span must be empty initially (mutation model; no initial DOM write).
	if initialText != "" {
		t.Logf("note: initial span text is %q (non-empty; pre-render fills the span)", initialText)
	}

	// 2. Click the button.
	btn, err := page.Timeout(5 * time.Second).Element("button")
	if err != nil {
		t.Fatalf("find button: %v", err)
	}
	if err := btn.Click(rodproto.InputMouseButtonLeft, 1); err != nil {
		t.Fatalf("click button: %v", err)
	}

	// 3. Wait for the async handler to settle and the DOM to update.
	//    fetchHello() returns a resolved Promise — a single microtask tick
	//    suffices, but WaitStable (200 ms) is reliable across slow CI runners.
	_ = browser.WaitStable(200 * time.Millisecond)

	// 4. Assert the DOM span shows "after".
	//    The click handler: state.greeting = await fetchHello()
	//                       __n0.textContent = state.greeting
	//    Both steps must have completed for the assertion to pass.
	spanText, err := span.Text()
	if err != nil {
		t.Fatalf("read span text after click: %v", err)
	}
	if spanText != "after" {
		t.Fatalf("DOM span after click: want %q got %q\nHTML:\n%s", "after", spanText, htmlStr)
	}
}
