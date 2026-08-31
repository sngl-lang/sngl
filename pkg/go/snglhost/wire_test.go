package snglhost_test

import (
	"net"
	"testing"
	"time"

	"git.duckfam.us/jonathan/sngl/pkg/go/snglhost"
)

// eventingHost is a Host that reports an event when told to, standing in for a
// toolkit without needing one.
type eventingHost struct {
	*snglhost.MemHost
	emit func(snglhost.Key, string)
}

func (h *eventingHost) SetOnEvent(fn func(snglhost.Key, string)) { h.emit = fn }

// TestAnEventCrossesBackOverTheWire: ops go one way, interaction comes the
// other. Without this a window is a picture.
func TestAnEventCrossesBackOverTheWire(t *testing.T) {
	client, server := net.Pipe()
	far := &eventingHost{MemHost: snglhost.NewMemHost()}
	go func() { _ = snglhost.ServeHost(far, server) }()

	h := snglhost.NewRPCHost(client)
	defer h.Close()

	// A batch first, both to mount something and to prove the reader routes a
	// response and an event to different places.
	key := snglhost.Key{Comp: "main", Path: "button@0"}
	h.Begin()
	if err := h.Create(snglhost.NodeDesc{Key: key, Name: "button", ID: "go", Events: []string{"click"}}, snglhost.Key{}, 0); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := h.End(); err != nil {
		t.Fatalf("End: %v", err)
	}

	// ServeHost wired the host's reporter up, so this is what a real click
	// reaches.
	if far.emit == nil {
		t.Fatal("ServeHost did not wire the host's event reporter")
	}
	far.emit(key, "click")

	select {
	case ev := <-h.Events():
		if ev.Key != key || ev.Name != "click" {
			t.Errorf("got %+v, want a click on %s", ev, key)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no event arrived")
	}
}

// chattyHost emits an event from inside an op, which is the interleaving that
// matters: the notification is written before the batch's response, so a reader
// that took the first message it saw would answer End with a click.
type chattyHost struct {
	*snglhost.MemHost
	emit func(snglhost.Key, string)
}

func (h *chattyHost) SetOnEvent(fn func(snglhost.Key, string)) { h.emit = fn }

func (h *chattyHost) SetProp(key snglhost.Key, prop string, v any) error {
	if h.emit != nil {
		h.emit(key, "click")
	}
	return h.MemHost.SetProp(key, prop, v)
}

// TestAnEventDuringABatchDoesNotStealTheResponse is why one goroutine owns
// reading. Two readers on one stream would race for each other's messages.
func TestAnEventDuringABatchDoesNotStealTheResponse(t *testing.T) {
	client, server := net.Pipe()
	far := &chattyHost{MemHost: snglhost.NewMemHost()}
	go func() { _ = snglhost.ServeHost(far, server) }()

	h := snglhost.NewRPCHost(client)
	defer h.Close()

	key := snglhost.Key{Comp: "main", Path: "text@0"}
	h.Begin()
	if err := h.Create(snglhost.NodeDesc{Key: key, Name: "text", ID: "t"}, snglhost.Key{}, 0); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := h.End(); err != nil {
		t.Fatalf("End: %v", err)
	}

	for i := range 10 {
		h.Begin()
		if err := h.SetProp(key, "value", i); err != nil {
			t.Fatalf("SetProp: %v", err)
		}
		if err := h.End(); err != nil {
			t.Fatalf("batch %d, with an event in flight: %v", i, err)
		}
	}

	nodes := far.Find("t")
	if len(nodes) != 1 {
		t.Fatalf("the worker holds %d nodes with #t", len(nodes))
	}
	if got := nodes[0].Props["value"]; got != float64(9) {
		t.Errorf("worker holds value=%v, want the last batch's 9", got)
	}
	// And every event still arrived on its own channel.
	if len(h.Events()) == 0 {
		t.Error("no events were routed while batches were in flight")
	}
}

// TestClosingTheWorkerIsObservable: a driver has to learn the window was shut
// rather than hanging on the next batch.
func TestClosingTheWorkerIsObservable(t *testing.T) {
	client, server := net.Pipe()
	go func() { _ = snglhost.ServeHost(snglhost.NewMemHost(), server) }()
	h := snglhost.NewRPCHost(client)

	_ = server.Close()
	select {
	case <-h.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("Done never closed after the worker went away")
	}
	h.Begin()
	if err := h.End(); err == nil {
		t.Error("End succeeded against a closed worker")
	}
}
