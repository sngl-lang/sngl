package fynehost

import (
	fyne "fyne.io/fyne/v2"

	"git.duckfam.us/jonathan/sngl/pkg/go/snglhost"
)

// Threaded runs a Host's ops on Fyne's own thread.
//
// Fyne owns the goroutine its loop runs on, and a widget touched from anywhere
// else is a data race. The ops arrive on whichever goroutine reads the wire, so
// they have to be handed over -- and a batch is handed over *whole*, in one
// fyne.DoAndWait. That is what Begin and End were always for: one thread hop
// per logical change rather than one per prop.
//
// Deferring also fixes the ordering that would otherwise be wrong. Fyne.Do is
// asynchronous, so ops posted individually could be applied out of order
// against a tree where order is the whole point of a Move.
func Threaded(h snglhost.Host) snglhost.Host { return &threaded{inner: h} }

type threaded struct {
	inner    snglhost.Host
	queued   []func() error
	depth    int
	deferred error
}

func (t *threaded) Begin() {
	t.depth++
	if t.depth == 1 {
		t.queued = t.queued[:0]
		t.deferred = nil
	}
	t.enqueue(func() error { t.inner.Begin(); return nil })
}

func (t *threaded) End() error {
	t.enqueue(func() error { return t.inner.End() })
	t.depth--
	if t.depth > 0 {
		return nil
	}
	ops := t.queued
	t.queued = nil
	var err error
	fyne.DoAndWait(func() {
		for _, op := range ops {
			if e := op(); e != nil && err == nil {
				err = e
			}
		}
	})
	if err == nil {
		err = t.deferred
	}
	return err
}

func (t *threaded) Create(d snglhost.NodeDesc, parent snglhost.Key, index int) error {
	return t.enqueue(func() error { return t.inner.Create(d, parent, index) })
}
func (t *threaded) Remove(key snglhost.Key) error {
	return t.enqueue(func() error { return t.inner.Remove(key) })
}
func (t *threaded) Move(key, parent snglhost.Key, index int) error {
	return t.enqueue(func() error { return t.inner.Move(key, parent, index) })
}
func (t *threaded) SetProp(key snglhost.Key, prop string, v any) error {
	return t.enqueue(func() error { return t.inner.SetProp(key, prop, v) })
}
func (t *threaded) Rebind(key snglhost.Key, events []string) error {
	return t.enqueue(func() error { return t.inner.Rebind(key, events) })
}

// SetOnEvent forwards to the wrapped host: an event originates on Fyne's thread
// already, so it needs no hop, and the wire is safe to write from there.
func (t *threaded) SetOnEvent(fn func(snglhost.Key, string)) {
	if rep, ok := t.inner.(snglhost.EventReporter); ok {
		rep.SetOnEvent(fn)
	}
}

// enqueue records an op to run on Fyne's thread. It returns nil because the op
// has not run yet; whatever it reports surfaces at End, which is the same
// contract the wire has.
func (t *threaded) enqueue(op func() error) error {
	t.queued = append(t.queued, op)
	return nil
}
