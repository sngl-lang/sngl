package main

import (
	"compress/gzip"
	"fmt"
	"html"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// artifact is one cross-compiled, gzip-compressed sngl binary placed under
// <outDir>/downloads/.
type artifact struct {
	os, arch string // human-facing display values (e.g. "Linux", "amd64")
	filename string // basename under downloads/, e.g. "sngl-linux-amd64.gz"
	size     int64  // compressed size in bytes
}

const downloadsMarker = "<!-- sngl:downloads -->"

// humanSize renders a byte count as a one-decimal decimal-MB string, e.g.
// "26.2 MB" (1 MB = 1,000,000 bytes, matching how download UIs report sizes).
func humanSize(n int64) string {
	return fmt.Sprintf("%.1f MB", float64(n)/1_000_000)
}

// injectDownloads replaces the downloadsMarker in learn/installation.html with
// a table of the supplied artifacts. A missing marker is logged and ignored.
func injectDownloads(outDir string, arts []artifact) error {
	page := filepath.Join(outDir, "learn", "installation.html")
	data, err := os.ReadFile(page)
	if err != nil {
		return fmt.Errorf("reading installation.html: %w", err)
	}
	if !strings.Contains(string(data), downloadsMarker) {
		log.Printf("downloads: marker %q not found in installation.html; skipping", downloadsMarker)
		return nil
	}

	var b strings.Builder
	b.WriteString(`<table class="downloads"><thead><tr><th>Platform</th><th>Architecture</th><th>Download</th><th>Size</th></tr></thead><tbody>`)
	for _, a := range arts {
		name := html.EscapeString(a.filename)
		fmt.Fprintf(&b,
			`<tr><td>%s</td><td>%s</td><td><a href="/downloads/%s">%s</a></td><td>%s</td></tr>`,
			html.EscapeString(a.os), html.EscapeString(a.arch), name, name, humanSize(a.size),
		)
	}
	b.WriteString(`</tbody></table>`)

	out := strings.Replace(string(data), downloadsMarker, b.String(), 1)
	if err := os.WriteFile(page, []byte(out), 0o644); err != nil {
		return fmt.Errorf("writing installation.html: %w", err)
	}
	log.Printf("downloads: injected %d artifacts", len(arts))
	return nil
}

// resolveVersion derives build metadata from git, falling back to safe
// defaults so the build still succeeds outside a checkout.
func resolveVersion() (version, commit, date string) {
	version, commit, date = "dev", "none", time.Now().UTC().Format(time.RFC3339)
	if out, err := exec.Command("git", "describe", "--tags", "--always").Output(); err == nil {
		if s := strings.TrimSpace(string(out)); s != "" {
			version = s
		}
	}
	if out, err := exec.Command("git", "rev-parse", "--short", "HEAD").Output(); err == nil {
		if s := strings.TrimSpace(string(out)); s != "" {
			commit = s
		}
	}
	return version, commit, date
}

// target is one GOOS/GOARCH cross-build target.
type target struct{ goos, goarch string }

// targets is the set of platforms docsgen produces downloads for.
var targets = []target{
	{"linux", "amd64"},
	{"linux", "arm64"},
	{"darwin", "amd64"},
	{"darwin", "arm64"},
	{"windows", "amd64"},
}

// displayOS maps a GOOS to a human-facing platform name for the table.
func displayOS(goos string) string {
	switch goos {
	case "darwin":
		return "macOS"
	case "linux":
		return "Linux"
	case "windows":
		return "Windows"
	default:
		return goos
	}
}

// buildOne cross-compiles sngl for a single target with CGO disabled, stamps
// version metadata via ldflags, gzip-compresses the binary into
// <outDir>/downloads/, removes the uncompressed binary, and returns its
// artifact record.
func buildOne(repoRoot, outDir string, t target, version, commit, date string) (artifact, error) {
	dlDir := filepath.Join(outDir, "downloads")
	if err := os.MkdirAll(dlDir, 0o755); err != nil {
		return artifact{}, err
	}

	base := fmt.Sprintf("sngl-%s-%s", t.goos, t.goarch)
	binName := base
	if t.goos == "windows" {
		binName += ".exe"
	}
	tmpBin := filepath.Join(dlDir, binName)

	ldflags := fmt.Sprintf("-s -w -X main.version=%s -X main.commit=%s -X main.date=%s", version, commit, date)
	cmd := exec.Command("go", "build", "-o", tmpBin, "-ldflags", ldflags, "./cmd/sngl")
	cmd.Dir = repoRoot
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS="+t.goos, "GOARCH="+t.goarch)
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return artifact{}, fmt.Errorf("building %s/%s: %w", t.goos, t.goarch, err)
	}

	gzName := binName + ".gz"
	gzPath := filepath.Join(dlDir, gzName)
	if err := gzipFile(tmpBin, gzPath); err != nil {
		return artifact{}, fmt.Errorf("gzip %s: %w", binName, err)
	}
	if err := os.Remove(tmpBin); err != nil {
		return artifact{}, err
	}

	info, err := os.Stat(gzPath)
	if err != nil {
		return artifact{}, err
	}
	log.Printf("binaries: built %s (%s)", gzName, humanSize(info.Size()))
	return artifact{os: displayOS(t.goos), arch: t.goarch, filename: gzName, size: info.Size()}, nil
}

// gzipFile streams src through gzip into dst.
func gzipFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	zw := gzip.NewWriter(out)
	if _, err := io.Copy(zw, in); err != nil {
		zw.Close()
		return err
	}
	return zw.Close()
}

// buildBinaries cross-compiles sngl for every target and returns the
// artifacts. repoRoot is the module root (where ./cmd/sngl lives).
func buildBinaries(repoRoot, outDir, version, commit, date string) ([]artifact, error) {
	var arts []artifact
	for _, t := range targets {
		a, err := buildOne(repoRoot, outDir, t, version, commit, date)
		if err != nil {
			return nil, err
		}
		arts = append(arts, a)
	}
	return arts, nil
}
