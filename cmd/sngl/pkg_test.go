package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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
	tw.Close()
	gzw.Close()
	return buf.Bytes()
}

func TestPkgDownloadPopulatesCache(t *testing.T) {
	archive := buildTarGz(t, map[string]string{
		"pkg/widgets.sngl": `component Counter(label = "") ui { }`,
	})
	expectHash := fmt.Sprintf("%x", sha256.Sum256(archive))

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write(archive)
	}))
	defer srv.Close()

	cacheRoot := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cacheRoot)

	workDir := t.TempDir()
	host := strings.TrimPrefix(srv.URL, "http://")
	mainSrc := fmt.Sprintf(`import "widgets" => "http://%s/widgets.tar.gz#%s"

component main ui {
    widgets.Counter(label="x")
}
`, host, expectHash)
	if err := os.WriteFile(filepath.Join(workDir, "main.sngl"), []byte(mainSrc), 0o644); err != nil {
		t.Fatalf("write main: %v", err)
	}

	oldDir, _ := os.Getwd()
	t.Cleanup(func() { os.Chdir(oldDir) })
	if err := os.Chdir(workDir); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	if err := runPkgDownload(pkgDownloadCmd, []string{"."}); err != nil {
		t.Fatalf("runPkgDownload: %v", err)
	}

	httpCache := filepath.Join(cacheRoot, "sngl", "http")
	entries, err := os.ReadDir(httpCache)
	if err != nil {
		t.Fatalf("read http cache: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("expected http cache to contain at least one entry")
	}
}

func TestPkgCacheClearRemovesDir(t *testing.T) {
	cacheRoot := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cacheRoot)

	snglDir := filepath.Join(cacheRoot, "sngl")
	if err := os.MkdirAll(filepath.Join(snglDir, "git", "example.com"), 0o755); err != nil {
		t.Fatalf("prepopulate cache: %v", err)
	}

	if err := pkgCacheClearCmd.Flags().Set("yes", "true"); err != nil {
		t.Fatalf("set --yes: %v", err)
	}
	t.Cleanup(func() { pkgCacheClearCmd.Flags().Set("yes", "false") })

	if err := runPkgCacheClear(pkgCacheClearCmd, nil); err != nil {
		t.Fatalf("runPkgCacheClear: %v", err)
	}
	if _, err := os.Stat(snglDir); !os.IsNotExist(err) {
		t.Errorf("expected %s removed, stat err = %v", snglDir, err)
	}
}
