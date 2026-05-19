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
