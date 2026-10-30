package android

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// tarGz is a gzipped tar of files, each with its mode.
func tarGz(t *testing.T, files map[string]int64, names ...string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, name := range names {
		body := "contents of " + name
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: files[name], Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		tw.Write([]byte(body))
	}
	tw.Close()
	gz.Close()
	return buf.Bytes()
}

func serve(t *testing.T, body []byte) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(body) }))
	t.Cleanup(srv.Close)
	return srv.URL + "/kotlinc-2.1.0-linux-amd64.tar.gz"
}

func sum(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

// TestFetchArchiveUnpacksExecutable keeps a binary's execute bit, which is
// what lets the build run it.
func TestFetchArchiveUnpacksExecutable(t *testing.T) {
	archive := tarGz(t, map[string]int64{"./bin/kotlinc": 0o755, "./lib/kotlin-stdlib.jar": 0o644}, "./bin/kotlinc", "./lib/kotlin-stdlib.jar")
	dest := filepath.Join(t.TempDir(), "kotlinc-2.1.0")
	if err := fetchArchive(serve(t, archive), sum(archive), dest); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(dest, "bin", "kotlinc"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o100 == 0 {
		t.Errorf("bin/kotlinc lost its execute bit: %v", info.Mode())
	}
	if !fileExists(filepath.Join(dest, "lib", "kotlin-stdlib.jar")) {
		t.Error("lib/kotlin-stdlib.jar was not unpacked")
	}
	if dirExists(dest+".part") || fileExists(dest+".download") {
		t.Error("staging files were left behind")
	}
}

// TestFetchArchiveRefusesWrongSum leaves nothing a later build would read as
// a complete download.
func TestFetchArchiveRefusesWrongSum(t *testing.T) {
	archive := tarGz(t, map[string]int64{"bin/d8": 0o755}, "bin/d8")
	dest := filepath.Join(t.TempDir(), "d8")
	err := fetchArchive(serve(t, archive), sum([]byte("something else")), dest)
	if err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("err = %v, want a checksum mismatch", err)
	}
	if dirExists(dest) {
		t.Error("a download that failed its checksum was unpacked")
	}
}

// TestExtractTarGzStaysInside refuses an entry naming a path outside the
// directory it unpacks into.
func TestExtractTarGzStaysInside(t *testing.T) {
	archive := tarGz(t, map[string]int64{"../escaped": 0o644}, "../escaped")
	path := filepath.Join(t.TempDir(), "a.tar.gz")
	if err := os.WriteFile(path, archive, 0o644); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(t.TempDir(), "out")
	if err := extractTarGz(path, dest); err == nil {
		t.Fatal("extractTarGz accepted ../escaped")
	}
	if fileExists(filepath.Join(filepath.Dir(dest), "escaped")) {
		t.Error("../escaped was written")
	}
}
