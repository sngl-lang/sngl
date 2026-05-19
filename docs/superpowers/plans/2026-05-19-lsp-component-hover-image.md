# LSP Component Hover Image Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Hovering a component identifier in an editor shows the pre-rendered example PNG inline in the hover float, sourced from `<sourceDir>/snapshots/example_<Name>_html.png` and served by an embedded HTTP server in `sngl lsp`.

**Architecture:** `sngl lsp` boots a local HTTP server bound to `127.0.0.1:0` on `initialize` and advertises the port via a custom `sngl/previewReady` notification. When the hover handler resolves an identifier to a `ComponentDecl`, it looks for `snapshots/example_<Name>_html.png` next to the source file; on hit it registers the absolute PNG path in the server's asset registry under a sha256-based URL and embeds `![Foo](http://127.0.0.1:N/preview/<sha>.png)` in the hover markdown. Lookup miss is silent — the hover degrades to the current text-only form.

**Tech Stack:** Go, `net/http`, existing `internal/lsp` and `internal/lspcore` packages.

**Spec:** `docs/superpowers/specs/2026-05-18-lsp-previews-design.md` §F1.

## Scope decisions (out of this slice)

| Spec piece | Status | Reason |
|---|---|---|
| Session token / `Authorization: Bearer` | deferred | Server binds to 127.0.0.1 only; non-loopback origins cannot reach it. Add when other localhost software becomes an attack surface. |
| Multi-platform fallback (bubbletea, fyne, android PNG) | deferred | Spec calls out `_html` as the primary; pick that and stop. |
| `.sngl/previews/<rel>/<Name>.png` repo-root layout | dropped | Existing `<dir>/snapshots/example_<Name>_<platform>.png` convention already works and is consumed by docsgen — reuse it. |
| `sngl snapshot --components` flag | dropped | `snapshot` already produces these via the `example_` convention. |

---

## File Structure

| File | Status | Responsibility |
|---|---|---|
| `internal/lspcore/hover.go` | modify | Extend `HoverAt` signature with `HoverOptions{ComponentImageURL}`, thread through `formatComponentHoverWithDoc` |
| `internal/lsp/preview.go` | create | Embedded HTTP server, asset registry, start/stop lifecycle |
| `internal/lsp/server.go` | modify | Start preview server on initialize, stop on shutdown; expose getter for asset registry |
| `internal/lsp/handler.go` | modify | After initialize result, send `sngl/previewReady` notification with the port |
| `internal/lsp/hover.go` | modify | Look up `snapshots/example_<Name>_html.png` near source URI; register asset; pass URL into `HoverOptions` |
| `internal/lspcore/hover_fixture_test.go` | modify (no behavior change) | The existing harness calls `HoverAt(content, doc, line, col)` — update to pass empty `HoverOptions{}` |
| `testdata/lsp_hover/component_image.sngl` | create | Fixture with a hover image directive |
| `testdata/lsp_hover/snapshots/example_Counter_html.png` | create | Minimal PNG checked in as fixture asset |
| `internal/lsp/preview_test.go` | create | Tests for server lifecycle, sha→path lookup, 404 on unknown sha |

---

## Task 1: Extend `HoverAt` with `HoverOptions`

Today `lspcore.HoverAt(content, doc, line, col)` returns hover markdown. To inject component preview images, the LSP-side handler needs to plumb a per-component URL lookup down to the formatter without baking policy ("where do PNGs live") into `lspcore`.

**Files:**
- Modify: `internal/lspcore/hover.go`
- Modify: `internal/lspcore/hover_fixture_test.go`
- Modify: `internal/lsp/hover.go`

- [ ] **Step 1: Add the `HoverOptions` type**

Append to `internal/lspcore/hover.go`:

```go
// HoverOptions configures optional behaviors of HoverAt. Zero value is fine —
// callers that don't need any of these features pass HoverOptions{}.
type HoverOptions struct {
	// ComponentImageURL, if non-nil, is invoked when the hover formatter
	// renders a ComponentDecl. Returning ok=true causes the formatter to
	// append a markdown image embed (`![<Name>](url)`) below the signature
	// and doc text.
	ComponentImageURL func(componentName string) (url string, ok bool)
}
```

- [ ] **Step 2: Update `HoverAt` signature**

In `internal/lspcore/hover.go`, find `func HoverAt(content string, doc *ast.Document, line, col int) string {` and change it to:

```go
func HoverAt(content string, doc *ast.Document, line, col int, opts HoverOptions) string {
	if doc != nil {
		if info := hoverLiteralAt(doc, line, col); info != "" {
			return info
		}
	}
	return hoverWord(content, doc, line, col, opts)
}
```

The internals previously delegated to `Hover(content, doc, line, col)`. Replace that delegation with a new helper `hoverWord` that takes opts. Define `hoverWord` next to the existing `Hover`:

```go
// hoverWord is the position→identifier→markdown path used by HoverAt.
// Hover() (no options) remains as a back-compat shim for callers that
// don't care about per-component image embedding.
func hoverWord(content string, doc *ast.Document, line, col int, opts HoverOptions) string {
	if doc == nil {
		return ""
	}
	word := WordAtPosition(content, line, col)
	if word == "" {
		return ""
	}
	return hoverInfoWithOptions(doc, word, opts)
}
```

Then refactor the existing `HoverInfo` to thread opts:

```go
func HoverInfo(doc *ast.Document, word string) string {
	return hoverInfoWithOptions(doc, word, HoverOptions{})
}

func hoverInfoWithOptions(doc *ast.Document, word string, opts HoverOptions) string {
	if info := hoverInStmtsWithOptions(doc.Stmts, doc, word, opts); info != "" {
		return info
	}
	if desc, ok := keywordDocs[word]; ok {
		return fmt.Sprintf("```sngl\n%s\n```\n\n%s\n", word, desc)
	}
	return ""
}
```

Rename `hoverInStmts(stmts, doc, word)` → `hoverInStmtsWithOptions(stmts, doc, word, opts)` and pass `opts` through. The single recursive call inside (for nested component bodies) also updates to pass opts. The only call site of the formatter that consumes opts today is the component path; everything else ignores opts.

- [ ] **Step 3: Thread opts into the component formatter**

Find the `*ast.ComponentDecl` case in the switch:

```go
case *ast.ComponentDecl:
    if s.Name == word {
        return formatComponentHoverWithDoc(s, doc)
    }
    if info := hoverInStmtsWithOptions(s.Body.Stmts, doc, word, opts); info != "" {
        return info
    }
```

Update the formatter signature:

```go
func formatComponentHoverWithDoc(c *ast.ComponentDecl, doc *ast.Document, opts HoverOptions) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "```sngl\ncomponent %s\n```\n", c.Name)
	if doc != nil {
		if d := docForPos(doc, c.Pos); d != "" {
			fmt.Fprintf(&sb, "\n%s\n", d)
		}
	}
	var params []ast.Param
	for _, p := range c.Props.Props {
		if param, ok := p.(ast.Param); ok {
			params = append(params, param)
		}
	}
	if len(params) > 0 {
		sb.WriteString("\n**Params:**\n")
		for _, p := range params {
			hint := typeExprString(p.Type)
			if hint == "" {
				hint = "dyn"
			}
			if p.Default == nil {
				fmt.Fprintf(&sb, "- `%s` %s (required)\n", p.Name, hint)
			} else {
				fmt.Fprintf(&sb, "- `%s` %s\n", p.Name, hint)
			}
		}
	}
	if opts.ComponentImageURL != nil {
		if url, ok := opts.ComponentImageURL(c.Name); ok {
			fmt.Fprintf(&sb, "\n![%s](%s)\n", c.Name, url)
		}
	}
	return sb.String()
}
```

The new signature requires updating the call site (Step 2's `return formatComponentHoverWithDoc(s, doc)` becomes `return formatComponentHoverWithDoc(s, doc, opts)`).

- [ ] **Step 4: Update LSP hover handler call site**

Edit `internal/lsp/hover.go`. The existing line:

```go
info := lspcore.HoverAt(fs.Content, fs.Doc, line, col)
```

becomes:

```go
info := lspcore.HoverAt(fs.Content, fs.Doc, line, col, lspcore.HoverOptions{})
```

(Image-URL wiring lands in Task 5; for now keep the options empty.)

- [ ] **Step 5: Update fixture harness**

Edit `internal/lspcore/hover_fixture_test.go`. The call:

```go
got := HoverAt(string(src), doc, d.Line, d.Col)
```

becomes:

```go
got := HoverAt(string(src), doc, d.Line, d.Col, HoverOptions{})
```

- [ ] **Step 6: Build and run all tests**

Run: `go test ./internal/lsp/... ./internal/lspcore/... -count=1`
Expected: PASS. No behavior change yet — just signature plumbing.

- [ ] **Step 7: Commit**

```bash
git add internal/lspcore/hover.go internal/lspcore/hover_fixture_test.go internal/lsp/hover.go
git commit -m "lspcore(hover): add HoverOptions with component image URL hook"
```

---

## Task 2: HTTP preview server skeleton

**Files:**
- Create: `internal/lsp/preview.go`
- Create: `internal/lsp/preview_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/lsp/preview_test.go`:

```go
package lsp

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

func TestPreviewServer_Lifecycle(t *testing.T) {
	srv := newPreviewServer()
	if err := srv.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer srv.Stop()
	if srv.Port() <= 0 {
		t.Fatalf("port = %d", srv.Port())
	}
}

func TestPreviewServer_ServesRegisteredAsset(t *testing.T) {
	dir := t.TempDir()
	pngPath := filepath.Join(dir, "foo.png")
	want := []byte("\x89PNG\r\n\x1a\nfake-png-bytes")
	if err := os.WriteFile(pngPath, want, 0o644); err != nil {
		t.Fatal(err)
	}

	srv := newPreviewServer()
	if err := srv.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer srv.Stop()

	url := srv.RegisterAsset(pngPath)
	// url is "http://127.0.0.1:N/preview/<sha>.png"
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	got, _ := io.ReadAll(resp.Body)
	if string(got) != string(want) {
		t.Fatalf("body mismatch")
	}
}

func TestPreviewServer_404OnUnknownSha(t *testing.T) {
	srv := newPreviewServer()
	if err := srv.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer srv.Stop()

	bogus := sha256.Sum256([]byte("not-registered"))
	url := "http://127.0.0.1:" + itoa(srv.Port()) + "/preview/" + hex.EncodeToString(bogus[:]) + ".png"
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 404 {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}

func TestPreviewServer_RegisterAssetIdempotent(t *testing.T) {
	srv := newPreviewServer()
	if err := srv.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer srv.Stop()

	dir := t.TempDir()
	pngPath := filepath.Join(dir, "foo.png")
	os.WriteFile(pngPath, []byte("x"), 0o644)

	a := srv.RegisterAsset(pngPath)
	b := srv.RegisterAsset(pngPath)
	if a != b {
		t.Errorf("non-idempotent: %s != %s", a, b)
	}
}

// itoa is a tiny strconv.Itoa stand-in to keep the test file dep-light.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := false
	if n < 0 {
		neg = true
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
```

- [ ] **Step 2: Run, verify failure**

Run: `go test ./internal/lsp/ -run TestPreviewServer -v`
Expected: FAIL with "undefined: newPreviewServer".

- [ ] **Step 3: Implement the server**

Create `internal/lsp/preview.go`:

```go
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
```

- [ ] **Step 4: Run, verify pass**

Run: `go test ./internal/lsp/ -run TestPreviewServer -v`
Expected: all four subtests PASS.

- [ ] **Step 5: Full regression**

Run: `go test ./internal/lsp/... ./internal/lspcore/... -count=1`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/lsp/preview.go internal/lsp/preview_test.go
git commit -m "lsp(preview): embedded HTTP server with sha-keyed asset registry"
```

---

## Task 3: Server lifecycle — start on initialize, stop on shutdown

**Files:**
- Modify: `internal/lsp/server.go`
- Modify: `internal/lsp/handler.go`

- [ ] **Step 1: Add preview server field to `*Server`**

In `internal/lsp/server.go`, find the `type Server struct {` block. Add a field:

```go
type Server struct {
	ws      *workspace
	reader  *bufio.Reader
	writer  io.Writer
	mu      sync.Mutex
	log     *log.Logger
	preview *previewServer
}
```

(Preserve other existing fields.)

Update `New()` to initialize it:

```go
func New() *Server {
	return &Server{
		ws:      newWorkspace(),
		log:     log.New(os.Stderr, "[sngl-lsp] ", log.LstdFlags),
		preview: newPreviewServer(),
	}
}
```

- [ ] **Step 2: Start the server in `handleInitialize`**

Edit `internal/lsp/handler.go`. At the top of `handleInitialize`, before constructing `InitializeResult`, start the preview server:

```go
func (s *Server) handleInitialize(id json.RawMessage, params json.RawMessage) {
	if err := s.preview.Start(); err != nil {
		s.log.Printf("preview server: %v", err)
		// Continue without preview — hover degrades silently
	}

	result := InitializeResult{ // ...existing...
```

(Don't change the result body; that's Task 4.)

- [ ] **Step 3: Stop the server in `shutdown`/`exit`**

In `internal/lsp/server.go`, find the `case "shutdown":` line:

```go
case "shutdown":
    s.sendResult(req.ID, nil)
case "exit":
    return nil
```

Change to:

```go
case "shutdown":
    s.preview.Stop()
    s.sendResult(req.ID, nil)
case "exit":
    s.preview.Stop()
    return nil
```

Also handle the EOF/IO-error path. Find the `return fmt.Errorf("read: %w", err)` after the `readMessage` failure:

```go
if err != nil {
    if err == io.EOF {
        s.preview.Stop()
        return nil
    }
    s.preview.Stop()
    return fmt.Errorf("read: %w", err)
}
```

- [ ] **Step 4: Build and test**

Run: `go test ./internal/lsp/... -count=1`
Expected: PASS. The existing `TestE2EDocumentColor` exercises the full lifecycle and should not regress.

If the e2e test now hangs or leaks ports, `Stop()` isn't being called somewhere — diff the test's setup/teardown against the new shutdown path.

- [ ] **Step 5: Commit**

```bash
git add internal/lsp/server.go internal/lsp/handler.go
git commit -m "lsp(preview): start/stop preview server with LSP lifecycle"
```

---

## Task 4: Port advertisement via `sngl/previewReady` notification

**Files:**
- Modify: `internal/lsp/handler.go`

- [ ] **Step 1: Send notification at end of `handleInitialize`**

In `internal/lsp/handler.go`, at the end of `handleInitialize` (after `s.sendResult(id, result)`), add:

```go
	s.sendResult(id, result)
	if port := s.preview.Port(); port > 0 {
		s.notify("sngl/previewReady", map[string]any{
			"port": port,
			"url":  fmt.Sprintf("http://127.0.0.1:%d", port),
		})
	}
}
```

Add `"fmt"` to the import block at the top of `internal/lsp/handler.go` if not already present.

- [ ] **Step 2: Test the notification fires**

Append to `internal/lsp/preview_test.go`:

```go
func TestServer_NotifiesPreviewReady(t *testing.T) {
	// Build a minimal in-process server, send initialize, capture notifications.
	r, w := io.Pipe()
	clientR, clientW := io.Pipe()
	srv := New()
	srv.reader = bufio.NewReader(r)
	srv.writer = clientW

	go func() {
		_ = srv.serve()
	}()
	defer srv.preview.Stop()

	// Send initialize request.
	body := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`
	header := fmt.Sprintf("Content-Length: %d\r\n\r\n", len(body))
	go func() {
		w.Write([]byte(header + body))
	}()

	// Read messages until we see sngl/previewReady or timeout.
	got := readMessagesUntil(t, clientR, "sngl/previewReady", 2*time.Second)
	if got == "" {
		t.Fatal("did not receive sngl/previewReady within timeout")
	}
	if !strings.Contains(got, `"port"`) {
		t.Errorf("notification missing port field: %s", got)
	}
}

// readMessagesUntil scans LSP-framed messages from r looking for one whose
// JSON body contains `method`. Returns the matching body or "" on timeout.
func readMessagesUntil(t *testing.T, r io.Reader, method string, timeout time.Duration) string {
	t.Helper()
	type result struct {
		body string
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		br := bufio.NewReader(r)
		for {
			line, err := br.ReadString('\n')
			if err != nil {
				ch <- result{err: err}
				return
			}
			line = strings.TrimSpace(line)
			if !strings.HasPrefix(line, "Content-Length:") {
				continue
			}
			n, _ := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(line, "Content-Length:")))
			// Skip blank line
			br.ReadString('\n')
			body := make([]byte, n)
			io.ReadFull(br, body)
			if strings.Contains(string(body), `"method":"`+method+`"`) {
				ch <- result{body: string(body)}
				return
			}
		}
	}()
	select {
	case res := <-ch:
		return res.body
	case <-time.After(timeout):
		return ""
	}
}
```

Add the imports at the top of `internal/lsp/preview_test.go`:

```go
import (
    "bufio"
    "crypto/sha256"
    "encoding/hex"
    "fmt"
    "io"
    "net/http"
    "os"
    "path/filepath"
    "strconv"
    "strings"
    "testing"
    "time"
)
```

- [ ] **Step 3: Run new test**

Run: `go test ./internal/lsp/ -run TestServer_NotifiesPreviewReady -v`
Expected: PASS within 2 seconds.

If the test hangs, check that `s.serve()` is actually running on the spawned goroutine. The pipe-based fake transport sometimes needs an explicit close at the end of the test — but the 2-second timeout will catch silent hangs.

- [ ] **Step 4: Full regression**

Run: `go test ./internal/lsp/... ./internal/lspcore/... -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/lsp/handler.go internal/lsp/preview_test.go
git commit -m "lsp(preview): emit sngl/previewReady notification with port"
```

---

## Task 5: Hover handler integration — discover snapshot, build URL

**Files:**
- Modify: `internal/lsp/hover.go`

- [ ] **Step 1: Write the snapshot-lookup helper**

Add to `internal/lsp/hover.go`:

```go
// componentSnapshotPath returns the absolute filesystem path to
// snapshots/example_<Name>_html.png relative to the source .sngl file,
// if it exists.
func componentSnapshotPath(sourceURI, componentName string) (string, bool) {
	srcPath := uriToPath(sourceURI)
	if srcPath == "" {
		return "", false
	}
	dir := filepath.Dir(srcPath)
	candidate := filepath.Join(dir, "snapshots", "example_"+componentName+"_html.png")
	info, err := os.Stat(candidate)
	if err != nil || info.IsDir() {
		return "", false
	}
	return candidate, true
}

// uriToPath converts a file:// URI to a filesystem path. Returns "" for
// non-file URIs.
func uriToPath(uri string) string {
	const prefix = "file://"
	if !strings.HasPrefix(uri, prefix) {
		return ""
	}
	return uri[len(prefix):]
}
```

Add `"os"`, `"path/filepath"`, `"strings"` to the imports at the top of `internal/lsp/hover.go` if absent.

- [ ] **Step 2: Wire the lookup into HoverOptions**

Replace the existing handler body (the Task 1 version uses `HoverOptions{}`):

```go
func (s *Server) handleHover(id json.RawMessage, params json.RawMessage) {
	var p HoverParams
	if err := json.Unmarshal(params, &p); err != nil {
		s.sendError(id, -32602, "invalid params")
		return
	}

	fs := s.ws.get(p.TextDocument.URI)
	if fs == nil || fs.Doc == nil {
		s.sendResult(id, nil)
		return
	}

	line := p.Position.Line + 1
	col := p.Position.Character + 1
	opts := lspcore.HoverOptions{
		ComponentImageURL: func(name string) (string, bool) {
			path, ok := componentSnapshotPath(p.TextDocument.URI, name)
			if !ok {
				return "", false
			}
			return s.preview.RegisterAsset(path), true
		},
	}
	info := lspcore.HoverAt(fs.Content, fs.Doc, line, col, opts)
	if info == "" {
		s.sendResult(id, nil)
		return
	}

	s.sendResult(id, Hover{
		Contents: MarkupContent{
			Kind:  "markdown",
			Value: info,
		},
	})
}
```

- [ ] **Step 3: Build and run all tests**

Run: `go test ./internal/lsp/... ./internal/lspcore/... -count=1`
Expected: PASS. No fixture exists yet for the image path; existing tests pass since their fixtures don't have matching snapshot files.

- [ ] **Step 4: Commit**

```bash
git add internal/lsp/hover.go
git commit -m "lsp(hover): lookup snapshots/example_<Name>_html.png and embed in component hover"
```

---

## Task 6: Fixture covering the image embed

The existing `testdata/lsp_hover/component.sngl` fixture doesn't have a matching PNG. Add a sibling fixture that does, plus the PNG itself.

**Files:**
- Create: `testdata/lsp_hover/component_image.sngl`
- Create: `testdata/lsp_hover/snapshots/example_Counter_html.png`
- Modify: `internal/lspcore/hover_fixture_test.go`

- [ ] **Step 1: Create a minimal PNG fixture**

Run the following to write a 1×1 transparent PNG (smallest valid PNG, 67 bytes):

```bash
mkdir -p testdata/lsp_hover/snapshots
printf '\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x06\x00\x00\x00\x1f\x15\xc4\x89\x00\x00\x00\rIDATx\x9cb\x00\x01\x00\x00\x05\x00\x01\r\n-\xb4\x00\x00\x00\x00IEND\xaeB`\x82' > testdata/lsp_hover/snapshots/example_Counter_html.png
```

Verify size: `wc -c testdata/lsp_hover/snapshots/example_Counter_html.png` should report ~67 bytes.

- [ ] **Step 2: Create the fixture**

Create `testdata/lsp_hover/component_image.sngl`:

```sngl
// HOVER(Counter) "component Counter"
// HOVER(Counter) "![Counter]("
// HOVER(Counter) ".png)"
component Counter(label string) { vbox {} }
component example_Counter { Counter(label="3 clicks") }
```

The three HOVER assertions verify (1) baseline component hover still works, (2) image markdown is emitted with the component name as alt, (3) the URL points to a `.png` resource.

- [ ] **Step 3: Update fixture harness to pass HoverOptions with the lookup**

Edit `internal/lspcore/hover_fixture_test.go`. The harness currently calls `HoverAt(string(src), doc, d.Line, d.Col, HoverOptions{})` (post-Task-1). It needs to supply a `ComponentImageURL` that mimics the LSP's lookup — namely, check `<fixture-dir>/snapshots/example_<Name>_html.png`.

Change the inner harness loop to compute opts per fixture:

```go
fixtureDir := filepath.Dir(path)
opts := HoverOptions{
    ComponentImageURL: func(name string) (string, bool) {
        candidate := filepath.Join(fixtureDir, "snapshots", "example_"+name+"_html.png")
        info, err := os.Stat(candidate)
        if err != nil || info.IsDir() {
            return "", false
        }
        // Fixture harness uses file:// URLs — the LSP wraps real ones in
        // http://127.0.0.1:N/preview/<sha>.png, but the substring asserts
        // only check for "![", "](", ".png)" so either format works.
        return "file://" + candidate, true
    },
}
for _, d := range dirs {
    got := HoverAt(string(src), doc, d.Line, d.Col, opts)
    // ...rest unchanged...
}
```

The `os` and `filepath` imports already exist in the harness file from Task 4 (`os.ReadFile`, `filepath.Glob`). No new imports needed.

- [ ] **Step 4: Run the new fixture**

Run: `go test ./internal/lspcore/ -run TestHoverFixtures/component_image -v`
Expected: PASS — all three HOVER substrings present in the output.

- [ ] **Step 5: Full regression**

Run: `go test ./internal/lsp/... ./internal/lspcore/... -count=1`
Expected: PASS — including the existing `testdata/lsp_hover/component.sngl` fixture (no snapshot file exists for that Counter, so image is silently omitted).

If `TestLSPFixturesTypeCheck` complains about the new fixture, the `example_Counter` component may not check cleanly. Adjust the fixture (e.g. `component example_Counter { vbox { Counter(label="3 clicks") } }`) until check passes — verify with `sngl check testdata/lsp_hover/component_image.sngl`.

- [ ] **Step 6: Commit**

```bash
git add testdata/lsp_hover/component_image.sngl testdata/lsp_hover/snapshots/example_Counter_html.png internal/lspcore/hover_fixture_test.go
git commit -m "lspcore(hover): fixture verifies component preview image embed"
```

---

## Task 7: Rebuild + smoke

**Files:** none

- [ ] **Step 1: Rebuild**

Run: `go install ./cmd/sngl`
Expected: success.

- [ ] **Step 2: Smoke check**

Open a `.sngl` file in nvim that defines a component (e.g. `component Foo { vbox {} }`) and has a matching `snapshots/example_Foo_html.png` on disk. `:LspRestart`, then hover the `Foo` identifier.

In a terminal that supports inline images (kitty / wezterm + image.nvim or snacks.image) the PNG renders in the hover float. Otherwise the hover shows `![Foo](http://127.0.0.1:PORT/preview/<sha>.png)` as text — confirm by Ctrl+clicking or copying the URL into a browser; expect to see the snapshot PNG.

To verify the notification arrives: run `nvim --log-level debug` and grep stderr for `sngl/previewReady`.

---

## Self-Review

**Spec coverage (§F1):**

| Spec row | Task |
|---|---|
| PNG via custom LSP route | Task 2 (HTTP server + `/preview/<sha>.png`) |
| Source: `snapshots/example_<Name>_html.png` near the .sngl file | Task 5 (`componentSnapshotPath`) |
| Embed `![<Name>](url)` in markdown hover | Task 1 (`formatComponentHoverWithDoc` image block) |
| Lookup miss is silent | Task 5 (callback returns `ok=false`) |
| Port advertisement `sngl/previewReady` | Task 4 |
| Bind 127.0.0.1 only | Task 2 (`net.Listen("tcp", "127.0.0.1:0")`) |
| Cache-Control header | Task 2 (`max-age=604800, immutable`) |
| Session token | **DEFERRED** — see scope decisions block at top |

**Placeholder scan:** none. Every code step has full code.

**Type consistency:** `previewServer`, `newPreviewServer`, `Start`, `Stop`, `Port`, `RegisterAsset`, `HoverOptions`, `ComponentImageURL`, `componentSnapshotPath`, `uriToPath` defined once and referenced consistently. `HoverAt` signature changes in Task 1 — every call site (LSP handler, fixture harness) is updated in Task 1 or Task 6.
