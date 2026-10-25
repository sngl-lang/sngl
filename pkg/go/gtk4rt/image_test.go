//go:build !js

package gtk4rt

import (
	"bytes"
	goimage "image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"duckfam.us/sngl/internal/testutil"
)

// A fetch that arrives after its picture is gone paints nothing, not even a
// picture made since at the address the old one left. A page drawn again
// while its pictures were on their way frees the old ones and GTK hands the
// new ones their memory, so a gallery's pictures landed under one another's
// labels.
func TestAFetchPaintsOnlyItsOwnPicture(t *testing.T) {
	if r := testutil.GUIRenderSkipReason(); r != "" {
		t.Skip(r)
	}
	red := goimage.NewNRGBA(goimage.Rect(0, 0, 200, 200))
	for i := range red.Pix {
		red.Pix[i] = []byte{255, 0, 0, 255}[i%4]
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, red); err != nil {
		t.Fatal(err)
	}
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release
		w.Write(buf.Bytes())
	}))
	defer srv.Close()

	reused := false
	build := func(app Handle) Handle {
		old := ImageSourceNew()
		Retain(old)
		ImageSourceSetPicture(old, LoadPicture(srv.URL+"/red.png"))
		Release(old)
		frame := ImageFrameNew()
		fresh := ImageSourceNew()
		reused = fresh == old
		BoxAppend(frame, fresh)
		close(release)
		PumpFor(500)
		win := ApplicationWindowNew(app)
		WindowSetChild(win, frame)
		return win
	}
	data, err := SnapshotModelBytes(build, 320, 180)
	if err != nil {
		if strings.Contains(err.Error(), "sngl_snapshot rc=") {
			t.Skipf("headless render backend unavailable: %v", err)
		}
		t.Fatalf("SnapshotModelBytes: %v", err)
	}
	if !reused {
		t.Skip("GTK made the new picture somewhere else, so the old fetch had nothing to mistake")
	}
	shot, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	b := shot.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			c := color.NRGBAModel.Convert(shot.At(x, y)).(color.NRGBA)
			if c.R > 200 && c.G < 60 && c.B < 60 {
				t.Fatalf("the new picture shows the old one's fetch, at %d,%d", x, y)
			}
		}
	}
}

// A fetch that arrives after its picture is gone keeps nothing it decoded:
// the texture was set on the source and never freed, a whole decoded image
// for each picture a page drawn again left on its way.
func TestAFetchForAGonePictureKeepsNoTexture(t *testing.T) {
	if r := testutil.GUIRenderSkipReason(); r != "" {
		t.Skip(r)
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, goimage.NewNRGBA(goimage.Rect(0, 0, 8, 8))); err != nil {
		t.Fatal(err)
	}
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release
		w.Write(buf.Bytes())
	}))
	defer srv.Close()

	var gone *source
	var fetched *Picture
	build := func(app Handle) Handle {
		pic := ImageSourceNew()
		Retain(pic)
		gone = sources[pic]
		fetched = LoadPicture(srv.URL + "/dot.png")
		ImageSourceSetPicture(pic, fetched)
		Release(pic)
		close(release)
		PumpFor(500)
		return ApplicationWindowNew(app)
	}
	if _, err := SnapshotModelBytes(build, 64, 64); err != nil {
		if strings.Contains(err.Error(), "sngl_snapshot rc=") {
			t.Skipf("headless render backend unavailable: %v", err)
		}
		t.Fatalf("SnapshotModelBytes: %v", err)
	}
	if !fetched.arrived {
		t.Fatal("the fetch never arrived")
	}
	if gone.tex != nil {
		t.Error("a gone picture's source holds the texture its fetch decoded")
	}
}
