//go:build !js

package gtk4rt

import (
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func hasDisplay() bool {
	return os.Getenv("DISPLAY") != "" || os.Getenv("WAYLAND_DISPLAY") != ""
}

// TestCallbackDispatch exercises the wrapper API and the cbind callback path
// without rendering: it builds widgets, wires a signal through Connect
// (cbind.Register) and fires it via Emit (cbind.Dispatch). This needs a display
// for gtk_init but no GPU/GL, so it is a reliable gate on the runtime.
func TestCallbackDispatch(t *testing.T) {
	if !hasDisplay() {
		t.Skip("gtk4 needs an X11/Wayland display")
	}
	Init()

	var clicks int
	btn := ButtonNew()
	ButtonSetLabel(btn, "go")
	Connect(btn, "clicked", func() { clicks++ })

	box := BoxNew(OrientationVertical, 6)
	BoxAppend(box, LabelNew("hi"))
	BoxAppend(box, btn)

	Emit(btn, "clicked")
	Emit(btn, "clicked")
	if clicks != 2 {
		t.Fatalf("Connect/Emit dispatched %d times, want 2", clicks)
	}
}

// TestSnapshotRender renders a widget tree to PNG through SnapshotModel. Real
// rendering needs a working GSK/GL path, which a headless CI (wlroots pixman,
// no GPU/EGL) may not provide — there the GskCairoRenderer fails and we skip
// rather than flake. It is also skipped under -short. When it does render, it
// asserts the output dimensions.
func TestSnapshotRender(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping real-render snapshot in -short mode")
	}
	if !hasDisplay() {
		t.Skip("gtk4 snapshot needs an X11/Wayland display")
	}

	build := func(app Handle) Handle {
		box := BoxNew(OrientationVertical, 6)
		BoxAppend(box, LabelNew("Hello"))
		win := ApplicationWindowNew(app)
		WindowSetChild(win, box)
		return win
	}

	out := filepath.Join(t.TempDir(), "out.png")
	if err := SnapshotModel(build, 200, 120, out); err != nil {
		// rc=3/4/5 == paintable/renderer/texture failure: no usable GL backend.
		if strings.Contains(err.Error(), "sngl_snapshot rc=") {
			t.Skipf("headless render backend unavailable: %v", err)
		}
		t.Fatalf("SnapshotModel: %v", err)
	}

	f, err := os.Open(out)
	if err != nil {
		t.Fatalf("open snapshot: %v", err)
	}
	defer f.Close()
	cfg, err := png.DecodeConfig(f)
	if err != nil {
		t.Fatalf("decode png: %v", err)
	}
	if cfg.Width != 200 || cfg.Height != 120 {
		t.Errorf("snapshot dims = %dx%d, want 200x120", cfg.Width, cfg.Height)
	}
}
