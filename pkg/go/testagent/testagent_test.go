package testagent

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/internal/testrpc"
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
