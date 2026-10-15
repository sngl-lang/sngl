//go:build !js

package html

import (
	"net/http"
	"testing"
	"time"

	"duckfam.us/sngl/codegen/platform/html/internal/webtest"
)

const stableWait = 200 * time.Millisecond

// startComponent compiles src via renderComponentHTML, serves the html at /,
// starts a headless browser, navigates to /, and returns the *webtest.Browser.
// The caller is responsible for defer browser.Close(). Skips the test if no
// browser is available.
func startComponent(t *testing.T, src string) *webtest.Browser {
	t.Helper()

	html := renderComponentHTML(t, src)
	htmlBytes := []byte(html)

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(htmlBytes)
	})

	engine := webtest.New(mux)
	t.Cleanup(engine.Close)

	if testing.Short() {
		t.Skip("skipping browser test in -short mode")
	}
	browser, err := engine.StartHeadless(1280, 720)
	if err != nil {
		t.Skipf("browser unavailable: %v", err)
	}

	if err := browser.NavigateRaw(engine.BaseURL() + "/"); err != nil {
		browser.Close()
		t.Fatalf("navigate: %v", err)
	}

	_ = browser.WaitStable(stableWait)

	return browser
}
