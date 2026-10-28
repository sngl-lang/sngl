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

const reachAcrossChildEnv = "SNGL_GTK4RT_REACH_ACROSS_CHILD"

// A scroll moving sideways lays its content out at its natural width and
// reads its end along x: a row too short to scroll asks once, asks again as
// it grows sideways, and scrolls once it is wider than the scroll. Its end
// read along y, the row's height never moved and a page arriving asked for
// nothing more.
func TestReachEndOfAScrollMovingSidewaysIsItsRightEdge(t *testing.T) {
	if os.Getenv(reachAcrossChildEnv) == "" {
		if r := testutil.GUIRenderSkipReason(); r != "" {
			t.Skip(r)
		}
		cmd := exec.Command(os.Args[0], "-test.run=^"+t.Name()+"$", "-test.v")
		cmd.Env = append(os.Environ(), reachAcrossChildEnv+"=1")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("child failed: %v\n%s", err, out)
		}
		want := strings.Join([]string{
			"first: calls=1",
			"grown: calls=2",
			"wide: scrolls=true",
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
	row := BoxNew(OrientationHorizontal, 0)
	BoxAppend(row, LabelNew("column"))
	sw := ScrollNew()
	ScrollSetDirection(sw, "horizontal")
	ScrolledWindowSetChild(sw, row)
	calls := 0
	ScrollOnReachEnd(sw, func() { calls++ })
	win := ApplicationWindowNew(Handle(app))
	WindowSetDefaultSize(win, 400, 200)
	WindowSetChild(win, sw)
	WindowPresent(win)

	for i := 0; i < 100 && scrolls[sw].content < 0; i++ {
		PumpFor(20)
	}
	PumpFor(200)
	fmt.Printf("first: calls=%d\n", calls)

	BoxAppend(row, LabelNew("another column"))
	PumpFor(300)
	fmt.Printf("grown: calls=%d\n", calls)

	for range 40 {
		BoxAppend(row, LabelNew("a column wide enough to need room"))
	}
	PumpFor(300)
	upper, _, page := scrollMeasure(sw)
	fmt.Printf("wide: scrolls=%t\n", upper > page)
}
