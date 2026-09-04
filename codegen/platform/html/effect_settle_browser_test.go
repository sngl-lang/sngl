//go:build !js

package html

import (
	"testing"
)

// The lowered settle is graded here for the same reason effect_browser_test.go
// gives: the effect fixtures in testdata/ are asserted by the interpreter, and
// the interpreter does not lower. Each of these ran a *compiled* program that
// disagreed with it.

// Two brackets rekeyed by one write end both lifetimes before either begins.
//
// A settle per bracket, called in the order the brackets were written, gives
// -A,+A,-B,+B: B's new lifetime begins while A's old one is still running, and
// a program handing a resource between them holds two of it for that window.
// interp's Reconcile ends every dying lifetime first, newest first across the
// whole set, and this is that order written down.
func TestEffect_TwoPositionsEndBothBeforeEitherBegins(t *testing.T) {
	src := `
import . "sngl:ui"
import . "sngl:app"
component App() {
    var (
        k = 0
        log list<string> = []
    )
    button(text="rekey", @click { k = k + 1 })
    effect(on=k, @mount { log.push("+A") }, @unmount { log.push("-A") })
    effect(on=k, @mount { log.push("+B") }, @unmount { log.push("-B") })
    text(value="[" + log.join(",") + "]")
}
component main { window(title="H", href="/index.html") { App() } }
`
	b := startComponent(t, src)
	defer b.Close()
	page := b.Page()

	if got := effectLog(page.MustElement("body").MustText()); got != "[+A,+B]" {
		t.Fatalf("initial log = %s, want [+A,+B]", got)
	}
	page.MustElement("button").MustClick()
	page.MustWaitStable()
	if got := effectLog(page.MustElement("body").MustText()); got != "[+A,+B,-B,-A,+A,+B]" {
		t.Fatalf("after rekey log = %s, want [+A,+B,-B,-A,+A,+B]", got)
	}
}

// `l.push(x)` is the only form push has, and it is a call rather than an
// assignment. A settle injected only after `*ir.Assign` on a bare identifier
// saw nothing here, so the bracket the new element describes never mounted.
func TestEffect_PushMountsTheNewBracket(t *testing.T) {
	src := `
import . "sngl:ui"
import . "sngl:app"
component App() {
    var (
        items list<string> = ["a"]
        log list<string> = []
    )
    button(text="add", @click { items.push("b") })
    for var it = items {
        effect(on=it, @mount(v) { log.push("+" + v) }, @unmount(v) { log.push("-" + v) })
    }
    text(value="[" + log.join(",") + "]")
}
component main { window(title="H", href="/index.html") { App() } }
`
	b := startComponent(t, src)
	defer b.Close()
	page := b.Page()

	if got := effectLog(page.MustElement("body").MustText()); got != "[+a]" {
		t.Fatalf("initial log = %s, want [+a]", got)
	}
	page.MustElement("button").MustClick()
	page.MustWaitStable()
	if got := effectLog(page.MustElement("body").MustText()); got != "[+a,+b]" {
		t.Fatalf("after push log = %s, want [+a,+b]", got)
	}
}

// A write to a field of a state struct is a write to that state. The settle
// used to be injected only where the assignment target was a bare identifier,
// so `key.n = 1` moved the bracket's key and settled nothing.
func TestEffect_FieldWriteRekeys(t *testing.T) {
	src := `
import . "sngl:ui"
import . "sngl:app"
struct counter {
    n int
}
component App() {
    var (
        key counter = counter{n = 0}
        log list<string> = []
    )
    button(text="bump", @click { key.n = key.n + 1 })
    effect(on=key.n, @mount { log.push("m") }, @unmount { log.push("u") })
    text(value="[" + log.join(",") + "]")
}
component main { window(title="H", href="/index.html") { App() } }
`
	b := startComponent(t, src)
	defer b.Close()
	page := b.Page()

	if got := effectLog(page.MustElement("body").MustText()); got != "[m]" {
		t.Fatalf("initial log = %s, want [m]", got)
	}
	page.MustElement("button").MustClick()
	page.MustWaitStable()
	if got := effectLog(page.MustElement("body").MustText()); got != "[m,u,m]" {
		t.Fatalf("after a field write log = %s, want [m,u,m]", got)
	}
}

// A key written as a call reads whatever the callee reads. The pass used to
// take the identifiers the `on` expression itself spelled, and a call spells
// none -- so the bracket depended on nothing and never ran again.
func TestEffect_ACalledKeyRekeys(t *testing.T) {
	src := `
import . "sngl:ui"
import . "sngl:app"
var (
    n = 0
    log list<string> = []
)
func scaled(m int) => m * n
component App() {
    button(text="bump", @click { n = n + 1 })
    effect(on=scaled(2), @mount { log.push("m") }, @unmount { log.push("u") })
    text(value="[" + log.join(",") + "]")
}
component main { window(title="H", href="/index.html") { App() } }
`
	b := startComponent(t, src)
	defer b.Close()
	page := b.Page()

	if got := effectLog(page.MustElement("body").MustText()); got != "[m]" {
		t.Fatalf("initial log = %s, want [m]", got)
	}
	page.MustElement("button").MustClick()
	page.MustWaitStable()
	if got := effectLog(page.MustElement("body").MustText()); got != "[m,u,m]" {
		t.Fatalf("after bump log = %s, want [m,u,m]", got)
	}
}

// A mount handler that rekeys the bracket through an ordinary function settles
// to a fixpoint, the way interp's Settle does.
//
// The guard used to be a test of the callee's *name*, so one hop through a
// helper defeated it: settle -> mount -> bump -> settle, with the running list
// written only at the very end, so every nested call saw the stale one and
// recursed. The page renders nothing at all under that build -- the script
// throws before the first paint -- which is what the initial log below reports.
//
// n rekeys once and then stops, so the settled answer is the interpreter's
// m,u,m: the mount at key 0, its ending, and the mount at key 1.
func TestEffect_AHelperRekeyingFromMountSettles(t *testing.T) {
	src := `
import . "sngl:ui"
import . "sngl:app"
var (
    n = 0
    log list<string> = []
)
func bump() {
    n = n + 1
}
component App() {
    effect(on=n, @mount {
        log.push("m")
        if n < 1 {
            bump()
        }
    }, @unmount { log.push("u") })
    text(value="[" + log.join(",") + "]")
}
component main { window(title="H", href="/index.html") { App() } }
`
	b := startComponent(t, src)
	defer b.Close()
	page := b.Page()

	if got := effectLog(page.MustElement("body").MustText()); got != "[m,u,m]" {
		t.Fatalf("settled log = %s, want [m,u,m]", got)
	}
}

// An effect that rekeys itself unconditionally stops at the bound rather than
// running out of stack, and says so: the settle raises when it leaves its loop
// with a pass still owed. What is asserted here is the other half -- that the
// page is alive and the log is the bound and not a crash -- because the raise
// happens after the last mount has already patched the text. The raise itself
// is pinned in testdata/effect_settle_sites.txtar, where its exact form on each
// target is visible.
func TestEffect_ASelfRekeyingBracketStopsAtTheBound(t *testing.T) {
	src := `
import . "sngl:ui"
import . "sngl:app"
var (
    n = 0
    runs = 0
)
func bump() {
    n = n + 1
}
component App() {
    effect(on=n, @mount {
        runs += 1
        bump()
    })
    text(value="[" + "{runs}" + "]")
}
component main { window(title="H", href="/index.html") { App() } }
`
	b := startComponent(t, src)
	defer b.Close()
	page := b.Page()

	if got := effectLog(page.MustElement("body").MustText()); got != "[512]" {
		t.Fatalf("runs after the bound = %s, want [512]", got)
	}
}
