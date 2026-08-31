// Package snglhost is the protocol between a running SNGL program and the
// surface it is rendered on.
//
// It sits under pkg/ rather than internal/ because both ends import it: the
// interpreter drives a Host, and a generated worker -- a separate program,
// built inside the user's own module and linked against a real toolkit --
// implements one. A protocol only one side can import is not a protocol.
//
// Nothing here knows what IR is, and that is the point. See Host.
package snglhost

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"

	"git.duckfam.us/jonathan/sngl/internal/testrpc"
)

// Key identifies one node in a component's body by *where it is written*
// rather than by which *ir.NodeInst it currently is.
//
// The interpreter's existing caches key on pointers -- childEnvs by
// *ir.NodeInst, callChildEnvs by *ir.CallStmt, Env.vals by ir.Symbol. That is
// right within a single run and useless across two: a recheck produces an
// entirely new ir.Package, so every one of those keys becomes garbage the
// moment the program is reloaded. A live window that reloads its source, and a
// REPL that appends a line to its buffer, are both that operation -- so
// identity has to survive a recompile or reload is a restart with extra steps.
//
// Pointer keys stay, because they are what a run uses. This is the projection
// the reconciler diffs across.
type Key struct {
	// Comp is the declaring component's name. A node is only ever compared
	// against nodes of the same declaration.
	Comp string
	// Path is the structural route to the node within that body.
	Path string
}

func (k Key) String() string {
	if k.Comp == "" {
		return k.Path
	}
	return k.Comp + ":" + k.Path
}

// Iter extends a key with a loop iteration. The iteration is identified by the
// node's own `key=` expression when it has one -- that is the author saying
// which iteration this is, and it is the only identity that survives the list
// being reordered. Without one the index is all there is, and a reorder loses
// the state, exactly as it does for the compiled platforms' list diffing.
func (k Key) Iter(id string) Key {
	k.Path += "[" + id + "]"
	return k
}

// Host is what a toolkit implements. It is the whole surface between the
// interpreter and a window.
//
// Nothing here carries IR. A host is handed a NodeDesc and a Key and never a
// *Node, because the out-of-process host is the case that matters: it is a
// separate program reached over a pipe, and anything it holds has to survive
// being serialised. Keeping the in-process host to the same diet is what stops
// the two drifting into different contracts.
//
// A host is called from one goroutine. It owns its toolkit's loop and will
// deliver events from it, but those must reach a Session through a queue --
// see Session's own note.
type Host interface {
	// Begin and End bracket the patches from one Diff. A reload produces a
	// large batch, and a host that relayouts per op will visibly thrash.
	//
	// End returns the batch's error rather than each op doing so: a host on the
	// far end of a pipe sends its ops as notifications and can only answer once,
	// which is the whole point of batching. An in-process host reports the same
	// way so the two contracts stay identical.
	Begin()
	End() error

	// Create mounts a node beneath parent at index. A zero parent is a root.
	// Its props and events arrive with it, so no SetProp or Rebind follows.
	Create(d NodeDesc, parent Key, index int) error
	// Remove unmounts a node and everything beneath it.
	Remove(key Key) error
	// Move places a node at index among parent's children.
	Move(key Key, parent Key, index int) error
	// SetProp assigns one prop. A nil value clears it.
	SetProp(key Key, prop string, v any) error
	// Rebind replaces the set of events a node reports.
	Rebind(key Key, events []string) error
}

// NodeDesc is a node as a host sees it: what to build, with what values, and
// which events to report. It is the IR-free projection of a Node.
type NodeDesc struct {
	Key  Key
	Name string
	// ID is the author's `#id`, which is how a test or an inspector addresses
	// the node. Empty for most.
	ID string
	// Props are ordered as written, because a host may pass some of them to a
	// constructor and order is all that says which.
	Props []PropVal
	// Events are the declared event names, without the `@`.
	Events []string
}

// PropVal is one evaluated prop.
type PropVal struct {
	Name  string
	Value any
}

// WireStruct is a struct value as a host sees it: named fields in the order the
// checker recorded, and nothing of the declaration it came from.
//
// A runtime *Struct carries Def and Type, which are IR. Handing one to a host
// leaks the program's declarations into it, and over a pipe it does not even
// round-trip -- the far side decodes a map with Def and Fields keys rather than
// a value. This is the projection that crosses.
type WireStruct struct {
	Fields []PropVal
}

// String renders it the way a struct literal is written, so a host and a View
// print the same text.
func (w WireStruct) String() string {
	parts := make([]string, len(w.Fields))
	for i, f := range w.Fields {
		parts[i] = fmt.Sprintf("%s = %v", f.Name, f.Value)
	}
	return "{" + strings.Join(parts, ", ") + "}"
}

// MemHost is the reference Host: a tree of maps, holding exactly what it was
// told and nothing else.
//
// It is what the headless runs and the REPL render, and it is the oracle a real
// host is checked against -- if applying a patch list to MemHost does not
// reproduce the session's own view, the fault is in Diff or Apply rather than
// in anyone's toolkit.
type MemHost struct {
	roots  []*MemNode
	byKey  map[Key]*MemNode
	parent map[Key]Key
	depth  int
	// Batches counts Begin/End pairs, so a test can assert a host was not
	// asked to relayout once per patch.
	Batches int
}

// MemNode is one node in a MemHost.
type MemNode struct {
	Key      Key
	Name     string
	ID       string
	Props    map[string]any
	Order    []string
	Events   []string
	Children []*MemNode
}

func NewMemHost() *MemHost {
	return &MemHost{byKey: map[Key]*MemNode{}, parent: map[Key]Key{}}
}

func (h *MemHost) Begin() { h.depth++ }
func (h *MemHost) End() error {
	h.depth--
	if h.depth == 0 {
		h.Batches++
	}
	return nil
}

func (h *MemHost) Create(d NodeDesc, parent Key, index int) error {
	if _, exists := h.byKey[d.Key]; exists {
		return fmt.Errorf("already mounted")
	}
	n := &MemNode{Key: d.Key, Name: d.Name, ID: d.ID, Props: map[string]any{}, Events: d.Events}
	for _, p := range d.Props {
		n.Props[p.Name] = p.Value
		n.Order = append(n.Order, p.Name)
	}
	h.byKey[d.Key] = n
	h.parent[d.Key] = parent
	return h.insert(parent, n, index)
}

func (h *MemHost) Remove(key Key) error {
	n, ok := h.byKey[key]
	if !ok {
		return fmt.Errorf("not mounted")
	}
	h.detach(key)
	var forget func(*MemNode)
	forget = func(m *MemNode) {
		delete(h.byKey, m.Key)
		delete(h.parent, m.Key)
		for _, c := range m.Children {
			forget(c)
		}
	}
	forget(n)
	return nil
}

func (h *MemHost) Move(key Key, parent Key, index int) error {
	n, ok := h.byKey[key]
	if !ok {
		return fmt.Errorf("not mounted")
	}
	h.detach(key)
	h.parent[key] = parent
	return h.insert(parent, n, index)
}

func (h *MemHost) SetProp(key Key, prop string, v any) error {
	n, ok := h.byKey[key]
	if !ok {
		return fmt.Errorf("not mounted")
	}
	if v == nil {
		delete(n.Props, prop)
		for i, name := range n.Order {
			if name == prop {
				n.Order = append(n.Order[:i], n.Order[i+1:]...)
				break
			}
		}
		return nil
	}
	if _, had := n.Props[prop]; !had {
		n.Order = append(n.Order, prop)
	}
	n.Props[prop] = v
	return nil
}

func (h *MemHost) Rebind(key Key, events []string) error {
	n, ok := h.byKey[key]
	if !ok {
		return fmt.Errorf("not mounted")
	}
	n.Events = events
	return nil
}

// Len is the number of mounted nodes.
func (h *MemHost) Len() int { return len(h.byKey) }

// Find returns the mounted nodes carrying an #id, which is how a test or an
// inspector addresses one.
func (h *MemHost) Find(id string) []*MemNode {
	var out []*MemNode
	var walk func([]*MemNode)
	walk = func(nodes []*MemNode) {
		for _, n := range nodes {
			if n.ID == id {
				out = append(out, n)
			}
			walk(n.Children)
		}
	}
	walk(h.roots)
	return out
}

// String renders the mounted tree. Comparable with RenderView, which renders a
// View the same way -- that equality is the whole correctness claim.
func (h *MemHost) String() string {
	var b strings.Builder
	var walk func([]*MemNode, int)
	walk = func(nodes []*MemNode, depth int) {
		for _, n := range nodes {
			WriteNodeLine(&b, depth, n.Name, n.Props, n.Order, n.Events)
			walk(n.Children, depth+1)
		}
	}
	walk(h.roots, 0)
	return b.String()
}

// WriteNodeLine prints one node. Exported because the interpreter renders a
// View in this same format, and the two being byte-identical is what a host's
// correctness is checked by. Props are sorted rather than written in
// order: a host may legitimately have been told about them in a different
// order than the view holds them, and what has to match is the values.
func WriteNodeLine(b *strings.Builder, depth int, name string, props map[string]any, order []string, events []string) {
	fmt.Fprintf(b, "%s%s(", strings.Repeat("  ", depth), name)
	keys := append([]string(nil), order...)
	sort.Strings(keys)
	parts := make([]string, 0, len(keys)+len(events))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=%v", k, props[k]))
	}
	ev := append([]string(nil), events...)
	sort.Strings(ev)
	for _, e := range ev {
		parts = append(parts, "@"+e)
	}
	b.WriteString(strings.Join(parts, " "))
	b.WriteString(")\n")
}

func (h *MemHost) insert(parent Key, n *MemNode, index int) error {
	siblings := h.childrenOf(parent)
	if index < 0 || index > len(*siblings) {
		index = len(*siblings)
	}
	*siblings = append(*siblings, nil)
	copy((*siblings)[index+1:], (*siblings)[index:])
	(*siblings)[index] = n
	return nil
}

func (h *MemHost) detach(key Key) {
	parent := h.parent[key]
	siblings := h.childrenOf(parent)
	for i, c := range *siblings {
		if c.Key == key {
			*siblings = append((*siblings)[:i], (*siblings)[i+1:]...)
			return
		}
	}
}

func (h *MemHost) childrenOf(parent Key) *[]*MemNode {
	if parent == (Key{}) {
		return &h.roots
	}
	if n, ok := h.byKey[parent]; ok {
		return &n.Children
	}
	return &h.roots
}

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
