package interprun

import (
	"net"
	"testing"
	"time"

	"git.duckfam.us/jonathan/sngl/internal/checker"
	"git.duckfam.us/jonathan/sngl/internal/interp"
	"git.duckfam.us/jonathan/sngl/internal/parser"
	"git.duckfam.us/jonathan/sngl/ir"
	"git.duckfam.us/jonathan/sngl/pkg/go/snglhost"
)

func check(t *testing.T, src string) *ir.Package {
	t.Helper()
	doc, err := parser.Parse("t.sngl", []byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	pkg, diags := checker.Check(doc, &checker.Config{IsMain: true})
	for _, d := range diags {
		if d.Severity == ir.Error {
			t.Fatalf("check: %s: %s", d.Pos, d.Msg)
		}
	}
	return pkg
}

// clickable is a host that can be told to report a click, standing in for a
// window without opening one.
type clickable struct {
	*snglhost.MemHost
	emit func(snglhost.Key, string, []any)
}

func (h *clickable) SetOnEvent(fn func(snglhost.Key, string, []any)) { h.emit = fn }

const src = `import . "sngl:ui"
import . "sngl:time"

component main {
    var count = 0
    vbox {
        text #out(value="count {count}")
        button #inc(text="+", @click { count += 1 })
        timer(interval=20ms, enabled=true, @tick { count += 10 })
    }
}
`

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// TestAClickReachesTheProgramAndTheAnswerComesBack is the loop a window runs,
// with a pipe where the window would be: the host reports a click, the
// interpreted handler runs, and the patch lands back on the host.
func TestAClickReachesTheProgramAndTheAnswerComesBack(t *testing.T) {
	client, server := net.Pipe()
	far := &clickable{MemHost: snglhost.NewMemHost()}
	go func() { _ = snglhost.ServeHost(far, server) }()

	s, err := interp.NewSession(check(t, src), "main", interp.Real{})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- Drive(s, client) }()
	defer func() {
		_ = server.Close()
		<-done
	}()

	waitFor(t, "the tree to mount", func() bool { return len(far.Find("inc")) == 1 })

	incKey := far.Find("inc")[0].Key
	far.emit(incKey, "click", nil)

	waitFor(t, "the click to come back", func() bool {
		out := far.Find("out")
		return len(out) == 1 && out[0].Props["value"] == "count 1"
	})
}

// TestATimerFiresOnTheWallClock: a window follows real time, unlike a test that
// advances it by hand, and the loop has to wake for a deadline nobody sent.
func TestATimerFiresOnTheWallClock(t *testing.T) {
	client, server := net.Pipe()
	far := &clickable{MemHost: snglhost.NewMemHost()}
	go func() { _ = snglhost.ServeHost(far, server) }()

	s, err := interp.NewSession(check(t, src), "main", interp.Real{})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- Drive(s, client) }()
	defer func() {
		_ = server.Close()
		<-done
	}()

	waitFor(t, "the timer to fire without anyone asking", func() bool {
		out := far.Find("out")
		return len(out) == 1 && out[0].Props["value"] == "count 10"
	})
}

// TestDriveReturnsWhenTheWindowCloses: a driver must not hang on a worker that
// went away.
func TestDriveReturnsWhenTheWindowCloses(t *testing.T) {
	client, server := net.Pipe()
	go func() { _ = snglhost.ServeHost(snglhost.NewMemHost(), server) }()

	s, err := interp.NewSession(check(t, src), "main", interp.Real{})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- Drive(s, client) }()

	time.Sleep(20 * time.Millisecond)
	_ = server.Close()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Drive did not return after the worker closed")
	}
}
