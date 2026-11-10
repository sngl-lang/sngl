//go:build !js

package win32

import (
	"bytes"
	"context"
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
	"duckfam.us/sngl/ir"
)

var _ codegen.Snapshotter = (*Generator)(nil)

// Snapshot implements codegen.Snapshotter: the program built for it, whose
// entry builds and shows the program and draws the window it shows at width
// by height into a file (sngl_win32_snapshot), cross-compiled and run -- under
// Wine, off Windows, in the prefix the test agent runs in -- and the pixels
// it wrote encoded as a PNG.
func (g *Generator) Snapshot(pkg *ir.Package, lang codegen.LangTranslator, width, height int) ([]byte, error) {
	if id := lang.LanguageIdentifier(); id != "c" {
		return nil, fmt.Errorf("win32: a snapshot in %s is not written yet", id)
	}
	if ok, reason := g.ProbeTest(); !ok {
		return nil, fmt.Errorf("win32: snapshot: %s", reason)
	}
	sink := codegen.NewMemSink()
	if err := g.Generate(&codegen.Request{
		Pkg:     pkg,
		Lang:    lang,
		Options: codegen.OptionsFromMap(map[string]any{snapshotOption: true}),
	}, sink); err != nil {
		return nil, fmt.Errorf("win32: %w", err)
	}
	dir, release, err := codegen.BuildDirForContent("snapshot-win32", sink.Files())
	if err != nil {
		return nil, fmt.Errorf("win32: %w", err)
	}
	defer release()
	cc, err := clang.ForTarget(clang.TargetOf(dir), "")
	if err != nil {
		return nil, fmt.Errorf("win32: %w", err)
	}
	bin := filepath.Join(dir, "snapshot")
	if _, err := cc.BuildDir(dir, bin, "c17"); err != nil {
		return nil, fmt.Errorf("win32: %w", err)
	}
	out := filepath.Join(dir, "snapshot.rgba")
	argv := append(clang.Launch(cc.Exe(bin)), out, strconv.Itoa(width), strconv.Itoa(height))
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = dir
	if argv[0] == "wine" {
		prefix, err := winePrefix(context.Background())
		if err != nil {
			return nil, err
		}
		cmd.Env = wineEnv(prefix)
	}
	if log, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("win32: running the snapshot: %w\n%s", err, log)
	}
	raw, err := os.ReadFile(out)
	if err != nil {
		return nil, fmt.Errorf("win32: %w", err)
	}
	return encodeSnapshot(raw)
}

// encodeSnapshot is the PNG of what sngl_win32_snapshot wrote: its width and
// height, four bytes each, little-endian, then its pixels, four bytes each,
// red, green, blue and alpha, row by row from the top.
func encodeSnapshot(raw []byte) ([]byte, error) {
	if len(raw) < 8 {
		return nil, fmt.Errorf("win32: a snapshot of %d bytes", len(raw))
	}
	w := int(binary.LittleEndian.Uint32(raw[0:4]))
	h := int(binary.LittleEndian.Uint32(raw[4:8]))
	pix := raw[8:]
	if w <= 0 || h <= 0 || len(pix) != w*h*4 {
		return nil, fmt.Errorf("win32: a snapshot of %d bytes of pixels for %d by %d", len(pix), w, h)
	}
	img := &image.NRGBA{Pix: pix, Stride: w * 4, Rect: image.Rect(0, 0, w, h)}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
