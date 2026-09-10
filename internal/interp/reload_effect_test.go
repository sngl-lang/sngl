package interp

import (
	"strings"
	"testing"
)

// reloadEffectSrc is one bracket and a log its handlers append to, so what ran
// across a reload is readable off the view.
//
// The brackets are written out rather than looped over a list, because a `var`
// is carried across a reload: editing the list's literal would leave the
// session running the value it already held and the tree would not move at all.
const reloadEffectSrc = `import . "sngl:ui"

component main node {
    var (
        log = ""
        extra = 0
    )

    effect(on="a", @mount { log = log + "+a" }, @unmount { log = log + "-a" })
    text #out(value="{log}|{extra}")
}
`

// secondBracket is reloadEffectSrc with a second bracket after the first.
const secondBracket = `    effect(on="b", @mount { log = log + "+b" }, @unmount { log = log + "-b" })
    text #out`

func withSecondBracket(src string) string {
	return strings.Replace(src, "    text #out", secondBracket, 1)
}

func logOf(t *testing.T, s *Session) string {
	t.Helper()
	out := s.View().Find("out")
	if len(out) != 1 {
		t.Fatalf("#out resolved to %d nodes", len(out))
	}
	v, _ := out[0].Props["value"].(string)
	return v
}

// A bracket the new source added begins its lifetime at the reload.
//
// Reload rebuilt the env, the timers and the view and left s.fx exactly as it
// was, so nothing ever reconciled the running set against the new tree: a
// bracket the edit introduced simply never mounted.
func TestReloadMountsABracketTheEditAdded(t *testing.T) {
	s := sessionFor(t, reloadEffectSrc, "main")
	if got := logOf(t, s); got != "+a|0" {
		t.Fatalf("initial log %q", got)
	}

	if _, err := s.Reload(check(t, withSecondBracket(reloadEffectSrc))); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	got := logOf(t, s)
	if !strings.Contains(got, "+b") {
		t.Errorf("log is %q; the bracket the edit added never mounted", got)
	}
	if strings.Contains(got, "-a") {
		t.Errorf("log is %q; the surviving bracket must not restart on a save", got)
	}
}

// A bracket the new source removed ends its lifetime at the reload.
//
// The running set was never asked what the new tree describes, so a bracket
// the edit deleted kept whatever it was holding for as long as the session
// lived -- and nothing could reach it again, since the only handle on it was
// the entry Reload walked past.
func TestReloadUnmountsABracketTheEditRemoved(t *testing.T) {
	s := sessionFor(t, withSecondBracket(reloadEffectSrc), "main")
	if got := logOf(t, s); got != "+a+b|0" {
		t.Fatalf("initial log %q", got)
	}

	if _, err := s.Reload(check(t, reloadEffectSrc)); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	if got := logOf(t, s); !strings.Contains(got, "-b") {
		t.Errorf("log is %q; the bracket the edit removed never unmounted", got)
	}
}

// A bracket that survives a reload runs the NEW source's unmount.
//
// The entry the running set held named the *ir.Func of the package the reload
// discarded, in a scope built from it, so a survivor kept a handler no line of
// the running program spells -- and that scope's symbols are ones the session
// no longer binds, so what the handler wrote reached nothing. Settling the set
// against the new package is what makes what is running be what the source
// says.
func TestReloadRepointsASurvivorAtTheNewSource(t *testing.T) {
	s := sessionFor(t, reloadEffectSrc, "main")
	if got := logOf(t, s); got != "+a|0" {
		t.Fatalf("initial log %q", got)
	}

	// Only the unmount body changes, so the bracket survives: same key, same
	// value keyed on, a different teardown.
	edited := strings.Replace(reloadEffectSrc, `log = log + "-a"`, `extra = 9`, 1)
	if _, err := s.Reload(check(t, edited)); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	if got := logOf(t, s); got != "+a|0" {
		t.Fatalf("after the reload the log reads %q; the survivor should not have moved", got)
	}

	// Now end that lifetime. The teardown that runs has to be the one the
	// running program declares, in the scope the running program binds.
	dropped := strings.Replace(edited, `    effect(on="a"`, `    //effect(on="a"`, 1)
	if _, err := s.Reload(check(t, dropped)); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	if got := logOf(t, s); got != "+a|9" {
		t.Errorf("log is %q, want \"+a|9\"; the survivor ran the discarded package's unmount", got)
	}
}
