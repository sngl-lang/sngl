//go:build !js

package gtk4

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"

	"duckfam.us/sngl/codegen"
	clang "duckfam.us/sngl/codegen/lang/c"
	"duckfam.us/sngl/codegen/lang/golang"
	"duckfam.us/sngl/ir"
)

// snapshotOption builds the program for `sngl snapshot`.
const snapshotOption = "snapshot"

var _ codegen.Snapshotter = (*Generator)(nil)

// Snapshot implements codegen.Snapshotter: the program built for it, whose
// entry builds the program and draws its first window at width by height
// into a file (the platform's _captureFile), built and run in the display this
// process has, and the pixels it wrote encoded as a PNG.
func (g *Generator) Snapshot(pkg *ir.Package, lang codegen.LangTranslator, width, height int) ([]byte, error) {
	if ok, reason := g.ProbeTest(); !ok {
		return nil, fmt.Errorf("%s: snapshot: %s", platformName, reason)
	}
	sink := codegen.NewMemSink()
	if err := g.Generate(&codegen.Request{
		Pkg:     pkg,
		Lang:    lang,
		Options: codegen.OptionsFromMap(map[string]any{snapshotOption: true}),
	}, sink); err != nil {
		return nil, fmt.Errorf("%s: %w", platformName, err)
	}
	dir, release, err := codegen.BuildDirForContent("snapshot-"+platformName, sink.Files())
	if err != nil {
		return nil, fmt.Errorf("%s: %w", platformName, err)
	}
	defer release()
	exe := filepath.Join(dir, "snapshot")
	if lang.LanguageIdentifier() == "go" {
		if exe, err = golang.BuildModule(dir, exe); err != nil {
			return nil, fmt.Errorf("%s: %w", platformName, err)
		}
	} else {
		cc, err := clang.ForTarget(clang.TargetOf(dir), "")
		if err != nil {
			return nil, fmt.Errorf("%s: %w", platformName, err)
		}
		if out, err := cc.BuildDir(dir, exe, "c17", ""); err != nil {
			return nil, fmt.Errorf("%s: %w\n%s", platformName, err, out)
		}
		exe = cc.Exe(exe)
	}
	out := filepath.Join(dir, "snapshot.rgba")
	cmd := exec.Command(exe, out, strconv.Itoa(width), strconv.Itoa(height))
	cmd.Dir = dir
	if log, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("%s: running the snapshot: %w\n%s", platformName, err, log)
	}
	raw, err := os.ReadFile(out)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", platformName, err)
	}
	return encodeSnapshot(raw)
}

// encodeSnapshot is the PNG of what _captureFile wrote: its width
// and height, four bytes each, little-endian, then its pixels, four bytes
// each, red, green, blue and alpha, row by row from the top.
func encodeSnapshot(raw []byte) ([]byte, error) {
	if len(raw) < 8 {
		return nil, fmt.Errorf("%s: a snapshot of %d bytes", platformName, len(raw))
	}
	w := int(binary.LittleEndian.Uint32(raw[0:4]))
	h := int(binary.LittleEndian.Uint32(raw[4:8]))
	pix := raw[8:]
	if w <= 0 || h <= 0 || len(pix) != w*h*4 {
		return nil, fmt.Errorf("%s: a snapshot of %d bytes of pixels for %d by %d", platformName, len(pix), w, h)
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, &image.NRGBA{Pix: pix, Stride: w * 4, Rect: image.Rect(0, 0, w, h)}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
