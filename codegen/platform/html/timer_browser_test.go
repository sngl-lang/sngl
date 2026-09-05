//go:build !js

package html

import (
	"strings"
	"testing"
	"time"
)

// waitForText polls the page until the body reports what the caller is waiting
// for, and reports what it last saw when it does not. A timer is real elapsed
// time in a browser, so an assertion made once is a race.
func waitForText(t *testing.T, read func() string, want func(string) bool, what string) string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	last := ""
	for time.Now().Before(deadline) {
		last = read()
		if want(last) {
			return last
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s; the page reads %q", what, last)
	return last
}

// A timer fires in a real browser, and a timer written in a child component
// fires too.
//
// `timer` is an ordinary component now: html overrides it with its own
// primitive, passTimerPrimitive takes that out of the tree, and the emitted
// script arms setInterval. Nothing about that is visible in a page that merely
// renders, which is why this drives Chrome rather than reading the output.
func TestTimer_FiresInTheBrowser(t *testing.T) {
	src := `
import . "sngl:ui"
import . "sngl:app"
import . "sngl:time"
component beat(label = "") {
    var beats = 0
    timer(interval=20ms, enabled=true, @tick { beats += 1 })
    text(value="{label}=" + string(beats))
}
component App() {
    var seconds = 0
    timer(interval=20ms, enabled=true, @tick { seconds += 1 })
    beat(label="child")
    text(value="root=" + string(seconds))
}
component main { window(title="H", href="/index.html") { App() } }
`
	b := startComponent(t, src)
	defer b.Close()
	page := b.Page()
	read := func() string { return page.MustElement("body").MustText() }

	waitForText(t, read, func(s string) bool {
		return !strings.Contains(s, "root=0")
	}, "the root component's timer to fire")
	waitForText(t, read, func(s string) bool {
		return !strings.Contains(s, "child=0")
	}, "the child component's timer to fire")
}

// The gate is the position the override places its primitive at, so a timer
// under a branch that is not rendering is not armed -- and arming it when the
// branch appears, and clearing it when the branch goes, is the same question.
//
// The gate used to reach the emitter as a bare variable name, so a literal
// `enabled=true` and a gate folded from an enclosing `if` both reduced to
// nothing, which the emitter read as "always on".
func TestTimer_ABranchIsTheGate(t *testing.T) {
	src := `
import . "sngl:ui"
import . "sngl:app"
import . "sngl:time"
component App() {
    var (
        shown = false
        n = 0
    )
    button(text="toggle", @click { shown = !shown })
    if shown {
        timer(interval=20ms, enabled=true, @tick { n += 1 })
    }
    text(value="n=" + string(n))
}
component main { window(title="H", href="/index.html") { App() } }
`
	b := startComponent(t, src)
	defer b.Close()
	page := b.Page()
	read := func() string { return page.MustElement("body").MustText() }

	// Nothing is armed while the branch is absent.
	time.Sleep(200 * time.Millisecond)
	if got := read(); !strings.Contains(got, "n=0") {
		t.Fatalf("a timer under a false branch fired: %q", got)
	}

	btn := page.MustElement("button")
	btn.MustClick()
	waitForText(t, read, func(s string) bool {
		return !strings.Contains(s, "n=0")
	}, "the timer to arm when its branch appears")

	// And it stops when the branch goes.
	btn.MustClick()
	stopped := read()
	time.Sleep(200 * time.Millisecond)
	if got := read(); got != stopped {
		t.Errorf("a timer kept firing after its branch went away: %q then %q", stopped, got)
	}
}
