package fynehost_test

import (
	"testing"

	fynetest "fyne.io/fyne/v2/test"

	"git.duckfam.us/jonathan/sngl/internal/interp"
	"git.duckfam.us/jonathan/sngl/pkg/go/fynehost"
	"git.duckfam.us/jonathan/sngl/pkg/go/snglhost"
)

// recorder notes the order ops were applied in, so the deferral can be checked
// rather than assumed.
type recorder struct {
	*snglhost.MemHost
	seen []string
}

func (r *recorder) Create(d snglhost.NodeDesc, p snglhost.Key, i int) error {
	r.seen = append(r.seen, "create "+d.Name)
	return r.MemHost.Create(d, p, i)
}

func (r *recorder) SetProp(k snglhost.Key, prop string, v any) error {
	r.seen = append(r.seen, "setprop "+prop)
	return r.MemHost.SetProp(k, prop, v)
}

// TestABatchIsAppliedOnFynesThreadInOrder covers both halves of Threaded: the
// ops are deferred until End, and they run in the order they were queued.
//
// fyne.Do is asynchronous, so ops posted one at a time could land out of order
// against a tree where order is the whole point of a Move.
func TestABatchIsAppliedOnFynesThreadInOrder(t *testing.T) {
	app := fynetest.NewApp()
	t.Cleanup(app.Quit)

	rec := &recorder{MemHost: snglhost.NewMemHost()}
	h := fynehost.Threaded(rec)

	key := snglhost.Key{Comp: "main", Path: "text@0"}
	h.Begin()
	if err := h.Create(snglhost.NodeDesc{Key: key, Name: "text", ID: "t"}, snglhost.Key{}, 0); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := h.SetProp(key, "value", "hi"); err != nil {
		t.Fatalf("SetProp: %v", err)
	}

	// Nothing may have happened yet: the batch is still queued.
	if len(rec.seen) != 0 {
		t.Errorf("ops ran before End: %v", rec.seen)
	}

	if err := h.End(); err != nil {
		t.Fatalf("End: %v", err)
	}
	want := []string{"create text", "setprop value"}
	if len(rec.seen) != len(want) {
		t.Fatalf("applied %v, want %v", rec.seen, want)
	}
	for i := range want {
		if rec.seen[i] != want[i] {
			t.Errorf("op %d is %q, want %q", i, rec.seen[i], want[i])
		}
	}
	if got := rec.Find("t")[0].Props["value"]; got != "hi" {
		t.Errorf("the batch did not reach the host: value=%v", got)
	}
}

// TestThreadedForwardsTheEventReporter: a worker wraps its host in Threaded, so
// ServeHost sees the wrapper -- and must still be able to wire up what a viewer
// does, or the window is inert.
func TestThreadedForwardsTheEventReporter(t *testing.T) {
	app := fynetest.NewApp()
	t.Cleanup(app.Quit)

	inner := fynehost.New(fynehost.Ctors())
	h := fynehost.Threaded(inner)
	rep, ok := h.(snglhost.EventReporter)
	if !ok {
		t.Fatal("Threaded does not report events, so a worker's window would be inert")
	}
	var got string
	rep.SetOnEvent(func(_ snglhost.Key, event string, _ []any) { got = event })

	s, err := interp.NewSession(check(t, src), "main", interp.NewVirtual())
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if err := s.Attach(h); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	key := s.View().Find("inc")[0].Key
	if err := inner.Fire(key, "click"); err != nil {
		t.Fatalf("Fire: %v", err)
	}
	if got != "click" {
		t.Errorf("the reporter saw %q, want click", got)
	}
}
