package main

import (
	"fmt"
	"os"
	"os/exec"
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

// TestPackageBinary covers the download artifact's naming, compression and
// size record over a stand-in file. It deliberately does not build anything:
// producing a real binary here is a cross-compile of the whole compiler, and
// the CGO_ENABLED=0 target configuration shares its build cache with nothing
// else in the suite, so it is a cold link every time.
func TestPackageBinary(t *testing.T) {
	for _, tc := range []struct {
		t    target
		want string
	}{
		{target{"linux", "amd64"}, "sngl-linux-amd64.gz"},
		{target{"windows", "amd64"}, "sngl-windows-amd64.exe.gz"},
	} {
		dl := filepath.Join(t.TempDir(), "downloads")
		if err := os.MkdirAll(dl, 0o755); err != nil {
			t.Fatal(err)
		}
		bin := filepath.Join(dl, binName(tc.t))
		if err := os.WriteFile(bin, []byte(strings.Repeat("sngl", 1024)), 0o755); err != nil {
			t.Fatal(err)
		}

		a, err := packageBinary(bin, tc.t)
		if err != nil {
			t.Fatalf("packageBinary: %v", err)
		}
		if a.filename != tc.want {
			t.Errorf("filename = %q, want %q", a.filename, tc.want)
		}
		if a.arch != tc.t.goarch || a.os != displayOS(tc.t.goos) {
			t.Errorf("artifact = %s/%s, want %s/%s", a.os, a.arch, displayOS(tc.t.goos), tc.t.goarch)
		}
		if _, err := os.Stat(bin); !os.IsNotExist(err) {
			t.Errorf("uncompressed binary %s survived packaging", bin)
		}
		info, err := os.Stat(filepath.Join(dl, a.filename))
		if err != nil {
			t.Fatalf("stat: %v", err)
		}
		if info.Size() == 0 {
			t.Error("gzip artifact is empty")
		}
		if a.size != info.Size() {
			t.Errorf("artifact.size = %d, stat size = %d", a.size, info.Size())
		}
	}
}

// TestBuildCommand asserts the cross-build's shape without running it: the
// target selection and version stamping are what the download page depends on.
func TestBuildCommand(t *testing.T) {
	cmd := buildCommand("/repo", "/out/sngl-darwin-arm64", target{"darwin", "arm64"}, "v1", "abc", "2026-01-01")
	if cmd.Dir != "/repo" {
		t.Errorf("Dir = %q, want /repo", cmd.Dir)
	}
	args := strings.Join(cmd.Args, " ")
	for _, want := range []string{"-o /out/sngl-darwin-arm64", "./cmd/sngl", "-X main.version=v1", "-X main.commit=abc", "-X main.date=2026-01-01"} {
		if !strings.Contains(args, want) {
			t.Errorf("build args %q missing %q", args, want)
		}
	}
	// By last entry, not by presence: the overrides are appended after
	// os.Environ(), and a host exporting GOOS would otherwise decide the
	// target with this test still green.
	for _, want := range []string{"CGO_ENABLED=0", "GOOS=darwin", "GOARCH=arm64"} {
		key, _, _ := strings.Cut(want, "=")
		if got := lastEnv(cmd.Env, key); got != want {
			t.Errorf("build env %s = %q, want %q", key, got, want)
		}
	}
}

// lastEnv is the entry the child process sees: exec takes the last assignment
// to a name.
func lastEnv(env []string, key string) string {
	out := ""
	for _, e := range env {
		if k, _, ok := strings.Cut(e, "="); ok && k == key {
			out = e
		}
	}
	return out
}

// TestBuildOneWiresTheCompileToWhatItPackages covers the joint the split left
// between buildCommand and packageBinary: the path the compiler is told to
// write is the path that gets packaged, and the downloads directory exists
// before either. The compile is stubbed -- a real one is the whole compiler.
func TestBuildOneWiresTheCompileToWhatItPackages(t *testing.T) {
	var told string
	defer func(orig func(*exec.Cmd) error) { runBuild = orig }(runBuild)
	runBuild = func(cmd *exec.Cmd) error {
		for i, a := range cmd.Args {
			if a == "-o" && i+1 < len(cmd.Args) {
				told = cmd.Args[i+1]
			}
		}
		if told == "" {
			return fmt.Errorf("build command names no output path")
		}
		return os.WriteFile(told, []byte("stand-in binary"), 0o755)
	}

	out := t.TempDir()
	a, err := buildOne("/repo", out, target{"windows", "amd64"}, "v", "c", "d")
	if err != nil {
		t.Fatalf("buildOne: %v", err)
	}
	if want := filepath.Join(out, "downloads", "sngl-windows-amd64.exe"); told != want {
		t.Errorf("compiler told to write %q, want %q", told, want)
	}
	if a.filename != "sngl-windows-amd64.exe.gz" {
		t.Errorf("packaged %q, want sngl-windows-amd64.exe.gz", a.filename)
	}
	if _, err := os.Stat(filepath.Join(out, "downloads", a.filename)); err != nil {
		t.Errorf("the artifact buildOne reported is not where it said: %v", err)
	}
}
