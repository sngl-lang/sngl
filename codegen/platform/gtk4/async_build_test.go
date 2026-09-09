package gtk4

import (
	"strings"
	"testing"
)

// asyncOffloadSrc is the same program fyne runs: a call statement that waits
// and an assignment whose answer arrives late, with a state write on either
// side of them.
const asyncOffloadSrc = `
import . "sngl:ui"
import go "sngl:language/go"

#[go.native("time", "time.Sleep")]
#[go.async]
func sleep(ns int)

#[go.native("os", "os.Hostname", fails)]
#[go.async]
func host() string

component main ui {
    var (
        greeting = "idle"
        busy = false
    )

    text #out(value=greeting)
    button #load(text="load", @click {
        busy = true
        sleep(300000000)
        greeting = host()
        busy = false
    })
}
`

// TestABlockingCallCompilesIntoAPostedUpdate builds rather than runs, as every
// gtk4 harness does -- a widget needs a display. What it can still establish is
// that the pieces fit: the goroutine, the gtk4rt.Post around the write-back,
// and the label setter *inside* that closure rather than beside it.
//
// The last one is the assertion with teeth. WalkLowered had no reason to look
// inside a call's arguments, so the two closures reached the Go backend raw and
// the widget write came out as its bare IR shape, naming a field the render
// function does not declare. That does not compile, which is what the build
// catches.
func TestABlockingCallCompilesIntoAPostedUpdate(t *testing.T) {
	skipWithoutGIR(t)
	model := generateGTK4ModelBuilt(t, asyncOffloadSrc)

	for _, want := range []string{
		"go func() {",
		"gtk4rt.Post(func() {",
		"gtk4rt.LabelSetText(",
	} {
		if !strings.Contains(model, want) {
			t.Errorf("emitted model has no %q\n--- model.go ---\n%s", want, model)
		}
	}
	buildGTK4Model(t, "gtk4-async-offload-", model)
}
