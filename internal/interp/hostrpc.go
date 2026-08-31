package interp

import (
	"encoding/json"
	"fmt"
	"io"

	"git.duckfam.us/jonathan/sngl/internal/testrpc"
)

// RPCHost is a Host on the far end of a pipe: the interpreter's side of the
// out-of-process worker.
//
// Ops go out as notifications and End as a request, so a batch costs one round
// trip rather than one per op. That is the batching contract made load-bearing:
// a host that could answer per op would not need Begin and End at all.
//
// The wire is internal/testrpc, the same line-delimited JSON-RPC the test
// driver already speaks to per-language agents. One wire in the repo, not two.
type RPCHost struct {
	w      *testrpc.Writer
	r      *testrpc.Reader
	closer io.Closer
	// pending is the error the batch has already failed with. An op that fails
	// mid-batch is reported at End, and the ops after it are still sent: the
	// far side is a separate process and dropping half a batch would leave it
	// holding a tree neither side can describe.
	pending error
}

// NewRPCHost speaks to a worker over rw.
func NewRPCHost(rw io.ReadWriteCloser) *RPCHost {
	return &RPCHost{w: testrpc.NewWriter(rw), r: testrpc.NewReader(rw), closer: rw}
}

func (h *RPCHost) Close() error { return h.closer.Close() }

func (h *RPCHost) Begin() {
	h.pending = nil
	h.notify("begin", nil)
}

// End asks the worker to commit the batch and report what went wrong, if
// anything. This is the only round trip.
func (h *RPCHost) End() error {
	id, err := h.w.Request("end", nil)
	if err != nil {
		return err
	}
	for {
		msg, err := h.r.Read()
		if err != nil {
			return err
		}
		if !msg.IsResponse() || *msg.ID != id {
			continue // a worker may notify events while a batch is in flight
		}
		if msg.Error != nil {
			return msg.Error
		}
		return h.pending
	}
}

type createParams struct {
	Desc   NodeDesc `json:"desc"`
	Parent Key      `json:"parent"`
	Index  int      `json:"index"`
}

type keyParams struct {
	Key Key `json:"key"`
}

type moveParams struct {
	Key    Key `json:"key"`
	Parent Key `json:"parent"`
	Index  int `json:"index"`
}

type propParams struct {
	Key   Key    `json:"key"`
	Prop  string `json:"prop"`
	Value any    `json:"value"`
}

type bindParams struct {
	Key    Key      `json:"key"`
	Events []string `json:"events"`
}

func (h *RPCHost) Create(d NodeDesc, parent Key, index int) error {
	return h.notify("create", createParams{Desc: d, Parent: parent, Index: index})
}
func (h *RPCHost) Remove(key Key) error { return h.notify("remove", keyParams{Key: key}) }
func (h *RPCHost) Move(key, parent Key, index int) error {
	return h.notify("move", moveParams{Key: key, Parent: parent, Index: index})
}
func (h *RPCHost) SetProp(key Key, prop string, v any) error {
	return h.notify("setprop", propParams{Key: key, Prop: prop, Value: v})
}
func (h *RPCHost) Rebind(key Key, events []string) error {
	return h.notify("rebind", bindParams{Key: key, Events: events})
}

func (h *RPCHost) notify(method string, params any) error {
	if err := h.w.Notify(method, params); err != nil && h.pending == nil {
		h.pending = err
	}
	return nil
}

// ServeHost runs the worker's side: it reads ops off rw and applies them to h,
// answering each `end` with whatever the batch produced.
//
// This is what a generated worker's main loop is, minus the toolkit. Running
// it against MemHost is what proves the protocol carries a tree faithfully,
// with the same oracle an in-process host is checked against.
func ServeHost(h Host, rw io.ReadWriteCloser) error {
	r, w := testrpc.NewReader(rw), testrpc.NewWriter(rw)
	var batch error
	for {
		msg, err := r.Read()
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
		if msg.Method == "end" {
			var rerr *testrpc.RPCError
			if err := h.End(); err != nil {
				rerr = &testrpc.RPCError{Code: 1, Message: err.Error()}
			} else if batch != nil {
				rerr = &testrpc.RPCError{Code: 1, Message: batch.Error()}
			}
			batch = nil
			if msg.ID != nil {
				if err := w.Respond(*msg.ID, struct{}{}, rerr); err != nil {
					return err
				}
			}
			continue
		}
		if err := serveOne(h, msg.Method, msg.Params); err != nil && batch == nil {
			batch = err
		}
	}
}

func serveOne(h Host, method string, raw json.RawMessage) error {
	switch method {
	case "begin":
		h.Begin()
		return nil
	case "create":
		var p createParams
		if err := json.Unmarshal(raw, &p); err != nil {
			return err
		}
		for i := range p.Desc.Props {
			p.Desc.Props[i].Value = decodeWire(p.Desc.Props[i].Value)
		}
		return h.Create(p.Desc, p.Parent, p.Index)
	case "remove":
		var p keyParams
		if err := json.Unmarshal(raw, &p); err != nil {
			return err
		}
		return h.Remove(p.Key)
	case "move":
		var p moveParams
		if err := json.Unmarshal(raw, &p); err != nil {
			return err
		}
		return h.Move(p.Key, p.Parent, p.Index)
	case "setprop":
		var p propParams
		if err := json.Unmarshal(raw, &p); err != nil {
			return err
		}
		return h.SetProp(p.Key, p.Prop, decodeWire(p.Value))
	case "rebind":
		var p bindParams
		if err := json.Unmarshal(raw, &p); err != nil {
			return err
		}
		return h.Rebind(p.Key, p.Events)
	}
	return fmt.Errorf("unknown op %q", method)
}

// structMarker is the key a WireStruct is recognised by after a round trip.
// JSON has no struct type, so a decoded one arrives as an ordinary object and
// would render as `map[Fields:[...]]` rather than `{x = 1}` -- the two sides
// would disagree while holding the same data.
const structMarker = "$struct"

// MarshalJSON tags a struct value so the far side can rebuild it.
func (w WireStruct) MarshalJSON() ([]byte, error) {
	return json.Marshal(map[string]any{structMarker: w.Fields})
}

// decodeWire rebuilds the values MarshalJSON tagged, recursively.
func decodeWire(v any) any {
	switch x := v.(type) {
	case map[string]any:
		if raw, ok := x[structMarker]; ok {
			return WireStruct{Fields: decodeFields(raw)}
		}
		out := make(map[string]any, len(x))
		for k, el := range x {
			out[k] = decodeWire(el)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, el := range x {
			out[i] = decodeWire(el)
		}
		return out
	}
	return v
}

func decodeFields(raw any) []PropVal {
	list, ok := raw.([]any)
	if !ok {
		return nil
	}
	out := make([]PropVal, 0, len(list))
	for _, el := range list {
		m, ok := el.(map[string]any)
		if !ok {
			continue
		}
		name, _ := m["Name"].(string)
		out = append(out, PropVal{Name: name, Value: decodeWire(m["Value"])})
	}
	return out
}
