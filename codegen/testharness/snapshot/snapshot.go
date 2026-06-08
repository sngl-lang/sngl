// Package snapshot owns the golden-file machinery used by the sngl
// test driver. It stores rendered snapshots from agents and diffs
// against committed goldens. The store is mime-driven: extension and
// diff strategy come from the mime type the agent reported.
package snapshot

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	_ "image/png"
	"math"
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
	if mime == "image/png" {
		ok, diff := pngMatch(goldenPath, golden, actual)
		if ok {
			return Result{Pass: true}, nil
		}
		return Result{Pass: false, Diff: diff}, nil
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

// pngMaxDiffPct is the maximum fraction of pixels that may differ between
// the golden and actual PNG before the comparison is considered a failure.
// GTK4's CairoRenderer produces non-deterministic subpixel antialiasing for
// text glyphs across separate process invocations; a small tolerance avoids
// flaky failures while still catching meaningful layout regressions (which
// affect far more pixels).
const pngMaxDiffPct = 0.5

// pngMatch decodes two PNG images and compares them with a pixel-count
// tolerance. Returns (true, "") when the fraction of differing pixels is
// below pngMaxDiffPct. On failure it saves debug copies to os.TempDir()
// and returns a diagnostic diff string.
func pngMatch(goldenPath string, goldenBytes, actualBytes []byte) (bool, string) {
	decodeImg := func(data []byte) (image.Image, error) {
		img, _, err := image.Decode(bytes.NewReader(data))
		return img, err
	}
	golden, err := decodeImg(goldenBytes)
	if err != nil {
		return false, fmt.Sprintf("PNG bytes differ (could not decode golden: %v)", err)
	}
	actual, err := decodeImg(actualBytes)
	if err != nil {
		return false, fmt.Sprintf("PNG bytes differ (could not decode actual: %v)", err)
	}
	gb := golden.Bounds()
	ab := actual.Bounds()
	if gb != ab {
		return false, fmt.Sprintf("PNG size mismatch: golden=%v actual=%v", gb, ab)
	}

	var maxDelta, totalDelta float64
	var diffCount int
	for y := gb.Min.Y; y < gb.Max.Y; y++ {
		for x := gb.Min.X; x < gb.Max.X; x++ {
			gr, gg, gb2, ga := color.RGBAModel.Convert(golden.At(x, y)).RGBA()
			ar, ag, ab2, aa := color.RGBAModel.Convert(actual.At(x, y)).RGBA()
			dr := math.Abs(float64(gr) - float64(ar))
			dg := math.Abs(float64(gg) - float64(ag))
			db := math.Abs(float64(gb2) - float64(ab2))
			da := math.Abs(float64(ga) - float64(aa))
			d := (dr + dg + db + da) / 4
			if d > 0 {
				diffCount++
				totalDelta += d
				if d > maxDelta {
					maxDelta = d
				}
			}
		}
	}
	total := gb.Dx() * gb.Dy()
	if diffCount == 0 {
		// Bytes differ but pixels are identical (e.g. metadata difference).
		return true, ""
	}
	diffPct := float64(diffCount) * 100 / float64(total)
	if diffPct <= pngMaxDiffPct {
		return true, ""
	}
	// Save debug copies so failures can be inspected.
	base := strings.TrimSuffix(filepath.Base(goldenPath), filepath.Ext(goldenPath))
	dbgGolden := filepath.Join(os.TempDir(), "sngl-snap-golden-"+base+".png")
	dbgActual := filepath.Join(os.TempDir(), "sngl-snap-actual-"+base+".png")
	_ = os.WriteFile(dbgGolden, goldenBytes, 0o644)
	_ = os.WriteFile(dbgActual, actualBytes, 0o644)
	return false, fmt.Sprintf(
		"PNG pixels differ: %d/%d pixels (%.2f%% > %.1f%% threshold), maxDelta=%.0f/65535, avgDelta=%.0f/65535\ngolden: %s\nactual: %s",
		diffCount, total, diffPct, pngMaxDiffPct,
		maxDelta, totalDelta/float64(diffCount),
		dbgGolden, dbgActual,
	)
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
