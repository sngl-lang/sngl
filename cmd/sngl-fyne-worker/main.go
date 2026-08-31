// Command sngl-fyne-worker renders a SNGL program in a Fyne window, driven
// over stdin and stdout.
//
// Separate from `sngl` because linking Fyne into the compiler would make it a
// CGo/OpenGL binary: no cross-compilation, and the WASM playground could not
// import it. See cmd/sngl/deps_test.go.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"

	fyne "fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"

	"git.duckfam.us/jonathan/sngl/pkg/go/fynehost"
	"git.duckfam.us/jonathan/sngl/pkg/go/snglhost"
)

func main() {
	title := flag.String("title", "SNGL", "window title")
	width := flag.Float64("width", 480, "initial window width")
	height := flag.Float64("height", 360, "initial window height")
	flag.Parse()

	a := app.New()
	w := a.NewWindow(*title)

	host := fynehost.New(fynehost.Default())
	host.OnUnsupported = func(name string) {
		fmt.Fprintf(os.Stderr, "sngl: no fyne widget for %q; it and anything inside it are not rendered\n", name)
	}
	w.SetContent(host.Root)
	w.Resize(fyne.NewSize(float32(*width), float32(*height)))

	// The driver's stream is stdin and stdout together. Serving runs off the
	// Fyne thread, and Threaded is what hands each batch back onto it.
	go func() {
		err := snglhost.ServeHost(fynehost.Threaded(host), stdio{})
		if err != nil && err != io.EOF {
			fmt.Fprintf(os.Stderr, "sngl-fyne-worker: %v\n", err)
		}
		// The driver went away, so the window has nothing behind it. Quitting
		// touches the app, and this is not Fyne's thread.
		fyne.Do(a.Quit)
	}()

	w.ShowAndRun()
}

// stdio is the driver's pipe: read from stdin, write to stdout. Diagnostics go
// to stderr, because anything on stdout is protocol.
type stdio struct{}

func (stdio) Read(p []byte) (int, error)  { return os.Stdin.Read(p) }
func (stdio) Write(p []byte) (int, error) { return os.Stdout.Write(p) }
func (stdio) Close() error                { return os.Stdin.Close() }
