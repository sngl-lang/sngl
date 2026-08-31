// Command sngl-fyne-worker renders a SNGL program in a Fyne window, driven
// over stdin and stdout.
//
// It is the out-of-process host: `sngl` interprets the program and sends
// patches, this opens the window and applies them. The split is not incidental.
// Linking Fyne into the compiler would make `sngl` a CGo/OpenGL binary that no
// longer cross-compiles and that the WASM playground could not import, and a
// worker built inside the user's own Go module is also the only thing that
// resolves their `replace` directives and pinned versions -- so what you see
// previewed is what you would ship.
//
// This one is hand-written and renders the widget set in fynehost.Default().
// A generated worker is the same main() with its registry emitted from the
// Specs its program can reach, which is what turns a widget the compiler never
// heard of into a rebuild rather than an impossibility.
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
	w.SetContent(host.Root)
	w.Resize(fyne.NewSize(float32(*width), float32(*height)))

	// The driver's stream is stdin and stdout together. Serving runs off the
	// Fyne thread, and Threaded is what hands each batch back onto it.
	go func() {
		err := snglhost.ServeHost(fynehost.Threaded(host), stdio{})
		if err != nil && err != io.EOF {
			fmt.Fprintf(os.Stderr, "sngl-fyne-worker: %v\n", err)
		}
		// The driver went away, so the window has nothing behind it.
		a.Quit()
	}()

	w.ShowAndRun()
}

// stdio is the driver's pipe: read from stdin, write to stdout. Diagnostics go
// to stderr, because anything on stdout is protocol.
type stdio struct{}

func (stdio) Read(p []byte) (int, error)  { return os.Stdin.Read(p) }
func (stdio) Write(p []byte) (int, error) { return os.Stdout.Write(p) }
func (stdio) Close() error                { return os.Stdin.Close() }
