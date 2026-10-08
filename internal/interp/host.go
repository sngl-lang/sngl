package interp

import (
	"fmt"
	"io"
	"strings"

	"duckfam.us/sngl/pkg/go/snglhost"
)

// The protocol lives in pkg/go/snglhost, where a generated worker can reach it
// without dragging the compiler into its import graph. These aliases keep the
// rest of this package naming the types where they are used.
type (
	Host       = snglhost.Host
	NodeDesc   = snglhost.NodeDesc
	PropVal    = snglhost.PropVal
	WireStruct = snglhost.WireStruct
	MemHost    = snglhost.MemHost
	MemNode    = snglhost.MemNode
	RPCHost    = snglhost.RPCHost
)

// NewMemHost returns the reference Host: a tree of maps holding exactly what it
// was told.
func NewMemHost() *MemHost { return snglhost.NewMemHost() }

// NewRPCHost drives a Host on the far end of a pipe.
func NewRPCHost(rw io.ReadWriteCloser) *RPCHost { return snglhost.NewRPCHost(rw) }

// ServeHost runs the far side: it reads ops off rw and applies them to h.
func ServeHost(h Host, rw io.ReadWriteCloser) error { return snglhost.ServeHost(h, rw) }

// wireValue converts a runtime value to one a host may hold. Composite values
// are converted through, since a list of structs is still a list of IR.
func wireValue(v any) any {
	switch x := v.(type) {
	case *Struct:
		if x == nil {
			return nil
		}
		w := WireStruct{Fields: make([]PropVal, len(x.Fields))}
		for i, f := range x.Fields {
			w.Fields[i] = PropVal{Name: f.Name, Value: wireValue(f.Value)}
		}
		return w
	case []any:
		out := make([]any, len(x))
		for i, el := range x {
			out[i] = wireValue(el)
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, el := range x {
			out[k] = wireValue(el)
		}
		return out
	case unitValue:
		// A host reads a measurement as its number; the unit table is the
		// interpreter's and does not cross.
		return x.magnitude()
	}
	return v
}

// Desc projects a node for a host.
func (n *Node) Desc() NodeDesc {
	d := NodeDesc{Key: n.Key, Name: n.Name, ID: n.ID}
	for _, name := range n.PropOrder {
		d.Props = append(d.Props, PropVal{Name: name, Value: wireValue(n.Props[name])})
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
			_ = h.End()
			return fmt.Errorf("%s: %w", p, err)
		}
	}
	return h.End()
}

// RenderView renders a View in MemHost's format.
func RenderView(v *View) string {
	var b strings.Builder
	var walk func([]*Node, int)
	walk = func(nodes []*Node, depth int) {
		for _, n := range HostChildren(nodes) {
			d := n.Desc()
			props := map[string]any{}
			var order []string
			for _, p := range d.Props {
				props[p.Name] = p.Value
				order = append(order, p.Name)
			}
			snglhost.WriteNodeLine(&b, depth, n.Name, props, order, d.Events)
			walk(n.Children, depth+1)
		}
	}
	if v != nil {
		walk(v.Roots, 0)
	}
	return b.String()
}
