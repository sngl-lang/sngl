package interp

import (
	"net"
	"strings"
	"testing"
)

// TestATreeSurvivesTheWire runs the same invariant as the in-process host, with
// a pipe in the middle: the interpreter drives an RPCHost, a ServeHost on the
// far end applies to a MemHost, and that MemHost must still hold exactly the
// tree the session holds.
//
// This is what makes the "nothing carries IR" rule testable rather than
// asserted. Anything that cannot cross a JSON boundary shows up here as a
// divergence, which is how the struct-value projection was found.
func TestATreeSurvivesTheWire(t *testing.T) {
	client, server := net.Pipe()
	far := NewMemHost()
	done := make(chan error, 1)
	go func() { done <- ServeHost(far, server) }()

	h := NewRPCHost(client)
	defer func() {
		_ = h.Close()
		<-done
	}()

	s := sessionFor(t, hostSrc, "main")
	if err := s.Attach(h); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	tracksFar := func(step string) {
		t.Helper()
		if want, got := RenderView(s.View()), far.String(); want != got {
			t.Fatalf("after %s the tree across the wire diverged\n--- session\n%s--- worker\n%s", step, want, got)
		}
	}
	tracksFar("attach")

	apply := func(name string, patches []Patch, err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if err := Apply(h, patches); err != nil {
			t.Fatalf("%s: applying: %v", name, err)
		}
		tracksFar(name)
	}

	p, err := s.Invoke(s.View().Find("inc")[0].Key, "click")
	apply("click", p, err)

	p, err = s.Tick()
	apply("tick", p, err)

	setVar(s.Env, "shown", true)
	p, err = s.Sync()
	apply("if on", p, err)

	setVar(s.Env, "items", []any{"c", "a", "b"})
	p, err = s.Sync()
	apply("reorder", p, err)

	setVar(s.Env, "shown", false)
	p, err = s.Sync()
	apply("if off", p, err)

	edited := strings.Replace(hostSrc, `value="count {count}"`, `value="count = {count}"`, 1)
	p, err = s.Reload(check(t, edited))
	apply("reload", p, err)
}

// TestAStructPropCrossesTheWireIntact is the case that forced WireStruct. A
// runtime *Struct carries Def and Type -- IR -- and a plain JSON round trip
// brings back an object with Fields and Def keys rather than a value, so the
// two sides would render the same data differently.
func TestAStructPropCrossesTheWireIntact(t *testing.T) {
	src := `import . "sngl:ui"

component main ui {
    text #t({}, "hi")
}
`
	client, server := net.Pipe()
	far := NewMemHost()
	done := make(chan error, 1)
	go func() { done <- ServeHost(far, server) }()
	h := NewRPCHost(client)
	defer func() {
		_ = h.Close()
		<-done
	}()

	s, err := NewSession(check(t, src), "main", NewVirtual())
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	// The prop must actually be a struct, or this proves nothing.
	if _, ok := s.View().Find("t")[0].Props["style"].(*Struct); !ok {
		t.Fatalf("the style prop is %T, not a *Struct", s.View().Find("t")[0].Props["style"])
	}
	if err := s.Attach(h); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	if want, got := RenderView(s.View()), far.String(); want != got {
		t.Errorf("a struct prop did not survive the wire\n--- session\n%s--- worker\n%s", want, got)
	}
	style := far.Find("t")[0].Props["style"]
	if _, ok := style.(WireStruct); !ok {
		t.Errorf("the worker holds style as %T, want a WireStruct rebuilt from the wire", style)
	}
}

// TestABatchIsOneRoundTrip: ops go as notifications and only End waits, so a
// batch of many patches costs one round trip rather than one each.
func TestABatchIsOneRoundTrip(t *testing.T) {
	client, server := net.Pipe()
	far := NewMemHost()
	done := make(chan error, 1)
	go func() { done <- ServeHost(far, server) }()
	h := NewRPCHost(client)
	defer func() {
		_ = h.Close()
		<-done
	}()

	s := sessionFor(t, hostSrc, "main")
	if err := s.Attach(h); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	if far.Batches != 1 {
		t.Errorf("attach committed %d batches across the wire, want 1", far.Batches)
	}
	if far.Len() < 5 {
		t.Errorf("the worker mounted only %d nodes; the batch did not arrive", far.Len())
	}
}
