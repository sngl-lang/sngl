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
	// PatchMove places a surviving node at Index among Parent's children.
	PatchMove
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
	case PatchMove:
		return "move"
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
	case PatchMove:
		return fmt.Sprintf("move %s to %s at %d", p.Key, p.Parent, p.Index)
	default:
		return fmt.Sprintf("%s %s", p.Kind, p.Key)
	}
}

// Diff reports the patches that turn before into after. Nodes match by mounted
// Key, never by position: a recheck replaces every pointer in the program.
//
// Removals first, deepest first. Then creations and moves *interleaved*, one
// ascending pass per parent -- separating them does not converge, because a
// creation's index is a position in the finished list and the list is not
// finished yet. Assignments last.
//
// A minimal move set via longest-increasing-subsequence would need moves to say
// "insert before this node"; this protocol says "at this index", so Diff
// simulates the host's list instead.
func Diff(before, after *View) []Patch {
	// gone is what the host will not be holding once removals are applied: a
	// node dropped or replaced, and everything beneath it, since Host.Remove
	// unmounts a subtree.
	gone := goneKeys(before, after)

	var removes []Patch
	walkParented(before, func(n *Node, parent Key, _, depth int) {
		// Only the top of each removed subtree is named: the host takes the
		// rest with it.
		if gone[n.Key] && !gone[parent] {
			removes = append(removes, Patch{Kind: PatchRemove, Key: n.Key, Index: depth})
		}
	})
	sortByIndexDesc(removes)
	for i := range removes {
		removes[i].Index = 0 // depth was scratch; a removal has no position
	}

	var structure, sets []Patch
	for _, group := range childGroups(after) {
		structure = append(structure, placeChildren(before, gone, group)...)
	}
	walkParented(after, func(n *Node, _ Key, _, _ int) {
		old, ok := before.At(n.Key)
		if !ok || gone[n.Key] {
			return // created, and a creation carries its props and events
		}
		sets = append(sets, propPatches(old, n)...)
		if !sameHandlerNames(old, n) {
			sets = append(sets, Patch{Kind: PatchRebind, Key: n.Key, Node: n})
		}
	})

	out := make([]Patch, 0, len(removes)+len(structure)+len(sets))
	out = append(out, removes...)
	out = append(out, structure...)
	out = append(out, sets...)
	return out
}

// goneKeys is every key the host will have dropped after removals: one absent
// from the new tree, one whose element changed -- no host turns a button into a
// text field by assignment -- and every descendant of either.
func goneKeys(before, after *View) map[Key]bool {
	gone := map[Key]bool{}
	walkParented(before, func(n *Node, parent Key, _, _ int) {
		if gone[parent] {
			gone[n.Key] = true
			return
		}
		now, ok := after.At(n.Key)
		if !ok || now.Name != n.Name {
			gone[n.Key] = true
		}
	})
	return gone
}

// placeChildren emits the creations and moves that bring one parent's children
// into their new order, by simulating what the host is holding.
//
// The simulation is the correctness argument: after step i the host's first
// i+1 children are exactly the wanted ones, so the last step leaves the whole
// list right. A survivor already in place costs nothing.
func placeChildren(before *View, gone map[Key]bool, group childGroup) []Patch {
	// What the host holds under this parent once removals are done.
	var was []*Node
	if p, ok := before.At(group.parent); ok {
		was = HostChildren(p.Children)
	} else if before != nil && group.parent == (Key{}) {
		was = hostRoots(before)
	}
	var cur []Key
	for _, c := range was {
		if !gone[c.Key] {
			cur = append(cur, c.Key)
		}
	}

	var out []Patch
	for i, n := range group.children {
		_, existed := before.At(n.Key)
		if !existed || gone[n.Key] {
			out = append(out, Patch{Kind: PatchCreate, Key: n.Key, Node: n, Parent: group.parent, Index: i})
			cur = insertKey(cur, n.Key, i)
			continue
		}
		if i < len(cur) && cur[i] == n.Key {
			continue // already where it belongs
		}
		out = append(out, Patch{Kind: PatchMove, Key: n.Key, Parent: group.parent, Index: i})
		cur = insertKey(removeKey(cur, n.Key), n.Key, i)
	}
	return out
}

func insertKey(keys []Key, k Key, at int) []Key {
	if at < 0 || at > len(keys) {
		at = len(keys)
	}
	keys = append(keys, Key{})
	copy(keys[at+1:], keys[at:])
	keys[at] = k
	return keys
}

func removeKey(keys []Key, k Key) []Key {
	for i, x := range keys {
		if x == k {
			return append(keys[:i], keys[i+1:]...)
		}
	}
	return keys
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
		out = append(out, Patch{Kind: PatchSetProp, Key: n.Key, Prop: name, Value: wireValue(nv)})
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

type childGroup struct {
	parent   Key
	children []*Node
}

// childGroups lists each parent's children, parents in mount order.
func childGroups(v *View) []childGroup {
	if v == nil {
		return nil
	}
	out := []childGroup{{parent: Key{}, children: hostRoots(v)}}
	var walk func([]*Node)
	walk = func(nodes []*Node) {
		for _, n := range nodes {
			kids := HostChildren(n.Children)
			if len(kids) > 0 {
				out = append(out, childGroup{parent: n.Key, children: kids})
			}
			walk(kids)
		}
	}
	walk(hostRoots(v))
	return out
}

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

// HostChildren is a node's children as a host sees them: one of the program's
// own components is transparent, and what its body rendered takes its place.
//
// A toolkit has a widget for `vbox`, none for `readout.Readout`. The tree keeps
// the instantiation as a node because an inspector wants it, but sending it to
// a host asks for a widget that cannot exist -- and everything inside it goes
// down with the failure.
func HostChildren(nodes []*Node) []*Node {
	var out []*Node
	for _, n := range nodes {
		if n.IsUserComponent() {
			out = append(out, HostChildren(n.Children)...)
			continue
		}
		out = append(out, n)
	}
	return out
}

// hostRoots is HostChildren for a whole view.
func hostRoots(v *View) []*Node {
	if v == nil {
		return nil
	}
	return HostChildren(v.Roots)
}

// walkParented visits every node a host holds, depth-first in mount order,
// reporting each node's parent key, its position among that parent's children,
// and its depth. Components are transparent; see HostChildren.
func walkParented(v *View, fn func(n *Node, parent Key, index, depth int)) {
	if v == nil {
		return
	}
	var walk func(nodes []*Node, parent Key, depth int)
	walk = func(nodes []*Node, parent Key, depth int) {
		for i, n := range nodes {
			fn(n, parent, i, depth)
			walk(HostChildren(n.Children), n.Key, depth+1)
		}
	}
	walk(hostRoots(v), Key{}, 0)
}

func sortByIndexDesc(p []Patch) {
	for i := 1; i < len(p); i++ {
		for j := i; j > 0 && p[j].Index > p[j-1].Index; j-- {
			p[j], p[j-1] = p[j-1], p[j]
		}
	}
}
