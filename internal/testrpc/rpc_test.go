package testrpc

import (
	"bytes"
	"testing"
)

func TestCodec_writeAndReadNotification(t *testing.T) {
	var buf bytes.Buffer
	w := NewWriter(&buf)
	if err := w.Notify("log", map[string]any{"test": "T1", "msg": "hi"}); err != nil {
		t.Fatalf("notify: %v", err)
	}
	r := NewReader(&buf)
	msg, err := r.Read()
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if msg.Method != "log" {
		t.Errorf("method = %q, want log", msg.Method)
	}
	if msg.IsNotification() != true {
		t.Errorf("expected notification (no id), got request")
	}
}

func TestCodec_writeAndReadRequest_thenResponse(t *testing.T) {
	var buf bytes.Buffer
	w := NewWriter(&buf)
	id, err := w.Request("list", nil)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	if err := w.Respond(id, map[string]any{"tests": []string{"a", "b"}}, nil); err != nil {
		t.Fatalf("respond: %v", err)
	}
	r := NewReader(&buf)
	req, err := r.Read()
	if err != nil {
		t.Fatalf("read req: %v", err)
	}
	if req.Method != "list" {
		t.Errorf("method = %q", req.Method)
	}
	if req.IsNotification() {
		t.Errorf("expected request (has id)")
	}
	resp, err := r.Read()
	if err != nil {
		t.Fatalf("read resp: %v", err)
	}
	if resp.Method != "" {
		t.Errorf("response should have empty Method")
	}
	if resp.ID == nil {
		t.Errorf("response missing id")
	}
}

func TestReader_malformedLineReturnsError(t *testing.T) {
	r := NewReader(bytes.NewBufferString("not json\n"))
	if _, err := r.Read(); err == nil {
		t.Error("expected error on malformed line")
	}
}
