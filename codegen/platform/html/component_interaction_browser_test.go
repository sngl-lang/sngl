//go:build !js

package html

import (
	"net/http"
	"testing"
	"time"

	"git.duckfam.us/jonathan/sngl/codegen/platform/html/internal/webtest"
	"github.com/go-rod/rod"

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

// TestInteraction_InputTwoWay verifies that a two-way bound input:
//  1. seeds the <input> with the initial state value ("World"), and
//  2. after typing "Sam", the <span> text updates to "Hello, Sam!" and
//     the input value is "Sam".
func TestInteraction_InputTwoWay(t *testing.T) {
	const src = `
import . "sngl:ui"
window {
    var name = "World"
    text(value="Hello, {name}!")
    input(:value=name)
}
`
	browser := startComponent(t, src)
	defer browser.Close()

	page := browser.Page()

	// 1. Check initial input value.
	input := page.MustElement("input")
	initialVal := input.MustEval("() => this.value").String()
	if initialVal != "World" {
		t.Errorf("initial input value: want %q got %q", "World", initialVal)
	}

	// 2. Type "Sam" into the input (select-all first to replace existing text).
	input.MustSelectAllText()
	input.MustInput("Sam")
	_ = browser.WaitStable(stableWait)

	// 3. Assert the span text updated.
	spanText := page.MustElement("span").MustText()
	if spanText != "Hello, Sam!" {
		t.Errorf("span text after typing: want %q got %q", "Hello, Sam!", spanText)
	}

	// 4. Assert the input value is "Sam".
	finalVal := input.MustEval("() => this.value").String()
	if finalVal != "Sam" {
		t.Errorf("input value after typing: want %q got %q", "Sam", finalVal)
	}
}

// TestInteraction_SelectChange verifies that changing a select updates the
// bound state and the dependent text span. This test is EXPECTED TO FAIL
// until the select body is completed (select renders no options yet).
func TestInteraction_SelectChange(t *testing.T) {
	const src = `
import . "sngl:ui"
window {
    var fruit = "apple"
    text(value="picked: {fruit}")
    select(options=["apple","banana","cherry"], :value=fruit)
}
`
	browser := startComponent(t, src)
	defer browser.Close()

	page := browser.Page()

	// Select "banana" from the <select> element.
	// Use the non-panicking Select so a missing option produces a test failure
	// rather than a panic (the select body is incomplete and renders no options yet).
	sel := page.MustElement("select")
	if err := sel.Select([]string{"banana"}, true, rod.SelectorTypeText); err != nil {
		t.Fatalf("select banana: %v (expected failure: select renders no options yet)", err)
	}
	_ = browser.WaitStable(stableWait)

	// Assert the span reflects the new selection.
	spanText := page.MustElement("span").MustText()
	if spanText != "picked: banana" {
		t.Errorf("span text after select: want %q got %q", "picked: banana", spanText)
	}
}
