package lsp

import (
	"bufio"
	"crypto/sha256"
	"encoding/base64"
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

	"golang.org/x/net/websocket"
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

func TestPreviewServer_RendersWindow(t *testing.T) {
	srv := newPreviewServer()
	srv.SetRenderer(func(uri, win string) ([]byte, error) {
		return fmt.Appendf(nil, "<html><body>%s/%s</body></html>", uri, win), nil
	})
	if err := srv.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer srv.Stop()

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
	for range 2 {
		line, _ := br.ReadString('\n')
		ln := 0
		fmt.Sscanf(strings.TrimSpace(line), "Content-Length: %d", &ln)
		br.ReadString('\n')
		buf := make([]byte, ln)
		io.ReadFull(br, buf)
	}

	// didOpen with a window
	go send(`{"jsonrpc":"2.0","method":"textDocument/didOpen","params":{"textDocument":{"uri":"file:///tmp/x.sngl","languageId":"sngl","version":1,"text":"import . \"sngl://std\"\nwindow #home(title=\"Home\", href=\"/\") {\n  text(value=\"hi\")\n}\n"}}}`)
	// Drain diagnostics notification.
	line, _ := br.ReadString('\n')
	ln := 0
	fmt.Sscanf(strings.TrimSpace(line), "Content-Length: %d", &ln)
	br.ReadString('\n')
	buf := make([]byte, ln)
	io.ReadFull(br, buf)

	// executeCommand sngl.openPreview
	go send(`{"jsonrpc":"2.0","id":2,"method":"workspace/executeCommand","params":{"command":"sngl.openPreview","arguments":[{"uri":"file:///tmp/x.sngl","position":{"line":1,"character":0}}]}}`)
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
