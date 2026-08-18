//go:build !js

package gtk4rt

import (
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

// TestSnapshotAndCallback exercises the runtime end-to-end without any
// generated code: it builds a small widget tree through the wrapper API, wires
// a signal through Connect (cbind.Register) and fires it via Emit
// (cbind.Dispatch), then renders a PNG through SnapshotModel. This is the
// hand-written proof that gtk4rt + cbind work before codegen targets them.
func TestSnapshotAndCallback(t *testing.T) {
	if os.Getenv("DISPLAY") == "" && os.Getenv("WAYLAND_DISPLAY") == "" {
		t.Skip("gtk4 snapshot needs an X11/Wayland display")
	}

	var clicked bool
	build := func(app Handle) Handle {
		box := BoxNew(OrientationVertical, 6)
		BoxAppend(box, LabelNew("Hello"))

		btn := ButtonNew()
		ButtonSetLabel(btn, "Go")
		Connect(btn, "clicked", func() { clicked = true })
		BoxAppend(box, btn)
		Emit(btn, "clicked") // fire synchronously through the dispatch path

		win := ApplicationWindowNew(app)
		WindowSetChild(win, box)
		return win
	}

	out := filepath.Join(t.TempDir(), "out.png")
	if err := SnapshotModel(build, 200, 120, out); err != nil {
		t.Fatalf("SnapshotModel: %v", err)
	}

	if !clicked {
		t.Error("Connect/Emit did not dispatch the clicked handler")
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
