package lsp

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/websocket"
)

// previewServer is an HTTP file server for component preview PNGs and rendered documents.
// It binds to 127.0.0.1:0 and serves only paths that have been explicitly
// registered via RegisterAsset, keyed by sha256(absolute path), and renders windows
// via a pluggable callback.
type previewServer struct {
	mu         sync.RWMutex
	listener   net.Listener
	server     *http.Server
	assets     map[string]string // sha → absolute path
	port       int
	renderHTML func(fileURI, windowName string) ([]byte, error)
	conns      map[*websocket.Conn]struct{} // active WS clients
	connsMu    sync.Mutex                   // protects writes to conns
}

func newPreviewServer() *previewServer {
	return &previewServer{
		assets: map[string]string{},
		conns:  map[*websocket.Conn]struct{}{},
	}
}

// Start binds the listener and launches the HTTP server in a goroutine.
// Returns an error if binding fails. Calling Start twice is a programming
// error.
func (s *previewServer) Start() error {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.listener = ln
	s.port = ln.Addr().(*net.TCPAddr).Port
	s.server = &http.Server{
		Handler:      s.handler(),
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 5 * time.Second,
	}
	s.mu.Unlock()
	go func() {
		_ = s.server.Serve(ln)
	}()
	return nil
}

// Stop shuts the server down with a short grace period. Safe to call once.
func (s *previewServer) Stop() {
	s.mu.RLock()
	srv := s.server
	s.mu.RUnlock()
	if srv == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	_ = srv.Shutdown(ctx)
}

// Port returns the bound TCP port (0 if not started).
func (s *previewServer) Port() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.port
}

// RegisterAsset adds an absolute PNG path to the served set and returns the
// http://127.0.0.1:<port>/preview/<sha>.png URL. Idempotent — calling twice
// with the same path returns the same URL.
func (s *previewServer) RegisterAsset(absPath string) string {
	sum := sha256.Sum256([]byte(absPath))
	sha := hex.EncodeToString(sum[:])
	s.mu.Lock()
	s.assets[sha] = absPath
	port := s.port
	s.mu.Unlock()
	return fmt.Sprintf("http://127.0.0.1:%d/preview/%s.png", port, sha)
}

// SetRenderer installs the doc→HTML render callback. Must be called
// before Start. The callback may return an error; the route surfaces it
// as a 500 with the error text in the body.
func (s *previewServer) SetRenderer(fn func(fileURI, windowName string) ([]byte, error)) {
	s.mu.Lock()
	s.renderHTML = fn
	s.mu.Unlock()
}

// BroadcastReload sends a reload message to every connected client.
// Stale connections (write errors) are dropped silently.
func (s *previewServer) BroadcastReload() {
	const msg = `{"type":"reload"}`
	s.connsMu.Lock()
	defer s.connsMu.Unlock()
	for c := range s.conns {
		if _, err := c.Write([]byte(msg)); err != nil {
			c.Close()
			delete(s.conns, c)
		}
	}
}

func (s *previewServer) handler() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/ws", websocket.Handler(s.wsHandler))
	mux.HandleFunc("/preview/", func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/preview/")
		if path == "" || strings.Contains(path, "..") {
			http.NotFound(w, r)
			return
		}
		// PNG asset: "<sha>.png" — no slash, ends with .png.
		if !strings.Contains(path, "/") && strings.HasSuffix(path, ".png") {
			s.serveAsset(w, r, strings.TrimSuffix(path, ".png"))
			return
		}
		// Window render: "<base64-uri>/<window>".
		parts := strings.SplitN(path, "/", 2)
		if len(parts) != 2 || parts[1] == "" {
			http.NotFound(w, r)
			return
		}
		uriBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
		if err != nil {
			http.NotFound(w, r)
			return
		}
		s.servePreview(w, r, string(uriBytes), parts[1])
	})
	return mux
}

// serveAsset handles the F1 PNG path.
func (s *previewServer) serveAsset(w http.ResponseWriter, r *http.Request, sha string) {
	s.mu.RLock()
	path, ok := s.assets[sha]
	s.mu.RUnlock()
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "max-age=604800, immutable")
	http.ServeFile(w, r, path)
}

// servePreview handles the F5 doc-render path.
func (s *previewServer) servePreview(w http.ResponseWriter, r *http.Request, uri, win string) {
	s.mu.RLock()
	render := s.renderHTML
	s.mu.RUnlock()
	if render == nil {
		http.Error(w, "preview renderer not initialized", http.StatusServiceUnavailable)
		return
	}
	body, err := render(uri, win)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Write(body)
}

// wsHandler runs the lifetime of a single WS connection. Registers the
// conn for broadcasts, then blocks reading client messages until the
// connection closes. Future: parse incoming "jump" messages for
// click-to-source; for now we just keep the connection alive.
func (s *previewServer) wsHandler(ws *websocket.Conn) {
	s.connsMu.Lock()
	s.conns[ws] = struct{}{}
	s.connsMu.Unlock()
	defer func() {
		s.connsMu.Lock()
		delete(s.conns, ws)
		s.connsMu.Unlock()
		ws.Close()
	}()
	buf := make([]byte, 1024)
	for {
		if _, err := ws.Read(buf); err != nil {
			return
		}
	}
}
