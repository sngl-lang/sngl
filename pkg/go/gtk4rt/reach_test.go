//go:build !js

package gtk4rt

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"

	"duckfam.us/sngl/internal/testutil"
)

const reachChildEnv = "SNGL_GTK4RT_REACH_CHILD"

// A scroll's @reachEnd is posted to idle, not called while GTK allocates, and
// is asked again when the content grows while its end shows -- however long
// after the report the next page arrives. fyne's ReachScroll is held to the
// same rule (pkg/go/fynelayout/scroll_test.go).
//
// In a process of its own, because it pumps GLib's default context with a
// window on screen.
func TestReachEndIsPostedAndAsksAgainAsTheContentGrows(t *testing.T) {
	if os.Getenv(reachChildEnv) == "" {
		if r := testutil.GUIRenderSkipReason(); r != "" {
			t.Skip(r)
		}
		cmd := exec.Command(os.Args[0], "-test.run=^"+t.Name()+"$", "-test.v")
		cmd.Env = append(os.Environ(), reachChildEnv+"=1")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("child failed: %v\n%s", err, out)
		}
		want := strings.Join([]string{
			"first: calls=1",
			"measured: calls=1",
			"pumped: calls=2",
			"pumped again: calls=2",
			"grown: calls=3",
		}, "\n")
		if !strings.Contains(string(out), want) {
			t.Fatalf("want the steps\n%s\ngot\n%s", want, out)
		}
		return
	}

	runtime.LockOSThread()
	Init()
	app, err := snapshotApplication()
	if err != nil {
		t.Skipf("no application: %v", err)
	}
	list := BoxNew(OrientationVertical, 0)
	BoxAppend(list, LabelNew("row"))
	sw := ScrollNew()
	ScrolledWindowSetChild(sw, list)
	calls := 0
	ScrollOnReachEnd(sw, func() { calls++ })
	win := ApplicationWindowNew(Handle(app))
	WindowSetDefaultSize(win, 200, 400)
	WindowSetChild(win, sw)
	WindowPresent(win)

	// Allocated: a first page shorter than the scroll asks once.
	for i := 0; i < 100 && scrolls[sw].content < 0; i++ {
		PumpFor(20)
	}
	PumpFor(200)
	fmt.Printf("first: calls=%d\n", calls)

	// A measure made while GTK allocates is this call: the report it makes
	// is posted, and runs only once the loop does.
	scrolls[sw].near = false
	scrolls[sw].changed(sw)
	fmt.Printf("measured: calls=%d\n", calls)
	PumpFor(200)
	fmt.Printf("pumped: calls=%d\n", calls)

	// Nothing grew: nothing more is asked.
	PumpFor(200)
	fmt.Printf("pumped again: calls=%d\n", calls)

	// The next page arrives long after the report, its end still showing.
	BoxAppend(list, LabelNew("another row"))
	PumpFor(300)
	fmt.Printf("grown: calls=%d\n", calls)
}
