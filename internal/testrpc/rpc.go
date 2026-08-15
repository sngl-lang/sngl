// Package testrpc is the JSON-RPC 2.0 codec shared by the sngl test
// driver and per-language testagent runtimes. Messages are line-
// delimited JSON, one per line. Requests carry an id; notifications
// omit it. Responses carry the request id plus either Result or Error.
//
// The codec is deliberately minimal: no batching, no positional params,
// no string id format (ints only). Sufficient for our wire and trivial
// to reimplement in each agent language.
package testrpc

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"sync/atomic"
)

// rpcLog emits per-message wire tracing to stderr when SNGL_RPC_DEBUG is set.
// Used to diagnose the CI-only test_bubbletea_snapshot hang, which reproduces
// only inside the GitLab runner. The env is read per-call (not cached) so an
// in-process driver picks it up even when the script sets it after start; logs
// carry the pid so the driver and the agent subprocess can be told apart.
func rpcLog(format string, args ...any) {
	if os.Getenv("SNGL_RPC_DEBUG") == "" {
		return
	}
	fmt.Fprintf(os.Stderr, "[rpc pid=%d] "+format+"\n", append([]any{os.Getpid()}, args...)...)
}

func idStr(id *uint64) string {
	if id == nil {
		return "nil"
	}
	return fmt.Sprintf("%d", *id)
}

// Message is the discriminated union of request, notification, and
// response. JSON tags use omitempty so a single struct round-trips
// all three forms.
type Message struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      *uint64         `json:"id,omitempty"`     // nil = notification when Method != ""
	Method  string          `json:"method,omitempty"` // empty on responses
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *RPCError       `json:"error,omitempty"`
}

// IsNotification reports whether the message is a request without an
// id — i.e. the sender expects no response.
func (m *Message) IsNotification() bool { return m.Method != "" && m.ID == nil }

// IsResponse reports whether the message is a response (Method empty).
func (m *Message) IsResponse() bool { return m.Method == "" && m.ID != nil }

// RPCError mirrors JSON-RPC 2.0's error object.
type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

func (e *RPCError) Error() string { return fmt.Sprintf("rpc error %d: %s", e.Code, e.Message) }

// Reader reads one Message per Read call.
type Reader struct {
	sc *bufio.Scanner
}

func NewReader(r io.Reader) *Reader {
	sc := bufio.NewScanner(r)
	// Snapshot frames can be hundreds of KB after base64. 8 MiB cap is
	// generous and prevents runaway memory on a bad sender.
	sc.Buffer(make([]byte, 64*1024), 8*1024*1024)
	return &Reader{sc: sc}
}

// Read returns the next message, or io.EOF when the stream ends.
func (r *Reader) Read() (*Message, error) {
	if !r.sc.Scan() {
		if err := r.sc.Err(); err != nil {
			rpcLog("read error: %v", err)
			return nil, err
		}
		rpcLog("read EOF")
		return nil, io.EOF
	}
	b := r.sc.Bytes()
	var m Message
	if err := json.Unmarshal(b, &m); err != nil {
		rpcLog("PARSE ERROR on %d bytes: %q", len(b), truncForLog(b))
		return nil, fmt.Errorf("testrpc: parse: %w", err)
	}
	rpcLog("recv id=%s method=%q response=%v len=%d", idStr(m.ID), m.Method, m.IsResponse(), len(b))
	return &m, nil
}

// truncForLog returns b as a string, truncated so a giant/corrupt frame
// doesn't flood the log.
func truncForLog(b []byte) string {
	const max = 200
	if len(b) > max {
		return string(b[:max]) + fmt.Sprintf("…(+%d bytes)", len(b)-max)
	}
	return string(b)
}

// Writer serialises messages. Safe for concurrent use.
type Writer struct {
	mu  sync.Mutex
	w   io.Writer
	seq atomic.Uint64
}

func NewWriter(w io.Writer) *Writer { return &Writer{w: w} }

// Notify sends a notification (no id, no response expected).
func (w *Writer) Notify(method string, params any) error {
	raw, err := json.Marshal(params)
	if err != nil {
		return err
	}
	return w.send(Message{JSONRPC: "2.0", Method: method, Params: raw})
}

// ReserveID allocates the next request id without sending anything. Pair it
// with SendRequest when the caller must register a response handler *before*
// the request goes on the wire — otherwise a fast reply can arrive and be
// dropped before the handler exists (a register-after-send race).
func (w *Writer) ReserveID() uint64 { return w.seq.Add(1) }

// SendRequest sends a request under a previously reserved id.
func (w *Writer) SendRequest(id uint64, method string, params any) error {
	raw, err := json.Marshal(params)
	if err != nil {
		return err
	}
	return w.send(Message{JSONRPC: "2.0", ID: &id, Method: method, Params: raw})
}

// Request reserves an id and sends a request in one call, returning the id.
// Use ReserveID + SendRequest instead when you need to register a response
// handler before the request is sent.
func (w *Writer) Request(method string, params any) (uint64, error) {
	id := w.ReserveID()
	return id, w.SendRequest(id, method, params)
}

// Respond sends a response to a previously received request. Either
// result or rerr should be non-nil, not both.
func (w *Writer) Respond(id uint64, result any, rerr *RPCError) error {
	if result != nil && rerr != nil {
		return errors.New("testrpc: result and error are mutually exclusive")
	}
	msg := Message{JSONRPC: "2.0", ID: &id}
	if rerr != nil {
		msg.Error = rerr
	} else {
		raw, err := json.Marshal(result)
		if err != nil {
			return err
		}
		msg.Result = raw
	}
	return w.send(msg)
}

func (w *Writer) send(m Message) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	enc, err := json.Marshal(m)
	if err != nil {
		return err
	}
	rpcLog("send id=%s method=%q response=%v len=%d", idStr(m.ID), m.Method, m.IsResponse(), len(enc))
	if _, err := w.w.Write(enc); err != nil {
		rpcLog("send write error: %v", err)
		return err
	}
	_, err = w.w.Write([]byte{'\n'})
	if err != nil {
		rpcLog("send newline error: %v", err)
	}
	return err
}
