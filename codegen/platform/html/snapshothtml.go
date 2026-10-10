//go:build !js

package html

import (
	"net/http"
	"time"

	"duckfam.us/sngl/codegen/platform/html/internal/webtest"
)

// SnapshotHTML renders a standalone HTML string to a PNG at the given viewport
// size using a headless Chromium instance (via go-rod). It serves the HTML over
// a local test server and screenshots it. This satisfies the bubbletea
// platform's private htmlSnapshotter interface so TUI snapshots can be
// rasterized through the html platform's browser machinery.
func (g *Generator) SnapshotHTML(html []byte, width, height int) ([]byte, error) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(html)
	})

	engine := webtest.New(mux)
	defer engine.Close()

	browser, err := engine.StartHeadless(width, height)
	if err != nil {
		return nil, err
	}
	defer browser.Close()

	if err := browser.NavigateRaw(engine.BaseURL() + "/"); err != nil {
		return nil, err
	}

	if err := browser.WaitStable(100 * time.Millisecond); err != nil {
		return nil, err
	}

	return browser.ScreenshotRaw()
}
