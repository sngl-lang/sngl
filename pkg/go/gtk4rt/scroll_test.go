//go:build !js

package gtk4rt

import (
	"bytes"
	goimage "image"
	"image/color"
	"image/png"
	"os"
	"strings"
	"testing"

	"duckfam.us/sngl/internal/testutil"
)

// A scroll holding more than it shows still shows its pictures. The viewport
// laid its content out at its minimum height, which is nothing for a picture,
// so content whose text alone was taller than the scroll drew every picture
// at no height at all.
func TestScrollShowsThePicturesItOverflowsWith(t *testing.T) {
	if r := testutil.GUIRenderSkipReason(); r != "" {
		t.Skip(r)
	}
	t.Chdir(t.TempDir())
	red := goimage.NewNRGBA(goimage.Rect(0, 0, 200, 200))
	for i := range red.Pix {
		red.Pix[i] = []byte{255, 0, 0, 255}[i%4]
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, red); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("red.png", buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}

	build := func(app Handle) Handle {
		list := BoxNew(OrientationVertical, 6)
		for range 10 {
			frame := ImageFrameNew()
			src := ImageSourceNew()
			ImageSourceSetPicture(src, LoadPicture("red.png"))
			BoxAppend(frame, src)
			BoxAppend(list, frame)
			BoxAppend(list, LabelNew(strings.Repeat("line\n", 20)))
		}
		sw := ScrollNew()
		ScrolledWindowSetChild(sw, list)
		win := ApplicationWindowNew(app)
		WindowSetChild(win, sw)
		return win
	}
	data, err := SnapshotModelBytes(build, 320, 180)
	if err != nil {
		if strings.Contains(err.Error(), "sngl_snapshot rc=") {
			t.Skipf("headless render backend unavailable: %v", err)
		}
		t.Fatalf("SnapshotModelBytes: %v", err)
	}
	shot, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	reds := 0
	b := shot.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			c := color.NRGBAModel.Convert(shot.At(x, y)).(color.NRGBA)
			if c.R > 200 && c.G < 60 && c.B < 60 {
				reds++
			}
		}
	}
	// The window is the size the compositor gives it and is drawn into the
	// snapshot's, so a picture is smaller than its 200 pixels there, and at
	// least one shows whole.
	if reds < 400 {
		t.Errorf("the scroll shows %d red pixels, want at least a picture's", reds)
	}
}
