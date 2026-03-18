package webtest

import (
	"time"

	"github.com/go-rod/rod"
)

// Browser controls a single browser tab.
type Browser struct {
	page    *rod.Page
	baseURL string
}

const defaultTimeout = 10 * time.Second

// tp returns a page clone with the action timeout applied.
func (b *Browser) tp() *rod.Page {
	return b.page.Timeout(defaultTimeout)
}

// NavigateRaw navigates to a URL without test helpers, returning an error.
func (b *Browser) NavigateRaw(url string) error {
	if err := b.tp().Navigate(url); err != nil {
		return err
	}
	return b.tp().WaitLoad()
}

// WaitStable waits for the page to settle (no DOM mutations for the given duration).
func (b *Browser) WaitStable(d time.Duration) error {
	return b.tp().WaitStable(d)
}

// ScreenshotRaw captures a full-page screenshot and returns the PNG bytes.
func (b *Browser) ScreenshotRaw() ([]byte, error) {
	return b.page.Screenshot(true, nil)
}

// Page returns the underlying rod page for direct CDP access.
func (b *Browser) Page() *rod.Page {
	return b.page
}

// Close closes the browser tab.
func (b *Browser) Close() {
	b.page.Close()
}
