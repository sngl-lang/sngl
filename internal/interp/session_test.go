package interp

import (
	"strings"
	"testing"
)

func sessionFor(t *testing.T, src, comp string) *Session {
	t.Helper()
	s, err := NewSession(check(t, src), comp, NewVirtual())
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	return s
}

const sessionSrc = `import . "sngl:ui"
import . "sngl:time"

component main node {
    var (
        count = 0
        label = "start"
    )
    vbox {
        text #out(value="{label}: {count}")
        button #inc(text="+", @click { count += 1 })
    }
}
`

// TestInvokeRunsTheHandlerAndReportsTheChange is the loop a window runs:
// an event arrives, state moves, and what changed comes back as patches.
func TestInvokeRunsTheHandlerAndReportsTheChange(t *testing.T) {
	s := sessionFor(t, sessionSrc, "main")
	out := s.View().Find("out")
	if len(out) != 1 {
		t.Fatalf("#out resolved to %d nodes", len(out))
	}
	if got := out[0].Props["value"]; got != "start: 0" {
		t.Fatalf("initial value %v", got)
	}

	inc := s.View().Find("inc")
	patches, err := s.Invoke(inc[0].Key, "click")
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if len(patches) != 1 || patches[0].Kind != PatchSetProp {
		t.Fatalf("want one setprop, got:\n%s", patchLines(patches))
	}
	if patches[0].Value != "start: 1" {
		t.Errorf("patched to %v, want \"start: 1\"", patches[0].Value)
	}
	// And the session's own view moved with it, so the next diff is against
	// what the host now holds.
	if got := s.View().Find("out")[0].Props["value"]; got != "start: 1" {
		t.Errorf("session view still reads %v", got)
	}
}

// TestASecondSyncWithNoChangeIsEmpty: an idle loop must not repaint.
func TestASecondSyncWithNoChangeIsEmpty(t *testing.T) {
	s := sessionFor(t, sessionSrc, "main")
	if _, err := s.Invoke(s.View().Find("inc")[0].Key, "click"); err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	patches, err := s.Sync()
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if len(patches) != 0 {
		t.Errorf("idle sync produced %d patches:\n%s", len(patches), patchLines(patches))
	}
}

// TestReloadCarriesStateAndPatchesTheEdit is the hot-reload case end to end:
// a running program, a file saved with one literal changed, and neither the
// window nor the state it holds is thrown away.
func TestReloadCarriesStateAndPatchesTheEdit(t *testing.T) {
	s := sessionFor(t, sessionSrc, "main")
	if _, err := s.Invoke(s.View().Find("inc")[0].Key, "click"); err != nil {
		t.Fatalf("Invoke: %v", err)
	}

	edited := strings.Replace(sessionSrc, `value="{label}: {count}"`, `value="{label} = {count}"`, 1)
	patches, err := s.Reload(check(t, edited))
	if err != nil {
		t.Fatalf("Reload: %v", err)
	}
	if len(patches) != 1 || patches[0].Kind != PatchSetProp {
		t.Fatalf("want one setprop, got %d:\n%s", len(patches), patchLines(patches))
	}
	// The count survived the reload: 1, not the initialiser's 0.
	if patches[0].Value != "start = 1" {
		t.Errorf("patched to %v; state was not carried across the reload", patches[0].Value)
	}
}

// TestReloadReinitialisesAVarWhoseTypeChanged: carrying a value across a type
// change leaves a session holding something no expression could have produced.
func TestReloadReinitialisesAVarWhoseTypeChanged(t *testing.T) {
	s := sessionFor(t, sessionSrc, "main")
	if _, err := s.Invoke(s.View().Find("inc")[0].Key, "click"); err != nil {
		t.Fatalf("Invoke: %v", err)
	}

	retyped := strings.Replace(sessionSrc, "count = 0", `count = "zero"`, 1)
	retyped = strings.Replace(retyped, "count += 1", `count = "one"`, 1)
	retyped = strings.Replace(retyped, "count += 10", `count = "ten"`, 1)
	if _, err := s.Reload(check(t, retyped)); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	if got := s.View().Find("out")[0].Props["value"]; got != "start: zero" {
		t.Errorf("value is %v; a retyped var must be reinitialised, not carried", got)
	}
}

// TestReloadRunsTheInitialiserForANewVar.
func TestReloadRunsTheInitialiserForANewVar(t *testing.T) {
	s := sessionFor(t, sessionSrc, "main")
	added := strings.Replace(sessionSrc, `label = "start"`, "label = \"start\"\n        extra = 7", 1)
	added = strings.Replace(added, `value="{label}: {count}"`, `value="{label}: {count}: {extra}"`, 1)
	if _, err := s.Reload(check(t, added)); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	if got := s.View().Find("out")[0].Props["value"]; got != "start: 0: 7" {
		t.Errorf("value is %v, want the new var initialised to 7", got)
	}
}

// firstTimerKey is the mounted path of the session's first scheduled timer. A
// timer is keyed on where it is written now, the same way an effect is, so
// there is no positional key to construct.
func firstTimerKey(t *testing.T, s *Session) Key {
	t.Helper()
	if len(s.Timers.entries) == 0 {
		t.Fatal("the session has no timer scheduled")
	}
	return s.Timers.entries[0].Key
}

// TestInvokeRunsUnderTheProviderTheHandlerIsWrittenUnder: a window's event
// arrives after the mount has unwound every provider, so the handler has to
// carry the values it was mounted beneath.
func TestInvokeRunsUnderTheProviderTheHandlerIsWrittenUnder(t *testing.T) {
	s := sessionFor(t, `import . "sngl:ui"

context #depth(1)

func show() => "d{depth}"

component main node {
    var got = ""
    depth(5) {
        button #tap(text="tap", @click { got = show() })
    }
    text #out(value=got)
}
`, "main")
	if _, err := s.Invoke(s.View().Find("tap")[0].Key, "click"); err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if got := s.View().Find("out")[0].Props["value"]; got != "d5" {
		t.Errorf("handler read %v, want d5", got)
	}
}
