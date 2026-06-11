# docsgen Prebuilt Binaries Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make `go tool docsgen -binaries` cross-compile the `sngl` CLI for five platforms, gzip them into the site's `downloads/` directory, and inject a download table into the Installation page.

**Architecture:** A new `binaries.go` in the docsgen command holds the target list, the cross-build+gzip loop, git-derived version stamping, and an `injectDownloads` post-processor that mirrors the existing `injectExamples`/`injectThemeBootstrap` HTML-rewrite passes. A `-binaries` flag in `main.go` gates the whole thing (off by default so local runs stay fast); the CI `pages` job opts in.

**Tech Stack:** Go stdlib (`os/exec`, `compress/gzip`, `flag`, `text/template` or `strings.Builder`), the existing `cmd/sngl/version.go` ldflags vars (`main.version`, `main.commit`, `main.date`).

---

### Task 1: Add the downloads marker to the Installation page

**Files:**
- Modify: `docs/learn/installation.md`

- [ ] **Step 1: Add a Download section with the injection marker**

Insert after the "Verify Installation" section (after line 21, before "## Editor Support") in `docs/learn/installation.md`:

```markdown
## Download Prebuilt Binaries

Prebuilt binaries are available for common platforms. Download the `.gz` for
your system, decompress it (`gunzip sngl-*.gz`), make it executable
(`chmod +x sngl-*`), rename it to `sngl`, and place it on your `PATH`.

<!-- sngl:downloads -->
```

- [ ] **Step 2: Verify it renders (marker stays an HTML comment without the flag)**

Run: `go tool docsgen -out /tmp/docsgen-marker && grep -c "sngl:downloads" /tmp/docsgen-marker/learn/installation.html`
Expected: prints `1` — the marker comment is present and harmless; the prose renders above it.

- [ ] **Step 3: Commit**

```bash
git add docs/learn/installation.md
git commit -m "docs: add prebuilt-binaries section + injection marker to installation page"
```

---

### Task 2: `injectDownloads` — render the download table (TDD)

**Files:**
- Create: `internal/cmd/docsgen/binaries.go`
- Test: `internal/cmd/docsgen/binaries_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/cmd/docsgen/binaries_test.go`:

```go
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
		"25.0 MB", // 26_214_400 bytes rounded
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
	// Missing marker is non-fatal.
	if err := injectDownloads(out, []artifact{{os: "Linux", arch: "amd64", filename: "x.gz", size: 1}}); err != nil {
		t.Fatalf("expected nil error on missing marker, got %v", err)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/cmd/docsgen/ -run TestInjectDownloads -v`
Expected: FAIL — `undefined: artifact` and `undefined: injectDownloads`.

- [ ] **Step 3: Write minimal implementation**

Create `internal/cmd/docsgen/binaries.go`:

```go
package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
)

// artifact is one cross-compiled, gzip-compressed sngl binary placed under
// <outDir>/downloads/.
type artifact struct {
	os, arch string // human-facing display values (e.g. "Linux", "amd64")
	filename string // basename under downloads/, e.g. "sngl-linux-amd64.gz"
	size     int64  // compressed size in bytes
}

const downloadsMarker = "<!-- sngl:downloads -->"

// humanSize renders a byte count as a one-decimal MiB string, e.g. "25.0 MB".
func humanSize(n int64) string {
	return fmt.Sprintf("%.1f MB", float64(n)/(1024*1024))
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
		fmt.Fprintf(&b,
			`<tr><td>%s</td><td>%s</td><td><a href="/downloads/%s">%s</a></td><td>%s</td></tr>`,
			a.os, a.arch, a.filename, a.filename, humanSize(a.size),
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
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/cmd/docsgen/ -run TestInjectDownloads -v`
Expected: PASS for both `TestInjectDownloads` and `TestInjectDownloadsNoMarker`.

- [ ] **Step 5: Commit**

```bash
git add internal/cmd/docsgen/binaries.go internal/cmd/docsgen/binaries_test.go
git commit -m "feat(docsgen): injectDownloads renders binary download table"
```

---

### Task 3: Version resolution from git

**Files:**
- Modify: `internal/cmd/docsgen/binaries.go`
- Test: `internal/cmd/docsgen/binaries_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/cmd/docsgen/binaries_test.go`:

```go
func TestResolveVersion(t *testing.T) {
	v, c, d := resolveVersion()
	// In a git checkout these resolve to real values; outside one they fall
	// back. Either way none may be empty.
	if v == "" || c == "" || d == "" {
		t.Errorf("resolveVersion returned empty field: version=%q commit=%q date=%q", v, c, d)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/cmd/docsgen/ -run TestResolveVersion -v`
Expected: FAIL — `undefined: resolveVersion`.

- [ ] **Step 3: Write minimal implementation**

Add to `internal/cmd/docsgen/binaries.go` (add `"os/exec"` and `"time"` to the import block):

```go
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
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/cmd/docsgen/ -run TestResolveVersion -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/cmd/docsgen/binaries.go internal/cmd/docsgen/binaries_test.go
git commit -m "feat(docsgen): resolveVersion derives build metadata from git"
```

---

### Task 4: `buildBinaries` — cross-compile + gzip

**Files:**
- Modify: `internal/cmd/docsgen/binaries.go`
- Test: `internal/cmd/docsgen/binaries_test.go`

- [ ] **Step 1: Write the failing test (single target, real cross-build, guarded)**

Append this test function to `internal/cmd/docsgen/binaries_test.go` (no new test-file imports needed — `os`, `path/filepath`, `testing` are already imported from Task 2):

```go
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
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/cmd/docsgen/ -run TestBuildOne -v`
Expected: FAIL — `undefined: target`, `undefined: buildOne`.

- [ ] **Step 3: Write minimal implementation**

Add to `internal/cmd/docsgen/binaries.go` (add `"compress/gzip"` and `"io"` to imports):

```go
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
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/cmd/docsgen/ -run TestBuildOne -v`
Expected: PASS — a `sngl-linux-amd64.gz` appears in the temp `downloads/`.

- [ ] **Step 5: Commit**

```bash
git add internal/cmd/docsgen/binaries.go internal/cmd/docsgen/binaries_test.go
git commit -m "feat(docsgen): buildBinaries cross-compiles + gzips sngl for all targets"
```

---

### Task 5: Wire the `-binaries` flag into `main.go`

**Files:**
- Modify: `internal/cmd/docsgen/main.go:24-67`

- [ ] **Step 1: Add the flag and the build/inject call**

In `internal/cmd/docsgen/main.go`, add the flag declaration alongside the existing flags (after line 26):

```go
	binaries := flag.Bool("binaries", false, "cross-compile sngl CLI downloads and inject the download table")
```

Then, after the `copySnapshots(*outDir)` call (currently line 45) and before `injectExamples`, add:

```go
	// Optionally cross-compile sngl downloads and inject the download table.
	// Gated by -binaries so local builds stay fast; CI passes the flag.
	if *binaries {
		version, commit, date := resolveVersion()
		repoRoot, err := os.Getwd()
		if err != nil {
			log.Fatalf("binaries: %v", err)
		}
		arts, err := buildBinaries(repoRoot, *outDir, version, commit, date)
		if err != nil {
			log.Fatalf("binaries: %v", err)
		}
		if err := injectDownloads(*outDir, arts); err != nil {
			log.Printf("downloads: %v", err)
		}
	}
```

(Note: docsgen runs from the repo root — `prebuildPlaygroundAssets` already shells `go build ./internal/playground/cmd` with that assumption — so `os.Getwd()` is the module root.)

- [ ] **Step 2: Verify it compiles**

Run: `go build ./internal/cmd/docsgen/`
Expected: no output (success).

- [ ] **Step 3: Run the full docsgen test suite**

Run: `go test ./internal/cmd/docsgen/`
Expected: PASS (cross-build tests skipped under `-short`; run without `-short` they pass but are slow).

- [ ] **Step 4: Commit**

```bash
git add internal/cmd/docsgen/main.go
git commit -m "feat(docsgen): add -binaries flag gating download generation"
```

---

### Task 6: End-to-end smoke build

**Files:** none (manual verification)

- [ ] **Step 1: Run docsgen with -binaries into a temp dir**

Run: `go tool docsgen -binaries -out /tmp/docsgen-bins`
Expected: log lines `binaries: built sngl-linux-amd64.gz (…)` for all five targets, then `downloads: injected 5 artifacts`.

- [ ] **Step 2: Verify artifacts and injected table**

Run:
```bash
ls /tmp/docsgen-bins/downloads/
grep -o 'href="/downloads/sngl-[^"]*"' /tmp/docsgen-bins/learn/installation.html
```
Expected: five `.gz` files listed; five `href="/downloads/sngl-*.gz"` matches printed. No `<!-- sngl:downloads -->` remains:

Run: `grep -c "sngl:downloads" /tmp/docsgen-bins/learn/installation.html`
Expected: `0`.

- [ ] **Step 3: Verify a binary actually runs and reports stamped version**

Run:
```bash
gunzip -c /tmp/docsgen-bins/downloads/sngl-linux-amd64.gz > /tmp/sngl-test && chmod +x /tmp/sngl-test && /tmp/sngl-test version
```
Expected: `sngl <git-describe> (commit <short-sha>, built <rfc3339>)` — not `dev`/`none`/`unknown` when run inside the checkout.

---

### Task 7: Update CI `pages` job

**Files:**
- Modify: `.gitlab-ci.yml:58`

- [ ] **Step 1: Add the flag to the docsgen invocation**

In `.gitlab-ci.yml`, change the `pages` job script line:

```yaml
    - go tool docsgen -out public
```

to:

```yaml
    - go tool docsgen -binaries -out public
```

- [ ] **Step 2: Verify YAML is still valid**

Run: `go run gopkg.in/yaml.v3 < /dev/null 2>/dev/null; python3 -c "import yaml,sys; yaml.safe_load(open('.gitlab-ci.yml'))" && echo OK`
Expected: `OK` (YAML parses). If `python3`/`yaml` unavailable, visually confirm the single-line change.

- [ ] **Step 3: Commit**

```bash
git add .gitlab-ci.yml
git commit -m "ci: build prebuilt binaries in pages job"
```

---

### Task 8: Full verification

**Files:** none

- [ ] **Step 1: Build everything**

Run: `go build ./...`
Expected: success.

- [ ] **Step 2: Run the docsgen package tests (full, not -short)**

Run: `go test ./internal/cmd/docsgen/`
Expected: PASS.

- [ ] **Step 3: Format**

Run: `cd internal/cmd/docsgen && go fmt . && cd -`
Expected: clean / files already formatted.
