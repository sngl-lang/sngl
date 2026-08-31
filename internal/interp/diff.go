package interp

import "fmt"

// PatchKind is what a Patch asks a host to do.
type PatchKind int

const (
	// PatchCreate mounts a node that was not there before, beneath Parent at
	// Index. Its props and handlers travel with it, so no SetProp follows.
	PatchCreate PatchKind = iota
	// PatchRemove unmounts a node and everything beneath it.
	PatchRemove
	// PatchSetProp assigns one prop of a node that survived.
	PatchSetProp
	// PatchRebind says a surviving node's set of declared events changed, so
	// the host must re-read Node.Handlers. Handler bodies are IR and every
	// reload replaces them, so they are never compared by value -- only the
	// names are, and only their appearing or disappearing is a patch.
	PatchRebind
)

func (k PatchKind) String() string {
	switch k {
	case PatchCreate:
		return "create"
	case PatchRemove:
		return "remove"
	case PatchSetProp:
		return "setprop"
	case PatchRebind:
		return "rebind"
	}
	return "?"
}

// Patch is one step in turning the tree a host has mounted into the tree the
// interpreter now holds.
type Patch struct {
	Kind PatchKind
	Key  Key

	// Node is the node to mount, on PatchCreate only.
	Node *Node
	// Parent is where it mounts, and Index its position among that parent's
	// children. A zero Parent means a root.
	Parent Key
	Index  int

	// Prop and Value carry a PatchSetProp.
	Prop  string
	Value any
}

func (p Patch) String() string {
	switch p.Kind {
	case PatchSetProp:
		return fmt.Sprintf("setprop %s %s=%v", p.Key, p.Prop, p.Value)
	case PatchCreate:
		return fmt.Sprintf("create %s (%s) under %s at %d", p.Key, p.Node.Name, p.Parent, p.Index)
	default:
		return fmt.Sprintf("%s %s", p.Kind, p.Key)
	}
}

// Diff reports the patches that turn before into after.
//
// Nodes are matched by mounted Key, never by position, which is the whole
// reason Key exists: a recheck replaces every pointer in the program, so a
// reload has nothing else to match on.
//
// The order is removals first (deepest first, so a parent outlives its
// children), then creations (shallowest first, so a parent exists before what
// mounts into it), then assignments to what survived. A host may apply them in
// that order without further thought.
func Diff(before, after *View) []Patch {
	var removes, creates, sets []Patch

	afterKeys := map[Key]bool{}
	walkParented(after, func(n *Node, parent Key, index, depth int) {
		afterKeys[n.Key] = true
		old, ok := before.At(n.Key)
		// A node whose element changed at the same key is not the same node:
		// no host can turn a button into a text field by assignment.
		if !ok || old.Name != n.Name {
			if ok {
				removes = append(removes, Patch{Kind: PatchRemove, Key: n.Key, Index: depth})
			}
			creates = append(creates, Patch{
				Kind: PatchCreate, Key: n.Key, Node: n, Parent: parent, Index: index,
			})
			return
		}
		sets = append(sets, propPatches(old, n)...)
		if !sameHandlerNames(old, n) {
			sets = append(sets, Patch{Kind: PatchRebind, Key: n.Key, Node: n})
		}
	})

	walkParented(before, func(n *Node, _ Key, _, depth int) {
		if !afterKeys[n.Key] {
			removes = append(removes, Patch{Kind: PatchRemove, Key: n.Key, Index: depth})
		}
	})

	// Deepest first among removals: a host freeing a parent must not be handed
	// its children afterwards.
	sortByIndexDesc(removes)
	// Creations arrive in pre-order already, which is parents before children.

	out := make([]Patch, 0, len(removes)+len(creates)+len(sets))
	out = append(out, removes...)
	out = append(out, creates...)
	out = append(out, sets...)
	for i := range out {
		if out[i].Kind == PatchRemove {
			out[i].Index = 0 // depth was scratch; a removal has no position
		}
	}
	return out
}

// propPatches reports the assignments turning old's props into n's.
//
// Values are compared by their rendered form rather than by ==  or DeepEqual.
// A *Struct carries the declaration and type it was checked against, and a
// reload replaces both, so comparing those pointers would report every struct
// prop in the program as changed on every reload.
func propPatches(old, n *Node) []Patch {
	var out []Patch
	for _, name := range n.PropOrder {
		nv := n.Props[name]
		ov, had := old.Props[name]
		if had && renderValue(ov) == renderValue(nv) {
			continue
		}
		out = append(out, Patch{Kind: PatchSetProp, Key: n.Key, Prop: name, Value: nv})
	}
	// A prop the node no longer sets is cleared, since the host is still
	// holding whatever it was last assigned.
	for _, name := range old.PropOrder {
		if _, still := n.Props[name]; !still {
			out = append(out, Patch{Kind: PatchSetProp, Key: n.Key, Prop: name, Value: nil})
		}
	}
	return out
}

func renderValue(v any) string { return fmt.Sprintf("%v", v) }

func sameHandlerNames(a, b *Node) bool {
	if len(a.Handlers) != len(b.Handlers) {
		return false
	}
	for i := range a.Handlers {
		if a.Handlers[i].Name != b.Handlers[i].Name {
			return false
		}
	}
	return true
}

// walkParented visits every node depth-first in mount order, reporting each
// node's parent key, its position among that parent's children, and its depth.
func walkParented(v *View, fn func(n *Node, parent Key, index, depth int)) {
	if v == nil {
		return
	}
	var walk func(nodes []*Node, parent Key, depth int)
	walk = func(nodes []*Node, parent Key, depth int) {
		for i, n := range nodes {
			fn(n, parent, i, depth)
			walk(n.Children, n.Key, depth+1)
		}
	}
	walk(v.Roots, Key{}, 0)
}

func sortByIndexDesc(p []Patch) {
	for i := 1; i < len(p); i++ {
		for j := i; j > 0 && p[j].Index > p[j-1].Index; j-- {
			p[j], p[j-1] = p[j-1], p[j]
		}
	}
}
