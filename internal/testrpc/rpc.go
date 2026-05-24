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
	"sync"
	"sync/atomic"
)

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
			return nil, err
		}
		return nil, io.EOF
	}
	var m Message
	if err := json.Unmarshal(r.sc.Bytes(), &m); err != nil {
		return nil, fmt.Errorf("testrpc: parse: %w", err)
	}
	return &m, nil
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

// Request sends a request and returns the allocated id. The caller is
// responsible for matching the eventual response by id (the reader
// loop on the other side typically dispatches by id).
func (w *Writer) Request(method string, params any) (uint64, error) {
	raw, err := json.Marshal(params)
	if err != nil {
		return 0, err
	}
	id := w.seq.Add(1)
	return id, w.send(Message{JSONRPC: "2.0", ID: &id, Method: method, Params: raw})
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
	if _, err := w.w.Write(enc); err != nil {
		return err
	}
	_, err = w.w.Write([]byte{'\n'})
	return err
}
