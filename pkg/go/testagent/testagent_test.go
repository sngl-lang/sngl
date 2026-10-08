package testagent

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"

	"duckfam.us/sngl/internal/testrpc"
)

func TestT_LogEmitsNotification(t *testing.T) {
	var buf bytes.Buffer
	at := newAgentT(&buf, "myTest")
	at.Log("hello")
	r := testrpc.NewReader(&buf)
	m, err := r.Read()
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if m.Method != "log" {
		t.Errorf("method = %q", m.Method)
	}
	if !m.IsNotification() {
		t.Errorf("expected notification")
	}
	var args struct {
		Test string `json:"test"`
		Msg  string `json:"msg"`
	}
	_ = json.Unmarshal(m.Params, &args)
	if args.Test != "myTest" || args.Msg != "hello" {
		t.Errorf("got %+v", args)
	}
}

func TestT_FailRecordsButContinues(t *testing.T) {
	var buf bytes.Buffer
	at := newAgentT(&buf, "myTest")
	at.Fail()
	if !at.failed {
		t.Errorf("Fail did not mark failed")
	}
}

func TestT_FailNowAborts(t *testing.T) {
	var buf bytes.Buffer
	at := newAgentT(&buf, "myTest")
	defer func() {
		if r := recover(); r == nil {
			t.Errorf("FailNow should panic with sentinel")
		}
		if !at.failed {
			t.Errorf("FailNow did not mark failed before abort")
		}
	}()
	at.FailNow()
}

func TestT_ErrorfDecomposes(t *testing.T) {
	var buf bytes.Buffer
	at := newAgentT(&buf, "myTest")
	at.Errorf("bad: %d", 7)
	if !at.failed {
		t.Error("Errorf must mark failed")
	}
	r := testrpc.NewReader(&buf)
	m, err := r.Read() // expect log first
	if err != nil {
		t.Fatal(err)
	}
	if m.Method != "log" {
		t.Errorf("first message should be log, got %q", m.Method)
	}
	var args struct{ Msg string }
	_ = json.Unmarshal(m.Params, &args)
	if !strings.Contains(args.Msg, "bad: 7") {
		t.Errorf("log msg = %q", args.Msg)
	}
	m, _ = r.Read() // then markFail
	if m.Method != "markFail" {
		t.Errorf("second message = %q, want markFail", m.Method)
	}
}

func TestRunRegisteredTest_passes(t *testing.T) {
	resetRegistry()
	RegisterTest("ok", func(t *T) { t.Log("ran") })
	var stdout bytes.Buffer
	w := testrpc.NewWriter(&stdout)
	runOne(w, "ok")
	// expect: testStart, log, testEnd(pass)
	r := testrpc.NewReader(&stdout)
	methods := []string{}
	for {
		m, err := r.Read()
		if err != nil {
			break
		}
		methods = append(methods, m.Method)
	}
	got := strings.Join(methods, ",")
	if got != "testStart,log,testEnd" {
		t.Errorf("methods = %q", got)
	}
}

func TestRunRegisteredTest_failNowReportsFailure(t *testing.T) {
	resetRegistry()
	RegisterTest("bad", func(t *T) { t.Errorf("nope"); t.FailNow() })
	var stdout bytes.Buffer
	w := testrpc.NewWriter(&stdout)
	runOne(w, "bad")
	r := testrpc.NewReader(&stdout)
	var lastEnd struct {
		Test       string `json:"test"`
		Status     string `json:"status"`
		DurationMs int    `json:"durationMs"`
	}
	for {
		m, err := r.Read()
		if err != nil {
			break
		}
		if m.Method == "testEnd" {
			_ = json.Unmarshal(m.Params, &lastEnd)
		}
	}
	if lastEnd.Status != "fail" {
		t.Errorf("status = %q, want fail", lastEnd.Status)
	}
}

func TestT_SnapshotErrorsWhenNoCaptureRegistered(t *testing.T) {
	resetSnapshot()
	var buf bytes.Buffer
	at := newAgentT(&buf, "myTest")
	at.Snapshot("first")
	if !at.failed {
		t.Error("Snapshot with no capture should fail the test")
	}
}

func TestT_SnapshotSubmitsRequestAndPasses(t *testing.T) {
	resetRegistry()
	resetSnapshot()
	RegisterSnapshot(func() (string, []byte, error) {
		return "text/plain", []byte("hello"), nil
	})
	// Simulate a driver via paired pipes. The agent (T) writes its request
	// to agentOut; the test reads it from driverIn. The test writes a
	// response to driverOut; the agent's startReadLoop reads it from agentIn.
	agentIn, driverOut := io.Pipe()
	driverIn, agentOut := io.Pipe()

	at := &T{name: "T1", w: testrpc.NewWriter(agentOut)}
	startReadLoop(at, agentIn)

	go func() {
		r := testrpc.NewReader(driverIn)
		msg, err := r.Read()
		if err != nil {
			t.Errorf("driver read: %v", err)
			return
		}
		if msg.Method != "snapshotAssert" {
			t.Errorf("driver got method = %q, want snapshotAssert", msg.Method)
		}
		w := testrpc.NewWriter(driverOut)
		_ = w.Respond(*msg.ID, map[string]any{"pass": true}, nil)
	}()

	at.Snapshot("first")
	if at.failed {
		t.Errorf("expected pass, got fail")
	}
}

func TestT_SnapshotMismatchReportsDiff(t *testing.T) {
	resetRegistry()
	resetSnapshot()
	RegisterSnapshot(func() (string, []byte, error) {
		return "text/plain", []byte("actual"), nil
	})
	agentIn, driverOut := io.Pipe()
	driverIn, agentOut := io.Pipe()

	at := &T{name: "T2", w: testrpc.NewWriter(agentOut)}
	startReadLoop(at, agentIn)

	go func() {
		r := testrpc.NewReader(driverIn)
		for {
			msg, err := r.Read()
			if err != nil {
				return
			}
			if msg.Method == "snapshotAssert" {
				w := testrpc.NewWriter(driverOut)
				_ = w.Respond(*msg.ID, map[string]any{"pass": false, "diff": "want X got Y"}, nil)
			}
			// Drain subsequent notifications (e.g. log from Errorf).
		}
	}()

	at.Snapshot("foo")
	if !at.failed {
		t.Errorf("expected mismatch to fail the test")
	}
}

// writerFunc adapts a function to io.Writer.
type writerFunc func([]byte) (int, error)

func (f writerFunc) Write(p []byte) (int, error) { return f(p) }

// TestSendAndAwaitRegistersBeforeSend is a regression test for the
// register-after-send race that caused the intermittent CI hang in
// test_bubbletea_snapshot. The writer delivers the response synchronously the
// instant the request is written — mimicking a driver whose reply beats the
// caller's registration. sendAndAwait must register the pending channel before
// sending, or the response is dropped and this hangs.
func TestSendAndAwaitRegistersBeforeSend(t *testing.T) {
	var w *testrpc.Writer
	w = testrpc.NewWriter(writerFunc(func(p []byte) (int, error) {
		var m testrpc.Message
		if json.Unmarshal(bytes.TrimSpace(p), &m) == nil && m.ID != nil && m.Method != "" {
			// Respond before Write returns — i.e. before the old code
			// would have registered its response channel.
			deliverResponse(&testrpc.Message{JSONRPC: "2.0", ID: m.ID, Result: json.RawMessage(`{"ok":true}`)})
		}
		return len(p), nil
	}))

	done := make(chan error, 1)
	go func() {
		resp, err := sendAndAwait(w, "ping", map[string]any{})
		if err != nil {
			done <- err
			return
		}
		if resp == nil || resp.ID == nil {
			done <- io.ErrUnexpectedEOF
			return
		}
		done <- nil
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("sendAndAwait: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("sendAndAwait hung: a response delivered before registration was dropped (register-after-send race)")
	}
}
