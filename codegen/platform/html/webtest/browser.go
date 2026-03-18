package webtest

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"git.duckfam.us/jonathan/sngl/internal/imgdiff"
	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"
)

// SnapshotOption configures a Snapshot call.
type SnapshotOption func(*snapshotConfig)

type snapshotConfig struct {
	suffix string
	width  int
	height int
}

// Mobile captures at 375x812 (iPhone) and appends "_mobile" to the filename.
func Mobile(cfg *snapshotConfig) {
	cfg.suffix = "mobile"
	cfg.width = 375
	cfg.height = 812
}

var (
	// SnapshotColorTolerance is the max per-channel RGBA delta before a pixel counts as different.
	SnapshotColorTolerance uint8 = 10
	// SnapshotPixelTolerance is the max fraction of pixels that may differ before failing.
	SnapshotPixelTolerance float64 = 0.005
)

// Browser controls a single browser tab.
type Browser struct {
	page    *rod.Page
	baseURL string
	timeout time.Duration
}

const defaultTimeout = 10 * time.Second

// SetTimeout overrides the default action timeout (10s).
func (b *Browser) SetTimeout(d time.Duration) {
	b.timeout = d
}

func (b *Browser) actionTimeout() time.Duration {
	if b.timeout > 0 {
		return b.timeout
	}
	return defaultTimeout
}

// tp returns a page clone with the action timeout applied.
func (b *Browser) tp() *rod.Page {
	return b.page.Timeout(b.actionTimeout())
}

// el finds a visible element by CSS selector (or XPath if it starts with //).
func (b *Browser) el(sel string) (*rod.Element, error) {
	p := b.tp()
	_ = p.WaitStable(300 * time.Millisecond)
	if len(sel) > 1 && sel[:2] == "//" {
		el, err := p.ElementX(sel)
		if err != nil {
			return nil, err
		}
		return el, el.WaitVisible()
	}
	el, err := p.Element(sel)
	if err != nil {
		return nil, err
	}
	return el, el.WaitVisible()
}

// Navigate goes to the given path on the test server.
func (b *Browser) Navigate(t testing.TB, path string) {
	t.Helper()
	if err := b.tp().Navigate(b.baseURL + path); err != nil {
		t.Fatalf("webtest: navigate %s: %v", path, err)
	}
	if err := b.tp().WaitLoad(); err != nil {
		t.Fatalf("webtest: wait load %s: %v", path, err)
	}
}

// NavigateURL goes to an absolute URL.
func (b *Browser) NavigateURL(t testing.TB, url string) {
	t.Helper()
	if err := b.tp().Navigate(url); err != nil {
		t.Fatalf("webtest: navigate %s: %v", url, err)
	}
	if err := b.tp().WaitLoad(); err != nil {
		t.Fatalf("webtest: wait load: %v", err)
	}
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

// Click waits for the element to be visible then clicks it.
func (b *Browser) Click(t testing.TB, sel string) {
	t.Helper()
	el, err := b.el(sel)
	if err != nil {
		t.Fatalf("webtest: click %s: %v", sel, err)
	}
	if err := el.Click(proto.InputMouseButtonLeft, 1); err != nil {
		t.Fatalf("webtest: click %s: %v", sel, err)
	}
}

// WaitVisible waits for the element to become visible.
func (b *Browser) WaitVisible(t testing.TB, sel string) {
	t.Helper()
	if _, err := b.el(sel); err != nil {
		t.Fatalf("webtest: wait visible %s: %v", sel, err)
	}
}

// Text returns the text content of the element.
func (b *Browser) Text(t testing.TB, sel string) string {
	t.Helper()
	el, err := b.el(sel)
	if err != nil {
		t.Fatalf("webtest: text %s: %v", sel, err)
	}
	s, err := el.Text()
	if err != nil {
		t.Fatalf("webtest: text %s: %v", sel, err)
	}
	return s
}

// HasElement returns true if at least one element matches the selector.
func (b *Browser) HasElement(t testing.TB, sel string) bool {
	t.Helper()
	p := b.tp()
	has, _, err := p.Has(sel)
	if err != nil {
		t.Fatalf("webtest: has element %s: %v", sel, err)
	}
	return has
}

// Screenshot captures a full-page screenshot and saves it to path.
func (b *Browser) Screenshot(t testing.TB, path string) {
	t.Helper()
	buf, err := b.page.Screenshot(true, nil)
	if err != nil {
		t.Fatalf("webtest: screenshot: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("webtest: mkdir for screenshot: %v", err)
	}
	if err := os.WriteFile(path, buf, 0o644); err != nil {
		t.Fatalf("webtest: write screenshot: %v", err)
	}
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

// Snapshot captures a full-page screenshot and compares it against a golden
// file at testdata/snapshots/<TestName>[_suffix].png relative to the caller's
// source directory.
func (b *Browser) Snapshot(t testing.TB, opts ...SnapshotOption) {
	t.Helper()

	cfg := snapshotConfig{}
	for _, o := range opts {
		o(&cfg)
	}

	if cfg.width > 0 && cfg.height > 0 {
		b.page.MustSetViewport(cfg.width, cfg.height, 0, false)
		defer b.page.MustSetViewport(1280, 720, 0, false)
	}

	_ = b.tp().WaitStable(300 * time.Millisecond)

	buf, err := b.page.Screenshot(true, nil)
	if err != nil {
		t.Fatalf("webtest: snapshot screenshot: %v", err)
	}

	_, file, _, ok := runtime.Caller(1)
	if !ok {
		t.Fatal("webtest: snapshot: cannot determine caller")
	}
	dir := filepath.Join(filepath.Dir(file), "testdata", "snapshots")
	name := strings.NewReplacer("/", "_", "\\", "_", " ", "_").Replace(t.Name())
	if cfg.suffix != "" {
		name += "_" + cfg.suffix
	}
	goldenPath := filepath.Join(dir, name+".png")

	if os.Getenv("WEBTEST_UPDATE") == "1" || !fileExists(goldenPath) {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("webtest: snapshot mkdir: %v", err)
		}
		if err := os.WriteFile(goldenPath, buf, 0o644); err != nil {
			t.Fatalf("webtest: snapshot write golden: %v", err)
		}
		return
	}

	golden, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("webtest: snapshot read golden: %v", err)
	}

	diffPath := strings.TrimSuffix(goldenPath, ".png") + ".diff.png"
	if err := imgdiff.Compare(golden, buf, diffPath, SnapshotColorTolerance, SnapshotPixelTolerance); err != nil {
		actualPath := strings.TrimSuffix(goldenPath, ".png") + ".actual.png"
		if writeErr := os.WriteFile(actualPath, buf, 0o644); writeErr != nil {
			t.Logf("webtest: failed to write actual: %v", writeErr)
		}
		t.Logf("webtest: diff image saved to %s", diffPath)
		t.Errorf("webtest: snapshot mismatch for %s: %v\n  actual: %s", filepath.Base(goldenPath), err, actualPath)
	}
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func (b *Browser) screenshotOnFailure(t testing.TB) {
	dir := os.Getenv("WEBTEST_SCREENSHOT_DIR")
	if dir == "" {
		dir = os.TempDir()
	}

	html, _ := b.page.HTML()

	buf, err := b.page.Screenshot(true, nil)
	if err != nil {
		t.Logf("webtest: failed to capture screenshot: %v", err)
		return
	}

	path := filepath.Join(dir, fmt.Sprintf("webtest-%s.png", t.Name()))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Logf("webtest: failed to create screenshot dir: %v", err)
		return
	}
	if err := os.WriteFile(path, buf, 0o644); err != nil {
		t.Logf("webtest: failed to write screenshot: %v", err)
		return
	}
	t.Logf("webtest: screenshot saved to %s", path)

	if html != "" {
		htmlPath := filepath.Join(dir, fmt.Sprintf("webtest-%s.html", t.Name()))
		if err := os.WriteFile(htmlPath, []byte(html), 0o644); err == nil {
			t.Logf("webtest: page source saved to %s", htmlPath)
		}
	}
}
