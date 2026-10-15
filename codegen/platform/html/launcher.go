//go:build !js

package html

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	"duckfam.us/sngl/codegen"
	"duckfam.us/sngl/codegen/platform/html/internal/webtest"
	"duckfam.us/sngl/ir"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/launcher"
	"github.com/go-rod/rod/lib/proto"
	"nhooyr.io/websocket"
)

// browserSteps bounds each step of driving the browser for a test run.
//
// Every one of them is a wait on another process, and none had a bound: rod's
// launcher waits for Chromium to print its DevTools URL with no deadline (and
// for leakless to report a pid outside even the context it is given), Connect
// and opening the page wait on CDP answers, the agent's messages arrive when
// they arrive, and Cleanup waits for the browser to exit. One stall in a
// loaded suite then held `sngl test`, and the test binary waiting on it, until
// Go's test timeout killed the binary and left both running -- naming no
// step, so nothing said which wait it was. Each step now fails with its name.
type browserSteps struct {
	launch       time.Duration // Chromium prints its DevTools URL
	connect      time.Duration // rod connects to it
	openPage     time.Duration // the test page is opened
	agentConnect time.Duration // the page's agent dials back
	agentIdle    time.Duration // the longest the agent may go without a message
	close        time.Duration // the browser closes and exits
}

var cdpSteps = browserSteps{
	launch:       30 * time.Second,
	connect:      15 * time.Second,
	openPage:     30 * time.Second,
	agentConnect: 30 * time.Second,
	agentIdle:    2 * time.Minute,
	close:        15 * time.Second,
}

// findBrowser is the browser LaunchTest runs; a test points it at a fake.
var findBrowser = launcher.LookPath

// LaunchTest implements codegen.TestLauncher for html. Hosts the
// generated page on a localhost HTTP server, accepts the agent's
// WebSocket connection on a second port, and drives a Chromium
// instance via rod. Skip-clean when Chrome/Chromium not on PATH.
func (g *Generator) LaunchTest(ctx context.Context, dir string, lang codegen.LangTranslator, opts *ir.StructLit) (codegen.RPCChannel, codegen.Cleanup, error) {
	browserPath, found := findBrowser()
	if !found {
		return nil, nil, &codegen.SkipError{Reason: "Chrome/Chromium not on PATH"}
	}

	pageL, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, nil, fmt.Errorf("listen page: %w", err)
	}
	pagePort := pageL.Addr().(*net.TCPAddr).Port
	pageSrv := &http.Server{Handler: http.FileServer(http.Dir(dir))}
	go pageSrv.Serve(pageL)

	accepted := make(chan codegen.RPCChannel, 1)
	accErr := make(chan error, 1)
	wsHandler := func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, &websocket.AcceptOptions{
			// Browser connects from 127.0.0.1:<pagePort> to
			// 127.0.0.1:<wsPort>; that's cross-origin from the WS
			// server's perspective, and websocket.Accept rejects
			// mismatched Origin by default.
			InsecureSkipVerify: true,
		})
		if err != nil {
			accErr <- err
			return
		}
		accepted <- newWSChannel(ctx, c)
	}
	wsL, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		pageSrv.Close()
		return nil, nil, fmt.Errorf("listen ws: %w", err)
	}
	wsPort := wsL.Addr().(*net.TCPAddr).Port
	wsSrv := &http.Server{Handler: http.HandlerFunc(wsHandler)}
	go wsSrv.Serve(wsL)

	pageURL := fmt.Sprintf("http://127.0.0.1:%d/index.html?sngl_port=%d", pagePort, wsPort)

	l := launcher.New().Bin(browserPath).Headless(true)
	var browser *rod.Browser
	// fail closes whatever has been started, each close bounded, and returns
	// err; every failure after the servers are up goes through it.
	fail := func(err error) (codegen.RPCChannel, codegen.Cleanup, error) {
		pageSrv.Close()
		wsSrv.Close()
		closeBrowser(l, browser)
		return nil, nil, err
	}

	browserWSURL, err := webtest.Within("launch browser", cdpSteps.launch, l.Launch)
	if err != nil {
		return fail(err)
	}
	connecting := rod.New().ControlURL(browserWSURL)
	if _, err := webtest.Within("connect to browser", cdpSteps.connect, func() (struct{}, error) {
		return struct{}{}, connecting.Connect()
	}); err != nil {
		return fail(err)
	}
	browser = connecting
	if _, err := webtest.Within("open test page", cdpSteps.openPage, func() (*rod.Page, error) {
		return browser.Page(proto.TargetCreateTarget{URL: pageURL})
	}); err != nil {
		return fail(err)
	}

	select {
	case ch := <-accepted:
		cleanup := func() {
			_ = ch.Close()
			pageSrv.Close()
			wsSrv.Close()
			closeBrowser(l, browser)
		}
		return ch, cleanup, nil
	case err := <-accErr:
		return fail(fmt.Errorf("wait for agent: %w", err))
	case <-time.After(cdpSteps.agentConnect):
		return fail(fmt.Errorf("wait for agent: the page did not connect within %v", cdpSteps.agentConnect))
	}
}

// closeBrowser closes the browser, waits for it to exit and removes its
// profile directory, killing it where it was never connected to or either
// step stalls. browser is nil when the launch never got as far.
func closeBrowser(l *launcher.Launcher, browser *rod.Browser) {
	webtest.CloseBrowser(l, browser, cdpSteps.close)
}

// wsChannel adapts a nhooyr.io/websocket.Conn to codegen.RPCChannel.
//
// Driver reads via bufio.Scanner expecting newline-delimited frames;
// browser sends one JSON-RPC message per WebSocket text frame.
// Adapter appends '\n' to incoming frames and strips it from
// outgoing — translating between the two framing conventions.
type wsChannel struct {
	conn  *websocket.Conn
	ctx   context.Context
	rdBuf []byte
}

func newWSChannel(ctx context.Context, conn *websocket.Conn) *wsChannel {
	return &wsChannel{conn: conn, ctx: ctx}
}

func (w *wsChannel) Read(p []byte) (int, error) {
	if len(w.rdBuf) > 0 {
		n := copy(p, w.rdBuf)
		w.rdBuf = w.rdBuf[n:]
		return n, nil
	}
	ctx, cancel := context.WithTimeout(w.ctx, cdpSteps.agentIdle)
	defer cancel()
	typ, data, err := w.conn.Read(ctx)
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded && w.ctx.Err() == nil {
			return 0, fmt.Errorf("run tests: the agent sent nothing for %v", cdpSteps.agentIdle)
		}
		return 0, err
	}
	if typ != websocket.MessageText {
		return 0, fmt.Errorf("unexpected ws frame type %v", typ)
	}
	data = append(data, '\n')
	n := copy(p, data)
	if n < len(data) {
		w.rdBuf = data[n:]
	}
	return n, nil
}

func (w *wsChannel) Write(p []byte) (int, error) {
	payload := p
	if len(payload) > 0 && payload[len(payload)-1] == '\n' {
		payload = payload[:len(payload)-1]
	}
	if err := w.conn.Write(w.ctx, websocket.MessageText, payload); err != nil {
		return 0, err
	}
	// Report the original write length (including any trailing '\n')
	// so the caller's accounting matches.
	return len(p), nil
}

func (w *wsChannel) Close() error {
	return w.conn.Close(websocket.StatusNormalClosure, "")
}

var _ io.ReadWriteCloser = (*wsChannel)(nil)
