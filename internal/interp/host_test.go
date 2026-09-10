package interp

import (
	"strings"
	"testing"
)

const hostSrc = `import . "sngl:ui"
import . "sngl:time"

component main node {
    var (
        count = 0
        shown = false
        items = ["a", "b", "c"]
    )
    vbox {
        text #out(value="count {count}")
        button #inc(text="+", @click { count += 1 })
        if shown {
            hbox {
                text #deep(value="nested")
            }
        }
        for var it = items {
            text(value=it, key=it)
        }
    }
}
`

// tracks asserts the invariant the whole patch protocol exists to keep: a host
// that applied every patch holds exactly the tree the session holds.
//
// If this ever fails, the fault is in Diff or Apply and not in anyone's
// toolkit -- which is the reason to have a host made of maps at all.
func tracks(t *testing.T, step string, h *MemHost, s *Session) {
	t.Helper()
	want, got := RenderView(s.View()), h.String()
	if want != got {
		t.Errorf("after %s the host tree diverged\n--- session\n%s--- host\n%s", step, want, got)
	}
}

func step(t *testing.T, name string, h *MemHost, s *Session, patches []Patch, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	if err := Apply(h, patches); err != nil {
		t.Fatalf("%s: applying: %v", name, err)
	}
	tracks(t, name, h, s)
}

// TestTheHostTreeTracksTheSessionAcrossEveryOperation drives one host through
// the whole vocabulary -- create, assign, remove, move, rebind -- and checks
// the invariant after each.
func TestTheHostTreeTracksTheSessionAcrossEveryOperation(t *testing.T) {
	s := sessionFor(t, hostSrc, "main")
	h := NewMemHost()
	if err := s.Attach(h); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	tracks(t, "attach", h, s)
	if h.Batches != 1 {
		t.Errorf("attach took %d batches, want 1", h.Batches)
	}

	// An event: one prop assigned.
	inc := s.View().Find("inc")[0].Key
	p, err := s.Invoke(inc, "click")
	step(t, "click", h, s, p, err)

	// A tick with nothing scheduled: the invariant has to hold across an
	// operation that changes nothing. What a timer's patches do to the host
	// tree is internal/interp/timertest's, since a schedule needs the platform
	// that declares the primitive registered.
	p, err = s.Tick()
	step(t, "tick", h, s, p, err)

	// A branch appearing: creations, parent before child.
	setVar(s.Env, "shown", true)
	p, err = s.Sync()
	step(t, "if on", h, s, p, err)
	if len(h.Find("deep")) != 1 {
		t.Errorf("#deep is not mounted on the host")
	}

	// A keyed reorder: movement, not re-assignment.
	setVar(s.Env, "items", []any{"c", "a", "b"})
	p, err = s.Sync()
	step(t, "reorder", h, s, p, err)
	for _, patch := range p {
		if patch.Kind != PatchMove {
			t.Errorf("reorder produced %s, want moves only", patch)
		}
	}

	// A branch disappearing: removals, and the subtree goes with it.
	setVar(s.Env, "shown", false)
	p, err = s.Sync()
	step(t, "if off", h, s, p, err)
	if len(h.Find("deep")) != 0 {
		t.Errorf("#deep survived the removal of its parent")
	}

	// A reload that edits a literal and adds a handler.
	edited := strings.Replace(hostSrc, `value="count {count}"`, `value="count = {count}"`, 1)
	edited = strings.Replace(edited, `text #out(value="count = {count}")`,
		`text #out(value="count = {count}", @click { count += 100 })`, 1)
	p, err = s.Reload(check(t, edited))
	step(t, "reload", h, s, p, err)

	// The rebind reached the host, so the event it now reports is bindable.
	out := h.Find("out")
	if len(out) != 1 || len(out[0].Events) != 1 || out[0].Events[0] != "click" {
		t.Errorf("host has events %v for #out, want [click]", out[0].Events)
	}
	// And the handler the reload introduced actually runs.
	p, err = s.Invoke(s.View().Find("out")[0].Key, "click")
	step(t, "click the new handler", h, s, p, err)
	if got := h.Find("out")[0].Props["value"]; got != "count = 101" {
		t.Errorf("value is %v, want \"count = 101\"", got)
	}
}

// TestApplyIsOneBatchPerDiff: a host defers layout until End, so a diff that
// is one logical change must not arrive as several batches.
func TestApplyIsOneBatchPerDiff(t *testing.T) {
	s := sessionFor(t, hostSrc, "main")
	h := NewMemHost()
	if err := s.Attach(h); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	before := h.Batches
	setVar(s.Env, "shown", true)
	p, err := s.Sync()
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if len(p) < 2 {
		t.Fatalf("expected several patches, got %d", len(p))
	}
	if err := Apply(h, p); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if h.Batches != before+1 {
		t.Errorf("%d patches arrived as %d batches, want 1", len(p), h.Batches-before)
	}
}

// TestAnEmptyDiffTouchesTheHostNotAtAll: an idle loop must not even open a
// batch, or a host doing work per batch pays for nothing happening.
func TestAnEmptyDiffTouchesTheHostNotAtAll(t *testing.T) {
	s := sessionFor(t, hostSrc, "main")
	h := NewMemHost()
	if err := s.Attach(h); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	before := h.Batches
	p, err := s.Sync()
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if err := Apply(h, p); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if h.Batches != before {
		t.Errorf("an empty diff opened a batch")
	}
}

// TestNoIRReachesTheHost is the contract the out-of-process host depends on.
// A NodeDesc must be serialisable, which means it must not carry a pointer
// into the program.
func TestNoIRReachesTheHost(t *testing.T) {
	s := sessionFor(t, hostSrc, "main")
	n := s.View().Find("inc")[0]
	if n.Inst == nil || n.Env == nil {
		t.Fatal("the node under test carries no IR, so this proves nothing")
	}
	d := n.Desc()
	if d.Key != n.Key || d.Name != n.Name || d.ID != "inc" {
		t.Errorf("Desc lost the node's identity: %+v", d)
	}
	if len(d.Events) != 1 || d.Events[0] != "click" {
		t.Errorf("Desc has events %v, want [click]", d.Events)
	}
	// Every prop value must be a plain value, not something holding a scope.
	for _, p := range d.Props {
		switch p.Value.(type) {
		case *Env, *Node, *LambdaValue:
			t.Errorf("prop %q carries %T into the host", p.Name, p.Value)
		}
	}
}

// TestAUserComponentIsTransparentToTheHost: a toolkit has a widget for `vbox`
// and none for `readout.Readout`. The tree keeps the instantiation as a node
// because an inspector wants it, but a host asked to build one gets an element
// it cannot make -- and everything inside goes down with it.
//
// This is what made examples/calculator render an empty window.
func TestAUserComponentIsTransparentToTheHost(t *testing.T) {
	src := `import . "sngl:ui"

component panel(label string) node {
    vbox {
        text #inner(value=label)
    }
}

component main node {
    vbox #outer {
        panel(label="hi")
    }
}
`
	s := sessionFor(t, src, "main")
	h := NewMemHost()
	if err := s.Attach(h); err != nil {
		t.Fatalf("Attach: %v", err)
	}

	// The component contributes no node of its own...
	if got := strings.Count(h.String(), "panel"); got != 0 {
		t.Errorf("the host was asked to build %d panel widgets:\n%s", got, h.String())
	}
	// ...and what its body rendered took its place, beneath the real parent.
	if len(h.Find("inner")) != 1 {
		t.Errorf("#inner did not reach the host:\n%s", h.String())
	}
	outer := h.Find("outer")
	if len(outer) != 1 || len(outer[0].Children) != 1 {
		t.Fatalf("#outer holds %v, want the panel's vbox hoisted into it:\n%s", outer, h.String())
	}
	tracks(t, "attach with a user component", h, s)
}
