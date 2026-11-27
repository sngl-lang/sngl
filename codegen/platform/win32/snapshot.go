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
	"duckfam.us/sngl/codegen/lang/golang"
	"duckfam.us/sngl/ir"
)

var _ codegen.Snapshotter = (*Generator)(nil)

// Snapshot implements codegen.Snapshotter: the program built for it, whose
// entry builds and shows the program and draws the window it shows at width
// by height into a file (sngl_win32_snapshot), cross-compiled and run -- under
// Wine, off Windows, in the prefix the test agent runs in -- and the pixels
// it wrote encoded as a PNG.
func (g *Generator) Snapshot(pkg *ir.Package, lang codegen.LangTranslator, width, height int) ([]byte, error) {
	if ok, reason := g.ProbeTest(); !ok {
		return nil, fmt.Errorf("win32: snapshot: %s", reason)
	}
	sink := codegen.NewMemSink()
	if err := codegen.Generate(g, &codegen.Request{
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
	exe, err := buildProgram(dir, "snapshot", lang)
	if err != nil {
		return nil, fmt.Errorf("win32: %w", err)
	}
	out := filepath.Join(dir, "snapshot.rgba")
	argv := append(clang.Launch(exe), out, strconv.Itoa(width), strconv.Itoa(height))
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = dir
	if argv[0] == "wine" {
		prefix, err := winePrefix(context.Background())
		if err != nil {
			return nil, err
		}
		cmd.Env = wineEnv(prefix)
	}
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	stderr, err := StderrToFile(cmd)
	if err != nil {
		return nil, fmt.Errorf("win32: %w", err)
	}
	err = cmd.Run()
	if log := stdout.String() + stderr(); err != nil {
		return nil, fmt.Errorf("win32: running the snapshot: %w\n%s", err, log)
	}
	raw, err := os.ReadFile(out)
	if err != nil {
		return nil, fmt.Errorf("win32: %w", err)
	}
	return encodeSnapshot(raw)
}

// buildProgram builds the program the build wrote to dir into name beside
// it, for the target its manifest names, in lang: with MinGW's compiler, or
// the Go toolchain calling it through cgo. It is the executable it wrote.
func buildProgram(dir, name string, lang codegen.LangTranslator) (string, error) {
	bin := filepath.Join(dir, name)
	if lang.LanguageIdentifier() == "go" {
		return golang.BuildModule(dir, bin)
	}
	cc, err := clang.ForTarget(clang.TargetOf(dir), "")
	if err != nil {
		return "", err
	}
	if _, err := cc.BuildDir(dir, bin, "c17", ""); err != nil {
		return "", err
	}
	return cc.Exe(bin), nil
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
