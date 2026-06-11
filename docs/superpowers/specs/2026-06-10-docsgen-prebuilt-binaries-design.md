# docsgen Prebuilt Binaries — Design

**Date:** 2026-06-10
**Status:** Approved

## Goal

Have `go tool docsgen` cross-compile the `sngl` CLI for the common
platforms and surface them as downloads on the Installation page of the
documentation site.

## Targets

Five build targets:

| GOOS    | GOARCH | Artifact filename            |
|---------|--------|------------------------------|
| linux   | amd64  | `sngl-linux-amd64.gz`        |
| linux   | arm64  | `sngl-linux-arm64.gz`        |
| darwin  | amd64  | `sngl-darwin-amd64.gz`       |
| darwin  | arm64  | `sngl-darwin-arm64.gz`       |
| windows | amd64  | `sngl-windows-amd64.exe.gz`  |

The `sngl` CLI cross-compiles cleanly with `CGO_ENABLED=0`: the only CGo in
the tree lives in the gtk4/fyne snapshot/test paths, which are build-tagged
out of the CLI binary. Verified by a dry-run `CGO_ENABLED=0 GOOS=windows
GOARCH=amd64 go build ./cmd/sngl`.

Raw binaries are ~86 MB, so each is gzip-compressed individually
(`compress/gzip`, stdlib) — roughly ~25 MB each. No tar/zip archive layout;
a single compressed binary per target keeps the layout flat and the download
self-explanatory.

## Trigger

Binary generation is **gated behind a new `-binaries` flag** on docsgen
(default off). Normal docsgen runs stay fast and skip cross-compilation;
release builds pass `-binaries`. When the flag is absent, no `downloads/`
directory is produced and the Installation page shows only the
build-from-source instructions.

## Components

### `internal/cmd/docsgen/binaries.go` (new)

```go
type target struct{ goos, goarch string }

type artifact struct {
    os, arch string // display values
    filename string // basename under downloads/
    size     int64  // compressed size in bytes
}

var targets = []target{ /* the five above */ }

// buildBinaries cross-compiles sngl for every target, gzip-compresses each
// into <outDir>/downloads/, and returns the resulting artifacts.
func buildBinaries(outDir, version, commit, date string) ([]artifact, error)
```

Per target:

1. `go build -o <tmpfile> -ldflags "-X main.version=<v> -X main.commit=<c>
   -X main.date=<d>" ./cmd/sngl` with env `CGO_ENABLED=0`, `GOOS`, `GOARCH`.
2. Open the temp binary, stream it through `gzip.Writer` into
   `<outDir>/downloads/<filename>`.
3. `stat` the compressed file for its size; remove the temp binary.
4. Append an `artifact`.

The version/commit/date stamping reuses the existing ldflags vars in
`cmd/sngl/version.go` (`main.version`, `main.commit`, `main.date`).

### Version resolution

A small helper derives build metadata from git, with safe fallbacks:

- `version`: `git describe --tags --always`, else `"dev"`
- `commit`: `git rev-parse --short HEAD`, else `"none"`
- `date`: build timestamp (RFC3339, from the OS clock at docsgen runtime),
  else `"unknown"`

Git failures are non-fatal — they degrade to the fallback strings so the
build still succeeds outside a git checkout.

### `injectDownloads(outDir string, arts []artifact) error` (new, in main.go or binaries.go)

Mirrors the existing `injectExamples` / `injectTutorialLessons` /
`injectThemeBootstrap` post-processors:

1. Read `<outDir>/learn/installation.html`.
2. Replace the marker `<!-- sngl:downloads -->` with a rendered HTML table
   (columns: Platform, Architecture, Download link, Size). Links are
   relative (`../downloads/<filename>` or `/downloads/<filename>` depending
   on the page's emitted base — match what other relative asset links use).
3. If the marker is absent, log and skip (idempotent / defensive).

### `installation.md` change

Add a section after "Install from Source":

```markdown
## Download Prebuilt Binaries

Prebuilt binaries are available for common platforms. Download, decompress
(`gunzip`), make executable, and place on your `PATH`.

<!-- sngl:downloads -->
```

When docsgen runs without `-binaries`, the marker comment renders as an HTML
comment (invisible) and only the prose shows. With `-binaries`, the marker is
replaced by the download table.

### `main.go` wiring

- Declare `binaries := flag.Bool("binaries", false, "cross-compile sngl CLI downloads")`.
- After `compileSNGL` (and before the theme bootstrap pass, so the table is
  present when other passes run, though order is not critical):

```go
if *binaries {
    arts, err := buildBinaries(*outDir, resolveVersion()...)
    if err != nil { log.Fatalf("binaries: %v", err) }
    if err := injectDownloads(*outDir, arts); err != nil {
        log.Printf("downloads: %v", err)
    }
}
```

### `.gitlab-ci.yml` change

The `pages` job (which publishes the site to GitHub Pages) runs
`go tool docsgen -out public`. Add the flag so released pages carry the
binaries:

```yaml
    - go tool docsgen -binaries -out public
```

The cross-build runs only in CI / on release publishes, where the extra time
is acceptable; local `go tool docsgen` runs stay fast (no flag).

## Data flow

```
docsgen -binaries
  → compileSNGL (installation.md → installation.html with marker)
  → resolveVersion() (git describe / rev-parse / clock)
  → buildBinaries() → _site/downloads/sngl-*.gz + []artifact
  → injectDownloads() (marker → HTML table in installation.html)
  → injectThemeBootstrap() (unchanged)
```

## Error handling

- Cross-build failure for any target → fatal (`log.Fatalf`); a broken release
  download is worse than no download.
- `injectDownloads` missing-marker → logged, non-fatal.
- git metadata failures → fallback strings, non-fatal.

## Testing

`internal/cmd/docsgen/build_test.go`:

- Unit-test `injectDownloads` with synthetic `[]artifact` against a small
  in-memory/temp `installation.html` containing the marker; assert the table
  rows and links appear and the marker is gone.
- Do **not** cross-compile all five targets in tests (5×~86 MB is too heavy
  for CI). Optionally a build-tag-gated integration test could build a single
  `linux/amd64` artifact and assert a `.gz` lands in `downloads/`, but the
  default test suite stays fast.

## Out of scope (YAGNI)

- tar/zip archives, checksums (`SHA256SUMS`), or signing.
- A dedicated downloads page or nav entry.
- Version-stamped filenames (filenames stay `latest`; `sngl version` reports
  the embedded build metadata).
- Publishing/uploading artifacts anywhere — docsgen only writes them into the
  site output tree.
