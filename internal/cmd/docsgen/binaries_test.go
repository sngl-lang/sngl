package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInjectDownloads(t *testing.T) {
	out := t.TempDir()
	learn := filepath.Join(out, "learn")
	if err := os.MkdirAll(learn, 0o755); err != nil {
		t.Fatal(err)
	}
	page := filepath.Join(learn, "installation.html")
	const html = "<h2>Download Prebuilt Binaries</h2>\n<!-- sngl:downloads -->\n"
	if err := os.WriteFile(page, []byte(html), 0o644); err != nil {
		t.Fatal(err)
	}

	arts := []artifact{
		{os: "Linux", arch: "amd64", filename: "sngl-linux-amd64.gz", size: 26_214_400},
		{os: "Windows", arch: "amd64", filename: "sngl-windows-amd64.exe.gz", size: 27_000_000},
	}
	if err := injectDownloads(out, arts); err != nil {
		t.Fatalf("injectDownloads: %v", err)
	}

	data, err := os.ReadFile(page)
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)

	if strings.Contains(got, "<!-- sngl:downloads -->") {
		t.Error("marker not replaced")
	}
	for _, want := range []string{
		`href="/downloads/sngl-linux-amd64.gz"`,
		`href="/downloads/sngl-windows-amd64.exe.gz"`,
		"Linux", "Windows", "amd64",
		"26.2 MB", // 26_214_400 bytes / 1e6, rounded
	} {
		if !strings.Contains(got, want) {
			t.Errorf("injected HTML missing %q", want)
		}
	}
}

func TestInjectDownloadsNoMarker(t *testing.T) {
	out := t.TempDir()
	learn := filepath.Join(out, "learn")
	if err := os.MkdirAll(learn, 0o755); err != nil {
		t.Fatal(err)
	}
	page := filepath.Join(learn, "installation.html")
	if err := os.WriteFile(page, []byte("<h2>No marker here</h2>"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := injectDownloads(out, []artifact{{os: "Linux", arch: "amd64", filename: "x.gz", size: 1}}); err != nil {
		t.Fatalf("expected nil error on missing marker, got %v", err)
	}
}

func TestResolveVersion(t *testing.T) {
	v, c, d := resolveVersion()
	if v == "" || c == "" || d == "" {
		t.Errorf("resolveVersion returned empty field: version=%q commit=%q date=%q", v, c, d)
	}
}

func TestBuildOne(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping cross-build in -short mode")
	}
	repoRoot, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	out := t.TempDir()
	a, err := buildOne(repoRoot, out, target{goos: "linux", goarch: "amd64"}, "vtest", "ctest", "dtest")
	if err != nil {
		t.Fatalf("buildOne: %v", err)
	}
	if a.filename != "sngl-linux-amd64.gz" {
		t.Errorf("filename = %q, want sngl-linux-amd64.gz", a.filename)
	}
	gz := filepath.Join(out, "downloads", a.filename)
	info, err := os.Stat(gz)
	if err != nil {
		t.Fatalf("stat %s: %v", gz, err)
	}
	if info.Size() == 0 {
		t.Error("gzip artifact is empty")
	}
	if a.size != info.Size() {
		t.Errorf("artifact.size = %d, stat size = %d", a.size, info.Size())
	}
}
