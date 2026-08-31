package interp

import (
	"fmt"
	"sort"
	"strings"
)

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
	Begin()
	End()

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

// Desc projects a node for a host.
func (n *Node) Desc() NodeDesc {
	d := NodeDesc{Key: n.Key, Name: n.Name, ID: n.ID}
	for _, name := range n.PropOrder {
		d.Props = append(d.Props, PropVal{Name: name, Value: n.Props[name]})
	}
	for _, h := range n.Handlers {
		d.Events = append(d.Events, h.Name)
	}
	return d
}

// Apply hands a patch list to a host, bracketed as one batch.
//
// Diff already orders the patches so this needs no thought: removals before
// creations before moves before assignments. Apply adds nothing but the
// bracket and the translation.
func Apply(h Host, patches []Patch) error {
	if len(patches) == 0 {
		return nil
	}
	h.Begin()
	defer h.End()
	for _, p := range patches {
		var err error
		switch p.Kind {
		case PatchCreate:
			err = h.Create(p.Node.Desc(), p.Parent, p.Index)
		case PatchRemove:
			err = h.Remove(p.Key)
		case PatchMove:
			err = h.Move(p.Key, p.Parent, p.Index)
		case PatchSetProp:
			err = h.SetProp(p.Key, p.Prop, p.Value)
		case PatchRebind:
			err = h.Rebind(p.Key, p.Node.Desc().Events)
		default:
			err = fmt.Errorf("unknown patch kind %d", p.Kind)
		}
		if err != nil {
			return fmt.Errorf("%s: %w", p, err)
		}
	}
	return nil
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
func (h *MemHost) End() {
	h.depth--
	if h.depth == 0 {
		h.Batches++
	}
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
			writeNodeLine(&b, depth, n.Name, n.Props, n.Order, n.Events)
			walk(n.Children, depth+1)
		}
	}
	walk(h.roots, 0)
	return b.String()
}

// RenderView renders a View in MemHost's format.
func RenderView(v *View) string {
	var b strings.Builder
	var walk func([]*Node, int)
	walk = func(nodes []*Node, depth int) {
		for _, n := range nodes {
			d := n.Desc()
			props := map[string]any{}
			var order []string
			for _, p := range d.Props {
				props[p.Name] = p.Value
				order = append(order, p.Name)
			}
			writeNodeLine(&b, depth, n.Name, props, order, d.Events)
			walk(n.Children, depth+1)
		}
	}
	if v != nil {
		walk(v.Roots, 0)
	}
	return b.String()
}

// writeNodeLine prints one node. Props are sorted rather than written in
// order: a host may legitimately have been told about them in a different
// order than the view holds them, and what has to match is the values.
func writeNodeLine(b *strings.Builder, depth int, name string, props map[string]any, order []string, events []string) {
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
