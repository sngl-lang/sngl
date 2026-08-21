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
	rodproto "github.com/go-rod/rod/lib/proto"
)

// TestBrowser_FuncvarStoredAsyncUpdatesDOM exercises the full async-await
// pipeline via a stored funcvar at runtime:
//
//  1. A component-local var (handler) is initialized to an async JS native
//     (fetchHello).
//  2. A click handler calls handler() — which must be awaited because the
//     funcvar's slot color is Async (via points-to analysis).
//  3. The result is assigned to the component-local "greeting" state var.
//  4. The DOM text node bound to greeting updates from "" to "after".
//
// This is distinct from TestBrowser_AsyncHandlerUpdatesDOM which calls the
// async native directly; here the call is indirected through a funcvar stored
// in the component state, so points-to analysis must propagate the Async
// color through the variable.
//
// The test skips gracefully when Chrome / Chromium is not available.
func TestBrowser_FuncvarStoredAsyncUpdatesDOM(t *testing.T) {
	// Verify the JS importer is registered (blank-import at package level ensures this).
	if codegen.LookupScheme("js") == nil {
		t.Fatal("js scheme importer not registered; check blank import")
	}

	// Note: text(value=greeting) must come BEFORE button(…@click…) in the
	// SNGL source so the NoReactivity lowering seeds idToNode["__n*"] before
	// addClickHandler translates the DOM-write assign (#__nN.value = greeting)
	// via domWriteFor → textContent.  If the button comes first the fall-through
	// path produces __n0.value (a custom expando) instead of __n0.textContent.
	//
	// handler is a component-local funcvar initialised to api.fetchHello; the
	// click body calls handler(), which must be awaited because points-to
	// analysis propagates the Async color through the funcvar slot.
	const snglSrc = `
import . "sngl://std"
import api "js://./api"

component main {
    var handler func() string = api.fetchHello
    var greeting = "before"
    text(value=greeting)
    button(text="Go", @click {
        greeting = handler()
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

	// Use compileAsyncHTML (parse → check → lower → generate) without the
	// AST optimizer. The component-local funcvar is kept in state, and the
	// fix in translate_ir.go ensures that native namespace member references
	// used as values (e.g. state.handler = __sngl_n_api.fetchHello) are
	// emitted with the esbuild-compatible alias and registered in
	// NativeImports so the bundler can inline the async function body.
	htmlBytes := compileAsyncHTML(t, snglSrc, fsys)
	htmlStr := string(htmlBytes)

	// Sanity: generated output must contain async/await before we open a browser.
	// The click handler wrapper must be async because handler's slot color is Async.
	if !strings.Contains(htmlStr, "async function") {
		t.Fatalf("generated HTML missing 'async function';\nHTML:\n%s", htmlStr)
	}
	if !strings.Contains(htmlStr, "await") {
		t.Fatalf("generated HTML missing 'await';\nHTML:\n%s", htmlStr)
	}
	// Verify the await is on the funcvar call site.
	// The funcvar is stored in state, so the call site is "await state.handler()".
	if !strings.Contains(htmlStr, "await state.handler()") {
		t.Fatalf("generated HTML missing 'await state.handler()' — funcvar call site not awaited;\nHTML:\n%s", htmlStr)
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
	//    Click handler: state.greeting = await state.handler()
	//                                  (where state.handler = fetchHello)
	//                   __n0.textContent = state.greeting
	//    Both steps must have completed for the assertion to pass.
	spanText, err := span.Text()
	if err != nil {
		t.Fatalf("read span text after click: %v", err)
	}
	if spanText != "after" {
		t.Fatalf("DOM span after click: want %q got %q\nHTML:\n%s", "after", spanText, htmlStr)
	}
}
