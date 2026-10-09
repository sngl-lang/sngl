// Package snglimage is where a Go target's image sources get their bytes: a
// file the program carries, embedded with it from the `file:` assets the build
// copied, or one fetched from a URL while the program runs.
package snglimage

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"io/fs"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
)

var assets fs.FS

// arrived is told each time a fetch finishes, from the fetching goroutine.
var arrived func()

// OnArrive is how a program that draws from what it has rather than being told
// -- a terminal's view, drawn again on every message -- learns to draw again.
func OnArrive(fn func()) { arrived = fn }

// Embed is where the program's assets are: the generated `assets.go` hands its
// embedded directory over as the program starts.
func Embed(f fs.FS) { assets = f }

// IsRemote reports whether url names something fetched rather than carried.
func IsRemote(url string) bool {
	return strings.HasPrefix(url, "http://") || strings.HasPrefix(url, "https://")
}

// ErrPending is what Load answers for a remote image still on its way.
var ErrPending = errors.New("snglimage: still fetching")

type fetch struct {
	done    bool
	data    []byte
	err     error
	waiting []func([]byte, error)
}

var (
	mu      sync.Mutex
	fetches = map[string]*fetch{}
)

// Carried is the bytes of a file the program carries: an asset's path, read
// from the embedded assets, or from the working directory where nothing was
// embedded.
func Carried(url string) ([]byte, error) {
	name := strings.TrimPrefix(url, "/")
	if assets != nil {
		if data, err := fs.ReadFile(assets, name); err == nil {
			return data, nil
		}
	}
	return os.ReadFile(name)
}

// Fetch hands done the bytes url names, once: at once for a file the program
// carries or a fetch already finished, and from the fetching goroutine
// otherwise. One fetch serves every caller asking for the same URL.
func Fetch(url string, done func([]byte, error)) {
	if !IsRemote(url) {
		data, err := Carried(url)
		done(data, err)
		return
	}
	mu.Lock()
	f := fetches[url]
	if f != nil && f.done {
		mu.Unlock()
		done(f.data, f.err)
		return
	}
	if f != nil {
		f.waiting = append(f.waiting, done)
		mu.Unlock()
		return
	}
	f = &fetch{waiting: []func([]byte, error){done}}
	fetches[url] = f
	mu.Unlock()
	go func() {
		data, err := get(url)
		mu.Lock()
		f.done, f.data, f.err = true, data, err
		waiting := f.waiting
		f.waiting = nil
		mu.Unlock()
		for _, w := range waiting {
			w(data, err)
		}
		if arrived != nil {
			arrived()
		}
	}()
}

func get(url string) ([]byte, error) {
	resp, err := http.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: %s", url, resp.Status)
	}
	return io.ReadAll(resp.Body)
}

// Load is the decoded image url names, if it is at hand: a remote one not yet
// fetched answers ErrPending and starts the fetch, so a drawing that paints it
// shows it the next time it repaints.
func Load(url string) (image.Image, error) {
	if !IsRemote(url) {
		data, err := Carried(url)
		if err != nil {
			return nil, err
		}
		return Decode(data)
	}
	mu.Lock()
	f := fetches[url]
	var (
		data []byte
		err  = ErrPending
	)
	if f != nil && f.done {
		data, err = f.data, f.err
	}
	mu.Unlock()
	if f == nil {
		Fetch(url, func([]byte, error) {})
	}
	if err != nil {
		return nil, err
	}
	return Decode(data)
}

// Decode is the picture in data: PNG, JPEG or GIF.
func Decode(data []byte) (image.Image, error) {
	img, _, err := image.Decode(bytes.NewReader(data))
	return img, err
}

// Size is the picture in data's own size in pixels: a raster's, or an SVG's
// `width` and `height`, or its view box where it gives no size.
func Size(data []byte) (w, h int, ok bool) {
	if cfg, _, err := image.DecodeConfig(bytes.NewReader(data)); err == nil {
		return cfg.Width, cfg.Height, true
	}
	var root struct {
		Width   string `xml:"width,attr"`
		Height  string `xml:"height,attr"`
		ViewBox string `xml:"viewBox,attr"`
	}
	if xml.NewDecoder(bytes.NewReader(data)).Decode(&root) != nil {
		return 0, 0, false
	}
	w, h = svgLength(root.Width), svgLength(root.Height)
	if w > 0 && h > 0 {
		return w, h, true
	}
	if f := strings.Fields(strings.ReplaceAll(root.ViewBox, ",", " ")); len(f) == 4 {
		w, h = svgLength(f[2]), svgLength(f[3])
	}
	return w, h, w > 0 && h > 0
}

func svgLength(s string) int {
	f, err := strconv.ParseFloat(strings.TrimSuffix(strings.TrimSpace(s), "px"), 64)
	if err != nil {
		return 0
	}
	return int(f)
}
