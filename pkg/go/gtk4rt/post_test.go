//go:build !js

package gtk4rt

import (
	"os"
	"os/exec"
	"runtime"
	"sync/atomic"
	"testing"
	"time"
)

// postChildEnv marks the process that runs the assertions rather than spawning
// one.
const postChildEnv = "SNGL_GTK4RT_POST_CHILD"

// Post is what a blocking call's answer comes back through on gtk4, and until
// this test it had no caller anywhere in the repository -- so nothing had ever
// established that the idle source fires the Go callback at all.
//
// The claim is specifically that it goes *through the loop*: the callback must
// not run on the goroutine that posted it, and must run once the loop is
// pumped. Both halves are asserted, because a Post that simply called fn would
// pass a test that only checked it ran.
//
// It runs in a process of its own because the loop it pumps is GLib's global
// default context, and that is the same context GTK puts a window's frame
// clock on: after TestSnapshotRender has presented one, pumping dispatches a
// render, and on a machine with no GL that aborts the process inside libepoxy
// rather than failing a test. Nothing here needs GTK -- an idle source is
// GLib -- so the isolation costs a fork and buys a context nobody else has
// touched.
func TestPostFiresOnTheMainLoopAndNotBefore(t *testing.T) {
	if os.Getenv(postChildEnv) == "" {
		cmd := exec.Command(os.Args[0], "-test.run=^"+t.Name()+"$", "-test.v")
		cmd.Env = append(os.Environ(), postChildEnv+"=1")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("the idle source did not deliver in a process of its own: %v\n%s", err, out)
		}
		return
	}

	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	mainLoopNew()

	var ran atomic.Bool
	posted := make(chan struct{})
	go func() {
		Post(func() {
			ran.Store(true)
			mainLoopQuit()
		})
		close(posted)
	}()
	<-posted

	// Nothing is pumping the loop yet.
	time.Sleep(100 * time.Millisecond)
	if ran.Load() {
		t.Fatal("the callback ran with no main loop running; Post is not going through the idle source")
	}

	// A loop that never quits would hang the suite rather than fail it.
	go func() {
		time.Sleep(10 * time.Second)
		mainLoopQuit()
	}()
	mainLoopRun()

	if !ran.Load() {
		t.Fatal("the loop ran and quit without the posted callback firing")
	}
}
