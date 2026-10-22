package fynelayout

import (
	"bytes"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	fynetest "fyne.io/fyne/v2/test"
)

// A source whose URL moves to one that fails shows nothing, not the picture
// the last URL fetched.
func TestAFailedURLClearsThePicture(t *testing.T) {
	fynetest.NewTempApp(t)
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 1, 1))); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/dot.png" {
			w.Write(buf.Bytes())
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	s := NewImageSource()
	img := NewImage()
	loads, failures := make(chan struct{}, 1), make(chan string, 1)
	img.OnLoad = func() { loads <- struct{}{} }
	img.OnError = func(m string) { failures <- m }
	img.Add(s)

	s.SetURL(srv.URL + "/dot.png")
	wait(t, loads)
	if s.pic.Resource == nil {
		t.Fatal("no picture after the first URL loaded")
	}
	s.SetURL(srv.URL + "/missing.png")
	wait(t, failures)
	if s.pic.Resource != nil {
		t.Error("the failed URL still shows the last URL's picture")
	}
}

func wait[T any](t *testing.T, c chan T) {
	t.Helper()
	select {
	case <-c:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out")
	}
}
