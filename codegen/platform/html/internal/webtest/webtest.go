// Package webtest provides lightweight browser testing using Chrome DevTools Protocol.
//
// It wraps go-rod to drive a headless Chrome against a test HTTP server.
// If Chrome is not installed, tests skip gracefully.
package webtest

import (
	"net/http"
	"net/http/httptest"
	"sync"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/launcher"
	"github.com/go-rod/rod/lib/proto"
)

// Engine manages a test HTTP server and a shared Chrome process.
type Engine struct {
	handler http.Handler

	mu       sync.Mutex
	server   *httptest.Server
	browser  *rod.Browser
	startErr error
	started  bool
}

// New creates an Engine that serves the given handler.
func New(handler http.Handler) *Engine {
	return &Engine{handler: handler}
}

// StartHeadless launches a browser tab without test hooks, returning an error
// instead of skipping. Caller is responsible for closing the returned Browser.
func (e *Engine) StartHeadless(width, height int) (*Browser, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if err := e.ensureStartedLocked(); err != nil {
		return nil, err
	}

	page, err := e.browser.Page(proto.TargetCreateTarget{URL: "about:blank"})
	if err != nil {
		return nil, err
	}

	if err := e.browser.SetCookies(nil); err != nil {
		page.Close()
		return nil, err
	}

	page.MustSetViewport(width, height, 0, false)

	return &Browser{
		page:    page,
		baseURL: e.server.URL,
	}, nil
}

// BaseURL returns the test server's base URL, or empty if not started.
func (e *Engine) BaseURL() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.server == nil {
		return ""
	}
	return e.server.URL
}

// Close shuts down the Chrome process and test server.
func (e *Engine) Close() {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.browser != nil {
		e.browser.Close()
		e.browser = nil
	}
	if e.server != nil {
		e.server.Close()
		e.server = nil
	}
}

// ensureStartedLocked initializes the server and Chrome allocator.
// Must be called with e.mu held.
func (e *Engine) ensureStartedLocked() error {
	if e.started {
		return e.startErr
	}
	e.started = true

	e.server = httptest.NewServer(e.handler)

	path, found := launcher.LookPath()
	if !found {
		e.server.Close()
		e.server = nil
		e.startErr = launcher.ErrAlreadyLaunched
		return e.startErr
	}

	u := launcher.New().Bin(path).Headless(true).NoSandbox(true).MustLaunch()
	browser := rod.New().ControlURL(u)
	if err := browser.Connect(); err != nil {
		e.server.Close()
		e.server = nil
		e.startErr = err
		return e.startErr
	}

	e.browser = browser
	return nil
}
