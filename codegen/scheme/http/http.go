// Package http registers the http:// and https:// import schemes.
package http

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	nethttp "net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"git.duckfam.us/jonathan/sngl/codegen"
)

func init() {
	codegen.RegisterFSScheme(&Importer{scheme: "http"})
	codegen.RegisterFSScheme(&Importer{scheme: "https"})
}

// Importer resolves http:// and https:// scheme imports by fetching a
// remote archive (tar.gz, tgz, or zip) and extracting it into the shared
// SNGL cache directory. The URI format is:
//
//	http(s)://host/path/archive.tar.gz#hash
//
// where hash is a sha256 of the compressed archive bytes for integrity
// verification. Use "#-" to skip verification.
//
// This is a deliberately narrow implementation: direct archive URLs only.
// Browsing WebDAV / apache / nginx index pages is not supported — write a
// tarball and serve it statically.
type Importer struct {
	scheme string // "http" or "https"
}

func (h *Importer) Scheme() string { return h.scheme }

func (h *Importer) ResolveFS(uri, _ string) (fs.FS, error) {
	rawURL := h.scheme + ":" + uri
	hash, cleanURL := splitHashFragment(rawURL)
	kind, err := archiveKind(cleanURL)
	if err != nil {
		return nil, err
	}

	cacheDir := httpCacheDir(cleanURL)

	if info, err := os.Stat(cacheDir); err == nil && info.IsDir() {
		// No hash re-verify on cache hit — verifyHash walks only .sngl files,
		// but the archive may contain non-.sngl asset files. The hash is
		// computed over the compressed archive bytes, checked at fetch time.
		return os.DirFS(cacheDir), nil
	}

	data, err := httpGetBytes(cleanURL)
	if err != nil {
		return nil, err
	}
	if hash != "-" {
		got := fmt.Sprintf("%x", sha256.Sum256(data))
		if got != hash {
			return nil, fmt.Errorf("http archive hash mismatch: expected %s, got %s", hash, got)
		}
	}

	if err := os.MkdirAll(filepath.Dir(cacheDir), 0o755); err != nil {
		return nil, err
	}
	tmpDir, err := os.MkdirTemp(filepath.Dir(cacheDir), ".http-extract-*")
	if err != nil {
		return nil, err
	}
	switch kind {
	case "tar.gz":
		err = extractTarGz(data, tmpDir)
	case "zip":
		err = extractZip(data, tmpDir)
	}
	if err != nil {
		os.RemoveAll(tmpDir)
		return nil, fmt.Errorf("extract %s: %w", kind, err)
	}
	// Many archives wrap content in a single top-level dir — if so, promote it
	// so .sngl files sit at the root of the returned FS.
	if root, ok := singleTopLevelDir(tmpDir); ok {
		tmpDir = root
	}
	if err := os.Rename(tmpDir, cacheDir); err != nil {
		// Rename may fail if another process populated cacheDir concurrently;
		// if so just use whatever is there.
		if _, statErr := os.Stat(cacheDir); statErr != nil {
			return nil, fmt.Errorf("install cache dir: %w", err)
		}
	}
	return os.DirFS(cacheDir), nil
}

// Refresh clears the cached extraction for the URL, re-fetches the archive,
// and returns the sha256 of the compressed archive bytes.
func (h *Importer) Refresh(uri, _ string) (string, error) {
	rawURL := h.scheme + ":" + uri
	_, cleanURL := splitHashFragment(rawURL)
	if _, err := archiveKind(cleanURL); err != nil {
		return "", err
	}
	cacheDir := httpCacheDir(cleanURL)
	if err := os.RemoveAll(cacheDir); err != nil {
		return "", fmt.Errorf("clear cache: %w", err)
	}
	data, err := httpGetBytes(cleanURL)
	if err != nil {
		return "", err
	}
	// Extract into the now-empty cache path so a subsequent ResolveFS is a hit.
	if err := os.MkdirAll(filepath.Dir(cacheDir), 0o755); err != nil {
		return "", err
	}
	tmp, err := os.MkdirTemp(filepath.Dir(cacheDir), ".http-extract-*")
	if err != nil {
		return "", err
	}
	kind, _ := archiveKind(cleanURL)
	switch kind {
	case "tar.gz":
		err = extractTarGz(data, tmp)
	case "zip":
		err = extractZip(data, tmp)
	}
	if err != nil {
		os.RemoveAll(tmp)
		return "", fmt.Errorf("extract %s: %w", kind, err)
	}
	if root, ok := singleTopLevelDir(tmp); ok {
		tmp = root
	}
	if err := os.Rename(tmp, cacheDir); err != nil {
		return "", fmt.Errorf("install cache dir: %w", err)
	}
	return fmt.Sprintf("%x", sha256.Sum256(data)), nil
}

// splitHashFragment peels off "#hash" from the URL, returning the bare hash
// (or "-" if absent) and the URL without the fragment.
func splitHashFragment(rawURL string) (hash, clean string) {
	hash = "-"
	if i := strings.LastIndex(rawURL, "#"); i >= 0 {
		hash = rawURL[i+1:]
		rawURL = rawURL[:i]
	}
	return hash, rawURL
}

func archiveKind(rawURL string) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("invalid URL %q: %w", rawURL, err)
	}
	p := strings.ToLower(u.Path)
	switch {
	case strings.HasSuffix(p, ".tar.gz"), strings.HasSuffix(p, ".tgz"):
		return "tar.gz", nil
	case strings.HasSuffix(p, ".zip"):
		return "zip", nil
	}
	return "", fmt.Errorf("unsupported archive extension in %q (want .tar.gz, .tgz, or .zip)", rawURL)
}

func httpCacheDir(cleanURL string) string {
	key := fmt.Sprintf("%x", sha256.Sum256([]byte(cleanURL)))
	u, err := url.Parse(cleanURL)
	host := "unknown"
	if err == nil {
		host = u.Host
	}
	return filepath.Join(codegen.SnglCacheDir(), "http", host, key)
}

func httpGetBytes(rawURL string) ([]byte, error) {
	slog.Info("exec", "cmd", "http GET", "url", rawURL)
	resp, err := nethttp.Get(rawURL)
	if err != nil {
		return nil, fmt.Errorf("http get %q: %w", rawURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != nethttp.StatusOK {
		return nil, fmt.Errorf("http get %q: status %d", rawURL, resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}

func extractTarGz(data []byte, destDir string) error {
	gzr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return err
	}
	defer gzr.Close()
	tr := tar.NewReader(gzr)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if err := extractOne(destDir, hdr.Name, hdr.Typeflag == tar.TypeDir, tr); err != nil {
			return err
		}
	}
}

func extractZip(data []byte, destDir string) error {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return err
	}
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			return err
		}
		err = extractOne(destDir, f.Name, f.FileInfo().IsDir(), rc)
		rc.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

// extractOne writes a single archive entry under destDir, rejecting paths that
// escape the destination via absolute paths or ".." traversal.
func extractOne(destDir, name string, isDir bool, r io.Reader) error {
	clean := filepath.Clean(name)
	if filepath.IsAbs(clean) || strings.HasPrefix(clean, "..") {
		return fmt.Errorf("unsafe archive path: %q", name)
	}
	dest := filepath.Join(destDir, clean)
	if isDir {
		return os.MkdirAll(dest, 0o755)
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	f, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(f, r)
	return err
}

// singleTopLevelDir returns the path of the single top-level directory inside
// dir, or (dir, false) if there are multiple entries or the single entry is a
// file.
func singleTopLevelDir(dir string) (string, bool) {
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 || !entries[0].IsDir() {
		return dir, false
	}
	return filepath.Join(dir, entries[0].Name()), true
}
