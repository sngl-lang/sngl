package http

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"fmt"
	"io/fs"
	nethttp "net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// buildTarGz returns a gzip-compressed tarball containing the given files.
// Keys are paths relative to the archive root, values are file contents.
func buildTarGz(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gzw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gzw)
	for name, content := range files {
		hdr := &tar.Header{Name: name, Mode: 0o644, Size: int64(len(content)), Typeflag: tar.TypeReg}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatalf("tar WriteHeader: %v", err)
		}
		if _, err := tw.Write([]byte(content)); err != nil {
			t.Fatalf("tar Write: %v", err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("tar Close: %v", err)
	}
	if err := gzw.Close(); err != nil {
		t.Fatalf("gzip Close: %v", err)
	}
	return buf.Bytes()
}

func TestHTTPImporterFetchesAndCaches(t *testing.T) {
	archive := buildTarGz(t, map[string]string{
		"pkg/widgets.sngl": `component Counter(label = "") { }`,
		"pkg/README.md":    "readme",
	})
	expectHash := fmt.Sprintf("%x", sha256.Sum256(archive))

	var hits int
	srv := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, _ *nethttp.Request) {
		hits++
		w.Header().Set("Content-Type", "application/gzip")
		w.Write(archive)
	}))
	defer srv.Close()

	// Redirect cache into a temp dir so the test is hermetic.
	tmp := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", tmp)
	t.Setenv("HOME", tmp) // fallback path if XDG_CACHE_HOME gets cleared elsewhere

	imp := &Importer{scheme: "http"}
	trimmed := strings.TrimPrefix(srv.URL, "http://")
	uri := trimmed + "/widgets.tar.gz#" + expectHash

	fsys, err := imp.ResolveFS(uri, "")
	if err != nil {
		t.Fatalf("first ResolveFS: %v", err)
	}
	// Single-top-level-dir promotion flattens pkg/ into the FS root.
	data, err := fs.ReadFile(fsys, "widgets.sngl")
	if err != nil {
		t.Fatalf("ReadFile widgets.sngl: %v", err)
	}
	if !strings.Contains(string(data), "Counter") {
		t.Errorf("archive content mismatch: %q", data)
	}

	// Second call should be a pure cache hit — no additional HTTP fetch.
	if _, err := imp.ResolveFS(uri, ""); err != nil {
		t.Fatalf("second ResolveFS: %v", err)
	}
	if hits != 1 {
		t.Errorf("expected 1 HTTP hit after cache warm, got %d", hits)
	}
	// Sanity: cache dir actually lives under XDG_CACHE_HOME/sngl/http.
	if _, err := os.Stat(tmp + "/sngl/http"); err != nil {
		t.Errorf("cache dir not created under XDG_CACHE_HOME: %v", err)
	}
}

func TestHTTPImporterHashMismatch(t *testing.T) {
	archive := buildTarGz(t, map[string]string{"pkg/a.sngl": ""})
	srv := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, _ *nethttp.Request) {
		w.Write(archive)
	}))
	defer srv.Close()
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	imp := &Importer{scheme: "http"}
	uri := strings.TrimPrefix(srv.URL, "http://") + "/a.tar.gz#deadbeef"
	_, err := imp.ResolveFS(uri, "")
	if err == nil || !strings.Contains(err.Error(), "hash mismatch") {
		t.Errorf("expected hash mismatch error, got %v", err)
	}
}

func TestHTTPImporterSkipsHashWithDash(t *testing.T) {
	archive := buildTarGz(t, map[string]string{"pkg/a.sngl": ""})
	srv := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, _ *nethttp.Request) {
		w.Write(archive)
	}))
	defer srv.Close()
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	imp := &Importer{scheme: "http"}
	uri := strings.TrimPrefix(srv.URL, "http://") + "/a.tar.gz#-"
	if _, err := imp.ResolveFS(uri, ""); err != nil {
		t.Fatalf("expected success with #-, got %v", err)
	}
}

func TestHTTPImporterRejectsNonArchive(t *testing.T) {
	imp := &Importer{scheme: "http"}
	_, err := imp.ResolveFS("example.com/not-an-archive.txt#-", "")
	if err == nil || !strings.Contains(err.Error(), "unsupported archive extension") {
		t.Errorf("expected unsupported-extension error, got %v", err)
	}
}
