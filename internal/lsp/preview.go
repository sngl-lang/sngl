package lsp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// previewServer is an HTTP file server for component preview PNGs.
// It binds to 127.0.0.1:0 and serves only paths that have been explicitly
// registered via RegisterAsset, keyed by sha256(absolute path).
type previewServer struct {
	mu       sync.RWMutex
	listener net.Listener
	server   *http.Server
	assets   map[string]string // sha → absolute path
	port     int
}

func newPreviewServer() *previewServer {
	return &previewServer{
		assets: map[string]string{},
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

func (s *previewServer) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/preview/", func(w http.ResponseWriter, r *http.Request) {
		// Expected path: /preview/<sha>.png
		name := strings.TrimPrefix(r.URL.Path, "/preview/")
		name = strings.TrimSuffix(name, ".png")
		if name == "" || strings.Contains(name, "/") {
			http.NotFound(w, r)
			return
		}
		s.mu.RLock()
		path, ok := s.assets[name]
		s.mu.RUnlock()
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("Cache-Control", "max-age=604800, immutable")
		http.ServeFile(w, r, path)
	})
	return mux
}
