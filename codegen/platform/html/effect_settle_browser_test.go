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
component App() node {
    var (
        k = 0
        log list<string> = []
    )
    button(text="rekey", @click { k = k + 1 })
    effect(on=k, @mount { log.push("+A") }, @unmount { log.push("-A") })
    effect(on=k, @mount { log.push("+B") }, @unmount { log.push("-B") })
    text(value="[" + log.join(",") + "]")
}
window(title="H", href="/index.html") { App() }
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
component App() node {
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
window(title="H", href="/index.html") { App() }
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
struct counter {
    n int
}
component App() node {
    var (
        key counter = counter{n = 0}
        log list<string> = []
    )
    button(text="bump", @click { key.n = key.n + 1 })
    effect(on=key.n, @mount { log.push("m") }, @unmount { log.push("u") })
    text(value="[" + log.join(",") + "]")
}
window(title="H", href="/index.html") { App() }
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
var (
    n = 0
    log list<string> = []
)
func scaled(m int) => m * n
component App() node {
    button(text="bump", @click { n = n + 1 })
    effect(on=scaled(2), @mount { log.push("m") }, @unmount { log.push("u") })
    text(value="[" + log.join(",") + "]")
}
window(title="H", href="/index.html") { App() }
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

// Two brackets rekeying EACH OTHER stop at the bound rather than running out of
// stack, and say so.
//
// This file used to hold two tests of one bracket rekeying ITSELF: one that it
// settled to a fixpoint through a helper, one that it stopped at the bound.
// Neither program compiles now -- an effect's own handler may not write what its
// own `on` reads -- so the fixpoint test is gone and this is what is left of the
// bound: A's mount writes what B is keyed on, B's mount writes what A is keyed
// on, and neither writes its own key, so no per-effect rule catches it. It is
// the better evidence anyway, since it is exactly the case the bound has to
// survive the checker's rule for.
//
// The count is 1024 rather than 512: a pass of the group runs both brackets, so
// the two handlers between them run twice per pass. The text is the count as it
// stood when the last mount patched it, which is before the settle raises -- the
// raise itself is pinned in testdata/effect_settle_sites.txtar, where its exact
// form on each target is visible.
func TestEffect_MutuallyRekeyingBracketsStopAtTheBound(t *testing.T) {
	src := `
import . "sngl:ui"
var (
    x = 0
    y = 0
    runs = 0
)
component App() node {
    effect(on=x, @mount {
        runs += 1
        y = y + 1
    })
    effect(on=y, @mount {
        runs += 1
        x = x + 1
    })
    text(value="[" + "{runs}" + "]")
}
window(title="H", href="/index.html") { App() }
`
	b := startComponent(t, src)
	defer b.Close()
	page := b.Page()

	if got := effectLog(page.MustElement("body").MustText()); got != "[1024]" {
		t.Fatalf("runs after the bound = %s, want [1024]", got)
	}
}
