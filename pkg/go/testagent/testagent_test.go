package testagent

import (
	"bytes"
	"encoding/json"
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
