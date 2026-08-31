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
	//
	// Keying a loop by `key=` is what makes this reachable: an iteration then
	// keeps its identity when the list is reordered, so the node survives and
	// only its position changed. Without a key an iteration is its index, and
	// a reorder reads as every element's contents changing instead.
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

// Diff reports the patches that turn before into after.
//
// Nodes are matched by mounted Key, never by position, which is the whole
// reason Key exists: a recheck replaces every pointer in the program, so a
// reload has nothing else to match on.
//
// The order is removals first (deepest first, so a parent outlives its
// children), then creations (shallowest first, so a parent exists before what
// mounts into it), then moves, then assignments to what survived. A host may
// apply them in that order without further thought.
//
// A move says "place this node at Index among Parent's children", and moves
// arrive in the order the new tree wants. A host that detaches and re-inserts
// at Index for each in turn converges on that order.
func Diff(before, after *View) []Patch {
	var removes, creates, moves, sets []Patch

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

	moves = movePatches(before, after)

	// Deepest first among removals: a host freeing a parent must not be handed
	// its children afterwards.
	sortByIndexDesc(removes)
	// Creations arrive in pre-order already, which is parents before children.

	out := make([]Patch, 0, len(removes)+len(creates)+len(moves)+len(sets))
	out = append(out, removes...)
	out = append(out, creates...)
	out = append(out, moves...)
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

// movePatches reports the reorderings, per parent.
//
// Only relative order among *surviving* siblings counts. Inserting or removing
// a node shifts the absolute index of everything after it, and a host doing the
// insertion shifts them itself -- emitting a move for each would turn one
// insertion into a patch per following sibling.
//
// So: take the survivors in their new order, read off where each sat before,
// and keep the longest increasing run of those old positions in place. Whatever
// is not in that run is what actually moved. This is the usual keyed-list
// reconciliation, and it is minimal in the number of moves.
func movePatches(before, after *View) []Patch {
	oldIndex := map[Key]int{}
	oldParent := map[Key]Key{}
	walkParented(before, func(n *Node, p Key, i, _ int) {
		oldIndex[n.Key], oldParent[n.Key] = i, p
	})

	var out []Patch
	for _, group := range childGroups(after) {
		type survivor struct {
			key Key
			at  int // index in the new child list
			was int // index in the old child list
		}
		var surv []survivor
		for i, n := range group.children {
			was, existed := oldIndex[n.Key]
			if !existed || oldParent[n.Key] != group.parent {
				continue // created, or reparented: a create handles it
			}
			surv = append(surv, survivor{key: n.Key, at: i, was: was})
		}
		if len(surv) < 2 {
			continue
		}
		olds := make([]int, len(surv))
		for i, sv := range surv {
			olds[i] = sv.was
		}
		stable := longestIncreasingRun(olds)
		for i, sv := range surv {
			if stable[i] {
				continue
			}
			out = append(out, Patch{Kind: PatchMove, Key: sv.key, Parent: group.parent, Index: sv.at})
		}
	}
	return out
}

type childGroup struct {
	parent   Key
	children []*Node
}

// childGroups lists each parent's children, parents in mount order.
func childGroups(v *View) []childGroup {
	if v == nil {
		return nil
	}
	out := []childGroup{{parent: Key{}, children: v.Roots}}
	var walk func([]*Node)
	walk = func(nodes []*Node) {
		for _, n := range nodes {
			if len(n.Children) > 0 {
				out = append(out, childGroup{parent: n.Key, children: n.Children})
			}
			walk(n.Children)
		}
	}
	walk(v.Roots)
	return out
}

// longestIncreasingRun marks the members of a longest increasing subsequence of
// xs. Those are the elements that can stay put while the rest move around them.
func longestIncreasingRun(xs []int) []bool {
	n := len(xs)
	best := make([]int, n) // length of the LIS ending at i
	prev := make([]int, n)
	bestEnd, bestLen := -1, 0
	for i := range xs {
		best[i], prev[i] = 1, -1
		for j := range i {
			if xs[j] < xs[i] && best[j]+1 > best[i] {
				best[i], prev[i] = best[j]+1, j
			}
		}
		if best[i] > bestLen {
			bestLen, bestEnd = best[i], i
		}
	}
	keep := make([]bool, n)
	for i := bestEnd; i >= 0; i = prev[i] {
		keep[i] = true
		if prev[i] < 0 {
			break
		}
	}
	return keep
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
