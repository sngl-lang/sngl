// Package snapshot owns the golden-file machinery used by the sngl
// test driver. It stores rendered snapshots from agents and diffs
// against committed goldens. The store is mime-driven: extension and
// diff strategy come from the mime type the agent reported.
package snapshot

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Store persists and diffs per-fixture snapshots. Dir is the directory
// containing the fixture file; each fixture <name>.sngl owns a sibling
// <name>.snapshots/ directory of goldens.
type Store struct {
	Dir    string
	Update bool // when true, every Assert overwrites the golden and passes.
}

// Result reports the outcome of a single Assert call.
type Result struct {
	Pass bool
	Diff string
}

// Assert reads or creates the golden for (fixture, name, mime), compares
// against actual bytes, and returns Pass/Diff. Returns an error only on
// I/O or unsupported mime.
func (s *Store) Assert(fixture, name, mime string, actual []byte) (Result, error) {
	ext, ok := mimeExt(mime)
	if !ok {
		return Result{}, fmt.Errorf("snapshot: unsupported mime %q", mime)
	}
	goldenDir := filepath.Join(s.Dir, fixture+".snapshots")
	goldenPath := filepath.Join(goldenDir, name+ext)

	if s.Update {
		// name may contain a "/"-separated namespace (e.g. "device/")
		// — the testagent T.snapshot prepends Snapshots.namePrefix to
		// disambiguate per-runner goldens. MkdirAll the full parent.
		if err := os.MkdirAll(filepath.Dir(goldenPath), 0o755); err != nil {
			return Result{}, err
		}
		if err := os.WriteFile(goldenPath, actual, 0o644); err != nil {
			return Result{}, err
		}
		return Result{Pass: true}, nil
	}

	golden, err := os.ReadFile(goldenPath)
	if err != nil {
		if os.IsNotExist(err) {
			return Result{Pass: false, Diff: "no golden — run with SNGL_UPDATE_SNAPSHOTS=1"}, nil
		}
		return Result{}, err
	}
	if bytes.Equal(golden, actual) {
		return Result{Pass: true}, nil
	}
	return Result{Pass: false, Diff: textDiff(golden, actual)}, nil
}

// mimeExt maps mime to file extension; ok=false for unsupported mimes.
func mimeExt(mime string) (string, bool) {
	switch mime {
	case "text/plain":
		return ".txt", true
	case "text/sngl":
		return ".sngl", true
	case "text/ansi":
		return ".ansi", true
	case "text/html":
		return ".html", true
	case "application/json":
		return ".json", true
	case "image/png":
		return ".png", true
	}
	return "", false
}

// textDiff returns a small unified-style diff. We avoid pulling a full
// diff library in for this minimal need; a future caller can swap in
// internal/diff if richer output is needed.
func textDiff(want, got []byte) string {
	var b strings.Builder
	fmt.Fprintf(&b, "--- want\n+++ got\n")
	w := bytes.Split(want, []byte("\n"))
	g := bytes.Split(got, []byte("\n"))
	max := len(w)
	if len(g) > max {
		max = len(g)
	}
	for i := 0; i < max; i++ {
		var ww, gg []byte
		if i < len(w) {
			ww = w[i]
		}
		if i < len(g) {
			gg = g[i]
		}
		if bytes.Equal(ww, gg) {
			fmt.Fprintf(&b, " %s\n", ww)
		} else {
			fmt.Fprintf(&b, "-%s\n+%s\n", ww, gg)
		}
	}
	return b.String()
}
