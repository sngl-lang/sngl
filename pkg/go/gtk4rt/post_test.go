//go:build !js

package gtk4rt

import (
	"runtime"
	"sync/atomic"
	"testing"
	"time"
)

// Post is what a blocking call's answer comes back through on gtk4, and until
// this test it had no caller anywhere in the repository -- so nothing had ever
// established that the idle source fires the Go callback at all.
//
// The claim is specifically that it goes *through the loop*: the callback must
// not run on the goroutine that posted it, and must run once the loop is
// pumped. Both halves are asserted, because a Post that simply called fn would
// pass a test that only checked it ran. No display is involved -- an idle
// source is GLib, not GTK -- so this runs wherever the runtime compiles.
func TestPostFiresOnTheMainLoopAndNotBefore(t *testing.T) {
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
