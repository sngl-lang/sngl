//go:build !js

package html

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/ir"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/launcher"
	"nhooyr.io/websocket"
)

// LaunchTest implements codegen.TestLauncher for html. Hosts the
// generated page on a localhost HTTP server, accepts the agent's
// WebSocket connection on a second port, and drives a Chromium
// instance via rod. Skip-clean when Chrome/Chromium not on PATH.
func (g *Generator) LaunchTest(ctx context.Context, dir string, lang codegen.LangTranslator, opts *ir.StructLit) (codegen.RPCChannel, codegen.Cleanup, error) {
	if _, found := launcher.LookPath(); !found {
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

	browserPath, _ := launcher.LookPath()
	l := launcher.New().Bin(browserPath).Headless(true)
	browserWSURL, err := l.Launch()
	if err != nil {
		pageSrv.Close()
		wsSrv.Close()
		return nil, nil, fmt.Errorf("browser launch: %w", err)
	}
	browser := rod.New().ControlURL(browserWSURL).MustConnect()
	page := browser.MustPage(pageURL)

	select {
	case ch := <-accepted:
		cleanup := func() {
			_ = ch.Close()
			pageSrv.Close()
			wsSrv.Close()
			_ = page.Close()
			_ = browser.Close()
			l.Cleanup()
		}
		return ch, cleanup, nil
	case err := <-accErr:
		pageSrv.Close()
		wsSrv.Close()
		_ = browser.Close()
		l.Cleanup()
		return nil, nil, fmt.Errorf("ws accept: %w", err)
	case <-time.After(15 * time.Second):
		pageSrv.Close()
		wsSrv.Close()
		_ = browser.Close()
		l.Cleanup()
		return nil, nil, fmt.Errorf("agent did not connect within 15s")
	}
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
	typ, data, err := w.conn.Read(w.ctx)
	if err != nil {
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
