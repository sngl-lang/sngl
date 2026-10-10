package webtest

import (
	"fmt"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/launcher"
)

// Within runs f, failing with step's name when it returns an error or has
// not returned after d. A step that times out keeps running in the
// background; its caller tears down the process it was waiting on.
func Within[T any](step string, d time.Duration, f func() (T, error)) (T, error) {
	type result struct {
		v   T
		err error
	}
	done := make(chan result, 1)
	go func() {
		v, err := f()
		done <- result{v, err}
	}()
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case r := <-done:
		if r.err != nil {
			return r.v, fmt.Errorf("%s: %w", step, r.err)
		}
		return r.v, nil
	case <-timer.C:
		var zero T
		return zero, fmt.Errorf("%s: no answer within %v", step, d)
	}
}

// CloseBrowser closes browser, waits for the process l launched to exit and
// removes its profile directory, each step bounded by d. A browser that was
// never connected to (nil) is killed rather than asked, as is one that does
// not close or exit in time: rod's Cleanup waits for the exit with no bound,
// and a profile left behind is one more directory under the temp dir's rod/.
//
// A launch that started no process leaves nothing to close.
func CloseBrowser(l *launcher.Launcher, browser *rod.Browser, d time.Duration) {
	if l == nil || l.PID() == 0 {
		return
	}
	cleanup := func() (struct{}, error) {
		l.Cleanup()
		return struct{}{}, nil
	}
	if browser != nil {
		_, _ = Within("close browser", d, func() (struct{}, error) {
			return struct{}{}, browser.Close()
		})
		if _, err := Within("wait for browser to exit", d, cleanup); err == nil {
			return
		}
	}
	l.Kill()
	_, _ = Within("wait for killed browser to exit", d, cleanup)
}
