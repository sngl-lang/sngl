# LSP Side-Panel Live Preview Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A local browser side-panel renders the SNGL window at the cursor, live-reloading on every save. Opened via LSP `workspace/executeCommand sngl.openPreview`; served from the existing embedded HTTP server in `sngl lsp`.

**Architecture:** Extend the F1 preview server with two new HTTP routes — `/preview/<file-id>/<window-name>` (renders the doc to HTML on demand via `codegen/platform/html.Generator.Generate`) and `/ws` (a `golang.org/x/net/websocket` endpoint that broadcasts reload messages on `textDocument/didChange`). The rendered HTML gets a small JS preamble injected that connects to `/ws` and reloads on a `{type:"reload"}` message. The LSP exposes `sngl.openPreview` via `workspace/executeCommand`, which returns the URL to open. On stale check/optimize errors, the server keeps serving the last successful render with a small banner.

**Tech Stack:** Go, existing `internal/lsp` and `codegen/platform/html` packages, `golang.org/x/net/websocket` (already a transitive dep via `golang.org/x/net`).

**Spec:** `docs/superpowers/specs/2026-05-18-lsp-previews-design.md` §F5.

## Scope decisions (out of this slice)

| Spec piece | Status | Reason |
|---|---|---|
| Click-to-source (`data-sngl-pos` + WS `jump` event) | deferred | Needs HTML codegen changes to emit per-element source positions. Substantial; ship core side-panel first. |
| Session token / WS handshake auth | deferred | Server is 127.0.0.1-only; non-loopback origins can't reach. Add when other localhost software warrants. |
| Multi-window navigation in the preview | deferred | Per spec — "just the chosen window". |
| VS Code webview integration | deferred | Spec calls for `.vsix` plugin; defer along with the F4 webview work. nvim opens via `xdg-open`. |
| Stale-on-error banner UI polish | basic | Implement the "preserve last good render + plain text banner" path; no styling beyond a `<div>`. |

---

## File Structure

| File | Status | Responsibility |
|---|---|---|
| `internal/lsp/preview.go` | modify | Extend `previewServer` with `/preview/...`, `/ws` routes; live-reload broadcast hub |
| `internal/lsp/preview_render.go` | create | `renderDocAsHTML(pkg, windowName) ([]byte, error)` — invokes `html.Generator.Generate`, extracts the window's HTML, injects live-reload script |
| `internal/lsp/preview_test.go` | modify | Add tests for render, broadcast, executeCommand |
| `internal/lsp/handler.go` | modify | `didChange` triggers debounced reload broadcast; advertise `executeCommandProvider`; dispatch `workspace/executeCommand` |
| `internal/lsp/server.go` | modify | Dispatch `workspace/executeCommand` |
| `internal/lsp/command.go` | create | Handler for `sngl.openPreview` |
| `internal/lsp/protocol.go` | modify | Add `ExecuteCommandOptions`, `ExecuteCommandParams` types |
| `editors/neovim/lua/sngl/preview.lua` | modify | Handle `sngl/previewReady` (port discovery from F1) and add `:SnglPreview` command that calls executeCommand and opens the URL |

---

## Task 1: HTTP route for preview HTML

Extend `previewServer` with a `/preview/<file-id>/<window>` route that renders on demand. For now, return a placeholder so we can validate the routing before render is in place. The real renderer comes in Task 2.

**Files:**
- Modify: `internal/lsp/preview.go`
- Modify: `internal/lsp/preview_test.go`

- [ ] **Step 1: Add render-callback field to previewServer**

In `internal/lsp/preview.go`, extend the struct:

```go
type previewServer struct {
	mu         sync.RWMutex
	listener   net.Listener
	server     *http.Server
	assets     map[string]string // sha → absolute path
	port       int
	// renderHTML returns the rendered HTML for a given (fileURI, windowName).
	// Set by Server.New() after the workspace is wired. nil if unset →
	// the route returns 503.
	renderHTML func(fileURI, windowName string) ([]byte, error)
}
```

Add a setter:

```go
// SetRenderer installs the doc→HTML render callback. Must be called
// before Start. The callback may return an error; the route surfaces it
// as a 500 with the error text in the body.
func (s *previewServer) SetRenderer(fn func(fileURI, windowName string) ([]byte, error)) {
	s.mu.Lock()
	s.renderHTML = fn
	s.mu.Unlock()
}
```

- [ ] **Step 2: Write failing test**

Append to `internal/lsp/preview_test.go`:

```go
func TestPreviewServer_RendersWindow(t *testing.T) {
	srv := newPreviewServer()
	srv.SetRenderer(func(uri, win string) ([]byte, error) {
		return []byte(fmt.Sprintf("<html><body>%s/%s</body></html>", uri, win)), nil
	})
	if err := srv.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer srv.Stop()

	// URL is /preview/<base64-of-uri>/<window>
	uri := "file:///tmp/x.sngl"
	url := fmt.Sprintf("http://127.0.0.1:%d/preview/%s/main",
		srv.Port(), base64.RawURLEncoding.EncodeToString([]byte(uri)))
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	got := string(body)
	if !strings.Contains(got, "file:///tmp/x.sngl/main") {
		t.Errorf("body = %q, want substring %q", got, "file:///tmp/x.sngl/main")
	}
}

func TestPreviewServer_PreviewMissingRenderer503(t *testing.T) {
	srv := newPreviewServer()
	if err := srv.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer srv.Stop()

	uri := base64.RawURLEncoding.EncodeToString([]byte("file:///x"))
	url := fmt.Sprintf("http://127.0.0.1:%d/preview/%s/main", srv.Port(), uri)
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 503 {
		t.Errorf("status = %d, want 503", resp.StatusCode)
	}
}
```

Add `"encoding/base64"` and `"fmt"` to imports if absent.

- [ ] **Step 3: Run, verify failure**

Run: `go test ./internal/lsp/ -run "TestPreviewServer_RendersWindow|TestPreviewServer_PreviewMissingRenderer503" -v`
Expected: FAIL — the `/preview/` route doesn't exist yet (or only matches the F1 PNG pattern).

- [ ] **Step 4: Add the preview route**

In `internal/lsp/preview.go`, find the `handler()` method. The existing handler registers `/preview/` for PNG asset lookup. We need to differentiate: `/preview/<sha>.png` (existing, F1) vs `/preview/<base64-uri>/<window>` (new). The existing path keys are sha hex (64 chars) ending in `.png`. The new path keys are arbitrary base64 followed by `/<window>`.

Rewrite the `/preview/` handler to dispatch based on the path shape:

```go
func (s *previewServer) handler() http.Handler {
	mux := http.NewServeMux()
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
```

Add `"encoding/base64"` to the imports if absent.

- [ ] **Step 5: Run, verify pass**

Run: `go test ./internal/lsp/ -run "TestPreviewServer_RendersWindow|TestPreviewServer_PreviewMissingRenderer503|TestPreviewServer_ServesRegisteredAsset|TestPreviewServer_404OnUnknownSha" -v`
Expected: all PASS — both new tests and the F1 PNG ones.

- [ ] **Step 6: Commit**

```bash
git add internal/lsp/preview.go internal/lsp/preview_test.go
git commit -m "lsp(preview): add /preview/<uri>/<window> route with pluggable renderer"
```

---

## Task 2: Doc-to-HTML renderer

Build the function that takes the workspace's IR for a file + a window name and produces HTML. Uses `codegen/platform/html.Generator.Generate`.

**Files:**
- Create: `internal/lsp/preview_render.go`
- Create: `internal/lsp/preview_render_test.go`

- [ ] **Step 1: Write failing test**

Create `internal/lsp/preview_render_test.go`:

```go
package lsp

import (
	"path/filepath"
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl"
	"git.duckfam.us/jonathan/sngl/internal/parser"
	"git.duckfam.us/jonathan/sngl/ir"
)

func TestRenderDocAsHTML_SimpleWindow(t *testing.T) {
	src := `
window #home(title="Home", href="/") {
    text(value="hello world")
}
`
	doc, err := parser.Parse("t.sngl", []byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	dir, _ := filepath.Abs("../../testdata/lsp")
	pkg, diags := sngl.Check(doc, dir)
	for _, d := range diags {
		if d.Severity == ir.Error {
			t.Fatalf("check: %s", d.Error())
		}
	}
	html, err := renderDocAsHTML(pkg, "home")
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	body := string(html)
	if !strings.Contains(body, "hello world") {
		t.Errorf("body missing %q; got:\n%s", "hello world", body)
	}
	if !strings.Contains(body, "<!doctype") && !strings.Contains(body, "<!DOCTYPE") {
		t.Errorf("body missing doctype; got:\n%s", body)
	}
}

func TestRenderDocAsHTML_UnknownWindow(t *testing.T) {
	src := `window #home(title="x", href="/") { text(value="hi") }`
	doc, _ := parser.Parse("t.sngl", []byte(src))
	dir, _ := filepath.Abs("../../testdata/lsp")
	pkg, _ := sngl.Check(doc, dir)
	_, err := renderDocAsHTML(pkg, "notreal")
	if err == nil {
		t.Fatal("expected error for unknown window")
	}
	if !strings.Contains(err.Error(), "notreal") {
		t.Errorf("error %q should mention the window name", err)
	}
}
```

- [ ] **Step 2: Run, verify failure**

Run: `go test ./internal/lsp/ -run TestRenderDocAsHTML -v`
Expected: FAIL with "undefined: renderDocAsHTML".

- [ ] **Step 3: Implement the renderer**

Create `internal/lsp/preview_render.go`:

```go
package lsp

import (
	"fmt"
	"strings"

	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/codegen/platform/html"
	"git.duckfam.us/jonathan/sngl/codegen/lang/none"
	"git.duckfam.us/jonathan/sngl/ir"
)

// renderDocAsHTML compiles pkg through the html platform with the none
// language and returns the HTML for the named window. Returns an error if
// the window doesn't exist or codegen fails.
func renderDocAsHTML(pkg *ir.Package, windowName string) ([]byte, error) {
	if pkg == nil {
		return nil, fmt.Errorf("nil package")
	}
	var found *ir.Window
	for _, w := range pkg.Windows {
		if w.ID == windowName {
			found = w
			break
		}
	}
	if found == nil {
		return nil, fmt.Errorf("window %q not found in package", windowName)
	}

	gen := &html.Generator{}
	lang := &none.Translator{}
	req := &codegen.Request{
		Pkg:    pkg,
		Lang:   lang,
		Source: "preview.sngl",
	}
	resp, err := gen.Generate(req)
	if err != nil {
		return nil, fmt.Errorf("html generate: %w", err)
	}
	if resp.Error != "" {
		return nil, fmt.Errorf("html generate: %s", resp.Error)
	}

	// The html platform writes one .html file per window. Find the one
	// whose path matches the window name. The convention is "<name>.html"
	// for routes / "index.html" for the lone window.
	matchSuffix := windowName + ".html"
	var single []byte
	for _, f := range resp.Files {
		if strings.HasSuffix(f.Path, matchSuffix) || (len(resp.Files) == 1 && strings.HasSuffix(f.Path, ".html")) {
			single = f.Content
			break
		}
	}
	if single == nil {
		return nil, fmt.Errorf("html generate: no output file matched window %q", windowName)
	}
	return injectLiveReloadScript(single), nil
}

// injectLiveReloadScript appends a small inline script that connects to
// /ws and reloads on a {"type":"reload"} message. Inserted before
// </body> if present, otherwise appended.
func injectLiveReloadScript(html []byte) []byte {
	const script = `<script>
(function(){
  try {
    var ws = new WebSocket("ws://" + location.host + "/ws");
    ws.onmessage = function(e){
      try { var m = JSON.parse(e.data); if (m && m.type === "reload") location.reload(); }
      catch (_) {}
    };
  } catch (_) {}
})();
</script>`
	idx := strings.LastIndex(string(html), "</body>")
	if idx < 0 {
		return append(html, []byte(script)...)
	}
	out := make([]byte, 0, len(html)+len(script))
	out = append(out, html[:idx]...)
	out = append(out, []byte(script)...)
	out = append(out, html[idx:]...)
	return out
}
```

**Note:** the `none` language translator may not exist as `codegen/lang/none`. Verify with `ls codegen/lang/`. If it doesn't exist:

- The html platform's static mode uses no language at all (Generator handles everything internally).
- Try `req.Lang = nil` first. If Generate rejects nil, look at how `sngl dump optimized --lang none --platform none` constructs the request and mirror it.

Adjust the import + Lang assignment based on what actually works. The test in Step 2 catches the wrong shape.

- [ ] **Step 4: Run, verify pass**

Run: `go test ./internal/lsp/ -run TestRenderDocAsHTML -v`
Expected: both PASS.

If the renderer fails with "html platform requires a Lang", look at `codegen/platform/html/routes.go:18` for the actual error path and substitute the right Lang. The `--lang none` mode in `sngl` CLI is the model.

- [ ] **Step 5: Commit**

```bash
git add internal/lsp/preview_render.go internal/lsp/preview_render_test.go
git commit -m "lsp(preview): renderDocAsHTML via html platform + live-reload injection"
```

---

## Task 3: WebSocket endpoint and broadcast hub

Add `/ws` route using `golang.org/x/net/websocket`. The hub tracks live connections; broadcasts a `{"type":"reload"}` JSON message to all.

**Files:**
- Modify: `internal/lsp/preview.go`
- Modify: `internal/lsp/preview_test.go`

- [ ] **Step 1: Add hub to previewServer**

In `internal/lsp/preview.go`, extend the struct:

```go
type previewServer struct {
	mu         sync.RWMutex
	listener   net.Listener
	server     *http.Server
	assets     map[string]string
	port       int
	renderHTML func(fileURI, windowName string) ([]byte, error)
	conns      map[*websocket.Conn]struct{} // active WS clients
	connsMu    sync.Mutex                    // protects writes to conns
}
```

Update `newPreviewServer`:

```go
func newPreviewServer() *previewServer {
	return &previewServer{
		assets: map[string]string{},
		conns:  map[*websocket.Conn]struct{}{},
	}
}
```

Add the broadcast method:

```go
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
```

Add `"golang.org/x/net/websocket"` to the imports.

- [ ] **Step 2: Register the /ws route**

In `handler()`, register the WS handler:

```go
mux.Handle("/ws", websocket.Handler(s.wsHandler))
```

Implement `wsHandler`:

```go
// wsHandler runs the lifetime of a single WS connection. It registers
// the conn for broadcasts, then blocks reading client messages until the
// connection closes. (Future: parse incoming "jump" messages for
// click-to-source; for now we just keep the connection alive.)
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
	// Drain incoming messages — we don't process them yet, but reading
	// keeps the connection healthy and detects close.
	buf := make([]byte, 1024)
	for {
		if _, err := ws.Read(buf); err != nil {
			return
		}
	}
}
```

- [ ] **Step 3: Test the broadcast**

Append to `internal/lsp/preview_test.go`:

```go
func TestPreviewServer_WSBroadcastReload(t *testing.T) {
	srv := newPreviewServer()
	if err := srv.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer srv.Stop()

	url := fmt.Sprintf("ws://127.0.0.1:%d/ws", srv.Port())
	ws, err := websocket.Dial(url, "", "http://127.0.0.1/")
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer ws.Close()

	// Give server a moment to register the conn.
	time.Sleep(50 * time.Millisecond)

	srv.BroadcastReload()

	ws.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 1024)
	n, err := ws.Read(buf)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	got := string(buf[:n])
	if !strings.Contains(got, `"reload"`) {
		t.Errorf("got %q, want reload message", got)
	}
}
```

Add `"golang.org/x/net/websocket"` to test imports.

- [ ] **Step 4: Run, verify pass**

Run: `go test ./internal/lsp/ -run TestPreviewServer_WSBroadcastReload -v`
Expected: PASS.

If FAIL with a timeout, the broadcast might be racing the conn-registration. The 50ms sleep is a hack; if flaky, replace with a polling check (`for srv.conns is empty { ... }`).

- [ ] **Step 5: Commit**

```bash
git add internal/lsp/preview.go internal/lsp/preview_test.go
git commit -m "lsp(preview): /ws endpoint with reload broadcast hub"
```

---

## Task 4: Trigger reload on didChange

Hook the WS broadcast into the LSP `didChange` flow with a 200ms debounce.

**Files:**
- Modify: `internal/lsp/handler.go`
- Modify: `internal/lsp/server.go`

- [ ] **Step 1: Add debouncer to server**

In `internal/lsp/server.go`, extend the Server struct:

```go
type Server struct {
	ws            *workspace
	reader        *bufio.Reader
	writer        io.Writer
	mu            sync.Mutex
	log           *log.Logger
	preview       *previewServer
	reloadTimer   *time.Timer
	reloadTimerMu sync.Mutex
}
```

(Preserve any other existing fields. Adjust if `time` isn't yet imported.)

Add a method:

```go
// scheduleReload debounces preview-reload broadcasts. Multiple didChange
// notifications within 200ms collapse into one reload to avoid thrashing
// the browser.
func (s *Server) scheduleReload() {
	const delay = 200 * time.Millisecond
	s.reloadTimerMu.Lock()
	defer s.reloadTimerMu.Unlock()
	if s.reloadTimer != nil {
		s.reloadTimer.Stop()
	}
	s.reloadTimer = time.AfterFunc(delay, func() {
		if s.preview != nil {
			s.preview.BroadcastReload()
		}
	})
}
```

Add `"time"` to imports.

- [ ] **Step 2: Trigger on didChange and didSave**

In `internal/lsp/handler.go`, find `handleDidChange`. At the end (after diagnostics are published):

```go
func (s *Server) handleDidChange(params json.RawMessage) {
	var p DidChangeTextDocumentParams
	if err := json.Unmarshal(params, &p); err != nil {
		s.log.Printf("didChange unmarshal: %v", err)
		return
	}
	if len(p.ContentChanges) == 0 {
		return
	}
	text := p.ContentChanges[len(p.ContentChanges)-1].Text
	fs := s.ws.update(p.TextDocument.URI, text, p.TextDocument.Version)
	diags := s.analyze(fs)
	s.publishDiagnostics(p.TextDocument.URI, diags)
	s.scheduleReload()
}
```

Same call at the end of `handleDidSave` (for editors that don't send didChange on save).

- [ ] **Step 3: Wire renderer into preview server**

In `internal/lsp/handler.go`, at the start of `handleInitialize` (before `s.preview.Start()`), set the renderer:

```go
func (s *Server) handleInitialize(id json.RawMessage, params json.RawMessage) {
	s.preview.SetRenderer(func(uri, windowName string) ([]byte, error) {
		fs := s.ws.get(uri)
		if fs == nil || fs.Doc == nil {
			return nil, fmt.Errorf("document not open: %s", uri)
		}
		pkg, err := s.checkForPreview(fs)
		if err != nil {
			return nil, err
		}
		return renderDocAsHTML(pkg, windowName)
	})
	if err := s.preview.Start(); err != nil {
		s.log.Printf("preview server: %v", err)
	}
	// ...existing initialize body continues...
```

Add `checkForPreview` to `internal/lsp/handler.go` (or wherever `analyze` is):

```go
// checkForPreview type-checks the workspace file and returns the IR
// package. Unlike analyze (which collects diagnostics for publishing),
// this short-circuits on any check error so the preview server can
// fall back to its last good render.
func (s *Server) checkForPreview(fs *fileState) (*ir.Package, error) {
	dir := uriToDir(fs.URI)
	pkg, diags := sngl.Check(fs.Doc, dir)
	for _, d := range diags {
		if d.Severity == ir.Error {
			return nil, fmt.Errorf("%s", d.Error())
		}
	}
	return pkg, nil
}

// uriToDir extracts the directory containing a file:// URI's file.
func uriToDir(uri string) string {
	const prefix = "file://"
	if !strings.HasPrefix(uri, prefix) {
		return "."
	}
	return filepath.Dir(uri[len(prefix):])
}
```

Add imports as needed: `"fmt"`, `"path/filepath"`, `"strings"`, `"git.duckfam.us/jonathan/sngl"`, `"git.duckfam.us/jonathan/sngl/ir"`.

- [ ] **Step 4: Add stale-on-error caching**

Refine the renderer closure to keep the last good render per (URI, window):

```go
type previewCache struct {
	mu      sync.Mutex
	lastGood map[string][]byte // key: uri + "|" + window
}

func newPreviewCache() *previewCache {
	return &previewCache{lastGood: map[string][]byte{}}
}

func (c *previewCache) key(uri, window string) string { return uri + "|" + window }
func (c *previewCache) get(uri, window string) ([]byte, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	b, ok := c.lastGood[c.key(uri, window)]
	return b, ok
}
func (c *previewCache) set(uri, window string, body []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.lastGood[c.key(uri, window)] = body
}
```

Add a `cache *previewCache` field to `Server`, initialize in `New`, and use it in the renderer:

```go
s.preview.SetRenderer(func(uri, windowName string) ([]byte, error) {
	fs := s.ws.get(uri)
	if fs == nil || fs.Doc == nil {
		return nil, fmt.Errorf("document not open: %s", uri)
	}
	pkg, err := s.checkForPreview(fs)
	if err != nil {
		// Serve last good render with a banner.
		if stale, ok := s.cache.get(uri, windowName); ok {
			return appendErrorBanner(stale, err.Error()), nil
		}
		return nil, err
	}
	body, rerr := renderDocAsHTML(pkg, windowName)
	if rerr != nil {
		if stale, ok := s.cache.get(uri, windowName); ok {
			return appendErrorBanner(stale, rerr.Error()), nil
		}
		return nil, rerr
	}
	s.cache.set(uri, windowName, body)
	return body, nil
})
```

Add the banner helper to `internal/lsp/preview_render.go`:

```go
// appendErrorBanner injects a small fixed-position banner showing the
// most recent check error, so the user knows the displayed render is
// stale.
func appendErrorBanner(stale []byte, msg string) []byte {
	banner := []byte(fmt.Sprintf(`<div style="position:fixed;bottom:0;left:0;right:0;background:#fee;color:#900;padding:8px;font-family:monospace;border-top:2px solid #c00;z-index:9999">%s</div>`, htmlEscape(msg)))
	idx := strings.LastIndex(string(stale), "</body>")
	if idx < 0 {
		return append(stale, banner...)
	}
	out := make([]byte, 0, len(stale)+len(banner))
	out = append(out, stale[:idx]...)
	out = append(out, banner...)
	out = append(out, stale[idx:]...)
	return out
}

func htmlEscape(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;")
	return r.Replace(s)
}
```

- [ ] **Step 5: Run all lsp tests**

Run: `go test ./internal/lsp/... -count=1`
Expected: PASS.

The new debounce + cache pieces have no direct test (their effect shows in T7 smoke). Coverage comes from integration via the existing tests not regressing.

- [ ] **Step 6: Commit**

```bash
git add internal/lsp/handler.go internal/lsp/server.go internal/lsp/preview_render.go
git commit -m "lsp(preview): render on demand, debounced reload, stale-on-error fallback"
```

---

## Task 5: `workspace/executeCommand sngl.openPreview`

The LSP client invokes this to get the URL to open. Args: `{uri, position}`; resolves the enclosing window (or first window in file) and returns `{url}`.

**Files:**
- Modify: `internal/lsp/protocol.go`
- Create: `internal/lsp/command.go`
- Modify: `internal/lsp/handler.go`
- Modify: `internal/lsp/server.go`

- [ ] **Step 1: Add protocol types**

Append to `internal/lsp/protocol.go`:

```go
// --- Execute Command ---

type ExecuteCommandOptions struct {
	Commands []string `json:"commands"`
}

type ExecuteCommandParams struct {
	Command   string            `json:"command"`
	Arguments []json.RawMessage `json:"arguments,omitempty"`
}
```

Add `ExecuteCommandProvider *ExecuteCommandOptions` to `ServerCapabilities`.

- [ ] **Step 2: Advertise capability**

In `internal/lsp/handler.go`, inside `handleInitialize`'s `ServerCapabilities` struct literal, add:

```go
ExecuteCommandProvider: &ExecuteCommandOptions{
	Commands: []string{"sngl.openPreview"},
},
```

- [ ] **Step 3: Create command handler**

Create `internal/lsp/command.go`:

```go
package lsp

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
)

// handleExecuteCommand routes workspace/executeCommand to the appropriate
// handler. Today only sngl.openPreview is supported.
func (s *Server) handleExecuteCommand(id json.RawMessage, params json.RawMessage) {
	var p ExecuteCommandParams
	if err := json.Unmarshal(params, &p); err != nil {
		s.sendError(id, -32602, "invalid params")
		return
	}
	switch p.Command {
	case "sngl.openPreview":
		s.cmdOpenPreview(id, p.Arguments)
	default:
		s.sendError(id, -32601, "unknown command: "+p.Command)
	}
}

// cmdOpenPreview parses {uri, position?} from args and returns
// {"url": "http://127.0.0.1:PORT/preview/<base64-uri>/<window>"}.
func (s *Server) cmdOpenPreview(id json.RawMessage, args []json.RawMessage) {
	if len(args) < 1 {
		s.sendError(id, -32602, "sngl.openPreview requires {uri, position?}")
		return
	}
	var first struct {
		URI      string   `json:"uri"`
		Position Position `json:"position"`
	}
	if err := json.Unmarshal(args[0], &first); err != nil {
		s.sendError(id, -32602, "sngl.openPreview: invalid args")
		return
	}
	fs := s.ws.get(first.URI)
	if fs == nil || fs.Doc == nil {
		s.sendError(id, -32603, "document not open: "+first.URI)
		return
	}
	// Type-check to enumerate windows.
	pkg, err := s.checkForPreview(fs)
	if err != nil || pkg == nil || len(pkg.Windows) == 0 {
		s.sendError(id, -32603, "no windows in document")
		return
	}
	// For now, return the first window. (TODO: enclosing-window-at-position.)
	windowName := pkg.Windows[0].ID

	port := s.preview.Port()
	if port == 0 {
		s.sendError(id, -32603, "preview server not running")
		return
	}
	url := fmt.Sprintf("http://127.0.0.1:%d/preview/%s/%s",
		port, base64.RawURLEncoding.EncodeToString([]byte(first.URI)), windowName)
	s.sendResult(id, map[string]string{"url": url})
}
```

- [ ] **Step 4: Dispatch in server.go**

In `internal/lsp/server.go`, find the `switch req.Method` block. Add before `default`:

```go
case "workspace/executeCommand":
	s.handleExecuteCommand(req.ID, req.Params)
```

- [ ] **Step 5: Test the command**

Append to `internal/lsp/preview_test.go`:

```go
func TestServer_ExecuteCommandOpenPreview(t *testing.T) {
	r, w := io.Pipe()
	clientR, clientW := io.Pipe()
	srv := New()
	srv.reader = bufio.NewReader(r)
	srv.writer = clientW

	go func() { _ = srv.serve() }()
	defer srv.preview.Stop()

	send := func(body string) {
		header := fmt.Sprintf("Content-Length: %d\r\n\r\n", len(body))
		w.Write([]byte(header + body))
	}

	// initialize
	go send(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`)
	// Drain initialize response + previewReady notification.
	br := bufio.NewReader(clientR)
	for i := 0; i < 2; i++ {
		line, _ := br.ReadString('\n')
		ln := 0
		fmt.Sscanf(strings.TrimSpace(line), "Content-Length: %d", &ln)
		br.ReadString('\n')
		buf := make([]byte, ln)
		io.ReadFull(br, buf)
	}

	// didOpen with a window
	go send(`{"jsonrpc":"2.0","method":"textDocument/didOpen","params":{"textDocument":{"uri":"file:///tmp/x.sngl","languageId":"sngl","version":1,"text":"window #home(title=\"Home\", href=\"/\") {\n  text(value=\"hi\")\n}\n"}}}`)
	// Drain diagnostics notification.
	line, _ := br.ReadString('\n')
	ln := 0
	fmt.Sscanf(strings.TrimSpace(line), "Content-Length: %d", &ln)
	br.ReadString('\n')
	buf := make([]byte, ln)
	io.ReadFull(br, buf)

	// executeCommand sngl.openPreview
	go send(`{"jsonrpc":"2.0","id":2,"method":"workspace/executeCommand","params":{"command":"sngl.openPreview","arguments":[{"uri":"file:///tmp/x.sngl","position":{"line":0,"character":0}}]}}`)
	line, _ = br.ReadString('\n')
	fmt.Sscanf(strings.TrimSpace(line), "Content-Length: %d", &ln)
	br.ReadString('\n')
	buf = make([]byte, ln)
	io.ReadFull(br, buf)

	body := string(buf)
	if !strings.Contains(body, `"url"`) {
		t.Errorf("response missing url: %s", body)
	}
	if !strings.Contains(body, "/preview/") {
		t.Errorf("response missing /preview/: %s", body)
	}
}
```

Add imports as needed.

- [ ] **Step 6: Run, verify pass**

Run: `go test ./internal/lsp/ -run TestServer_ExecuteCommandOpenPreview -v`
Expected: PASS.

If FAIL because the test scaffolding is too brittle, simplify by directly calling `srv.cmdOpenPreview` with synthetic args.

- [ ] **Step 7: Commit**

```bash
git add internal/lsp/protocol.go internal/lsp/command.go internal/lsp/handler.go internal/lsp/server.go internal/lsp/preview_test.go
git commit -m "lsp: dispatch sngl.openPreview via workspace/executeCommand"
```

---

## Task 6: Neovim plugin command

Add `:SnglPreview` to the existing neovim plugin. It calls `workspace/executeCommand` and opens the returned URL via `vim.ui.open`.

**Files:**
- Modify: `editors/neovim/lua/sngl/preview.lua` (or `lua/sngl/init.lua` if no preview.lua exists)

- [ ] **Step 1: Inspect existing nvim plugin**

Run: `ls editors/neovim/lua/sngl/ && cat editors/neovim/lua/sngl/init.lua 2>/dev/null | head -30`

Determine whether a `preview.lua` already exists. If not, create one. The plugin loader pattern (init.lua) typically requires submodules — match the existing convention.

- [ ] **Step 2: Add :SnglPreview command**

Create or extend `editors/neovim/lua/sngl/preview.lua`:

```lua
local M = {}

-- preview_url is set by the sngl/previewReady LSP notification handler in
-- init.lua / treesitter.lua. We rely on the client being started before
-- :SnglPreview is invoked, which is normal nvim LSP order.

function M.setup(opts)
  vim.api.nvim_create_user_command("SnglPreview", function()
    local clients = vim.lsp.get_clients({ name = "sngl" })
    if #clients == 0 then
      vim.notify("sngl LSP client not attached", vim.log.levels.ERROR)
      return
    end
    local client = clients[1]
    local uri = vim.uri_from_bufnr(0)
    local pos = vim.api.nvim_win_get_cursor(0)
    local params = {
      command = "sngl.openPreview",
      arguments = {
        { uri = uri, position = { line = pos[1] - 1, character = pos[2] } },
      },
    }
    client.request("workspace/executeCommand", params, function(err, result)
      if err then
        vim.notify("sngl preview: " .. tostring(err.message or err), vim.log.levels.ERROR)
        return
      end
      if not result or not result.url then
        vim.notify("sngl preview: no URL in response", vim.log.levels.ERROR)
        return
      end
      vim.ui.open(result.url)
    end, 0)
  end, { desc = "Open SNGL side-panel preview in browser" })
end

return M
```

In `editors/neovim/lua/sngl/init.lua`, ensure `preview.lua` is loaded. If the existing init.lua dispatches to other modules:

```lua
require("sngl.preview").setup(opts)
```

Add this line in the appropriate place (next to existing module setups).

- [ ] **Step 3: Manual smoke (no automated test)**

Plugin code has no Go test harness. Verify manually:

1. `go install ./cmd/sngl` to rebuild.
2. Open nvim with a SNGL file containing a window.
3. `:LspRestart`
4. `:SnglPreview`
5. Default browser should open showing the rendered window.
6. Edit the file (e.g. change the text), save. The browser should reload.

If the URL isn't reachable, check `:lua print(vim.inspect(vim.lsp.get_clients()))` to verify the client is attached.

- [ ] **Step 4: Commit**

```bash
git add editors/neovim/lua/sngl/preview.lua editors/neovim/lua/sngl/init.lua
git commit -m "editors(neovim): :SnglPreview opens browser preview via LSP command"
```

---

## Task 7: Rebuild and full smoke

**Files:** none

- [ ] **Step 1: Rebuild**

Run: `go install ./cmd/sngl`
Expected: success.

- [ ] **Step 2: Full test sweep**

Run: `go test ./... -count=1`
Expected: PASS (pre-existing `TestDocSNGLFormat` unrelated).

- [ ] **Step 3: End-to-end manual smoke**

Create a fixture file:

```bash
cat > /tmp/preview_smoke.sngl <<'EOF'
window #home(title="Smoke", href="/") {
    vbox(style={gap=8, padding=16}) {
        text(value="Hello, side panel!")
        text(value="Edit this file and save to live-reload.", style={color=#ff8040})
    }
}
EOF
```

In nvim, open `/tmp/preview_smoke.sngl`, `:LspRestart`, then `:SnglPreview`. Verify the browser shows the rendered window with both lines visible and the orange text. Edit a value, save (`:w`), confirm the browser reloads.

Introduce a syntax error temporarily (e.g. delete a closing brace), save. The browser should show the previous render plus a red banner at the bottom with the error. Restore the file, save again. The banner disappears.

- [ ] **Step 4: Document the manual smoke result**

If everything works, no commit needed — just note in the final summary that smoke passed. If something doesn't work, file a follow-up issue rather than patching here.

---

## Self-Review

**Spec coverage (§F5):**

| Spec row | Task |
|---|---|
| Open via `workspace/executeCommand sngl.openPreview` returning `{url}` | Task 5 |
| `/preview/<file-id>/<window-name>` HTTP route returns HTML | Task 1 + 2 |
| Render via `codegen/platform/html` `--lang none` static mode | Task 2 |
| `/ws` WebSocket endpoint with `{type:"reload"}` broadcast on didChange | Task 3 + 4 |
| Debounced reload (200ms) | Task 4 |
| Stale render with banner on check/optimize error | Task 4 |
| Client-side reload listener injected into rendered HTML | Task 2 (`injectLiveReloadScript`) |
| nvim editor command | Task 6 |
| Click-to-source (`data-sngl-pos` + WS jump) | **DEFERRED** — see scope decisions block |
| Session token | **DEFERRED** |
| Multi-window navigation | **DEFERRED** |
| VS Code webview | **DEFERRED** |

**Placeholder scan:** Task 2 Step 3 has a sketch fallback for the Lang choice — concrete enough that the engineer can resolve by inspection. Task 6 Step 1 instructs to inspect the existing nvim plugin layout — also concrete.

**Type consistency:** `renderDocAsHTML`, `injectLiveReloadScript`, `appendErrorBanner`, `htmlEscape`, `previewCache`, `previewServer`, `BroadcastReload`, `SetRenderer`, `checkForPreview`, `uriToDir`, `cmdOpenPreview` — declared once, used consistently across tasks. `ExecuteCommandOptions` / `ExecuteCommandParams` introduced in Task 5 only, referenced in Task 5 only.
