package lower

import (
	"duckfam.us/sngl/ast"
	"duckfam.us/sngl/ir"
)

// passCopyKeys gives each copy of a node a test indexes its key, the index into
// every loop around it (ir.CopyKey), in ir.CopyProp: `c.cell[1][0].click()`
// names a copy by that path, and no target can turn the path into a position
// among all the copies without knowing how many each earlier iteration
// rendered. A target finds the copy by the key it was stamped with.
//
// A component built at run time is handed the key of the copy it stands in
// through a prop, as passRecursionDepth hands one its depth: the loops around
// a node may be in the body of whatever renders the instance holding it. The
// prop is an ordinary one, so an instance a render slot keeps and hands a new
// position re-stamps its nodes through the setter every prop has. An instance
// a test reaches one copy of is handed its own key the same way, which the
// target reads where it binds the instance's root.
//
// Always on, and a no-op for a build emitting no test that indexes a node.
var passCopyKeys = pass{
	name:    "CopyKeys",
	enabled: func(Features) bool { return true },
	apply:   lowerCopyKeys,
}

func lowerCopyKeys(pkg *ir.Package, _ Features, opts Options) error {
	if pkg == nil || !opts.Tests {
		return nil
	}
	indexed := ir.IndexedRefIDs(pkg)
	if len(indexed) == 0 {
		return nil
	}
	st := &copyKeys{indexed: indexed, prefix: map[*ir.Component]*ir.Param{}}
	holding := map[*ir.Component]bool{}
	for changed := true; changed; {
		changed = false
		for _, c := range pkg.Components {
			if c.RuntimeInstance && !holding[c] && st.holds(c.Stmts, holding) {
				holding[c], changed = true, true
			}
		}
	}
	// Of those, the ones an instance of which stands in a copy: under a loop,
	// or in the body of one that does. Written once, an instance names its
	// nodes no copy.
	needs := map[*ir.Component]bool{}
	place := func(stmts []ir.Stmt, inCopy bool) (changed bool) {
		placed(stmts, 0, func(n *ir.NodeInst, loops int) {
			if st.keyedInstance(n) && (loops > 0 || inCopy) {
				n.Component.KeyedCopy = true
			}
			if (holding[n.Component] || st.keyedInstance(n)) && !needs[n.Component] && (loops > 0 || inCopy) {
				needs[n.Component], changed = true, true
			}
		})
		return changed
	}
	place(pkg.Body, false)
	for changed := true; changed; {
		changed = false
		for _, c := range pkg.Components {
			if place(c.Stmts, needs[c]) {
				changed = true
			}
		}
	}
	for _, c := range pkg.Components {
		if needs[c] {
			sym := &ir.Param{Name: ir.CopyProp, Type: ir.TypString}
			c.Props = append(c.Props, &ir.Prop{Name: ir.CopyProp, Type: ir.TypString, Default: &ir.Literal{Type: ir.TypString, Value: ""}, Sym: sym})
			st.prefix[c] = sym
		}
	}
	pkg.Body = st.stamp(pkg.Body, nil, nil)
	for _, c := range pkg.Components {
		c.Stmts = st.stamp(c.Stmts, nil, st.prefix[c])
	}
	return nil
}

type copyKeys struct {
	indexed map[string]bool
	// prefix is the prop of each component built at run time that holds a
	// node a test indexes, which its instance is handed its own copy's key in.
	prefix map[*ir.Component]*ir.Param
}

// holds reports whether stmts render a node a test indexes, directly or in an
// instance of a component in holding.
func (st *copyKeys) holds(stmts []ir.Stmt, holding map[*ir.Component]bool) bool {
	found := false
	ir.WalkView(stmts, func(s ir.Stmt) bool {
		if n, ok := s.(*ir.NodeInst); ok && (st.indexed[n.ID] || holding[n.Component]) {
			found = true
		}
		return !found
	})
	return found
}

func (st *copyKeys) stamp(stmts []ir.Stmt, loops []*ir.For, prefix *ir.Param) []ir.Stmt {
	for _, s := range stmts {
		if f, ok := s.(*ir.For); ok {
			f.Body = st.stamp(f.Body, append(loops[:len(loops):len(loops)], f), prefix)
			f.Else = st.stamp(f.Else, loops, prefix)
			continue
		}
		if n, ok := s.(*ir.NodeInst); ok && (st.indexed[n.ID] || st.prefix[n.Component] != nil) {
			if key := triggerKey(loops, prefix); key != nil {
				setCopyProp(n, key)
			}
		}
		for _, b := range ir.ViewBlocks(s) {
			*b = st.stamp(*b, loops, prefix)
		}
	}
	return stmts
}

// triggerKey is the key of the copy the loops are on, inside the copy prefix holds
// the key of: "" where the instance is rendered under no loop.
func triggerKey(loops []*ir.For, prefix *ir.Param) ir.Expr {
	inner := CopyKey(loops)
	if prefix == nil {
		return inner
	}
	if inner == nil {
		return paramIdent(prefix)
	}
	return &ir.Ternary{
		Type: ir.TypString,
		Cond: &ir.Binary{Type: ir.TypBool, Op: ast.BinEq, Left: paramIdent(prefix), Right: stringLit("")},
		Then: inner,
		Else: ir.JoinCopyKey(paramIdent(prefix), CopyKey(loops)),
	}
}

func setCopyProp(n *ir.NodeInst, key ir.Expr) {
	for i := range n.Props {
		if n.Props[i].Name == ir.CopyProp {
			n.Props[i].Value = key
			return
		}
	}
	n.Props = append(n.Props, ir.Arg{Name: ir.CopyProp, Value: key})
}

// placed calls visit on each node of a view with the number of loops around
// it.
func placed(stmts []ir.Stmt, loops int, visit func(*ir.NodeInst, int)) {
	for _, s := range stmts {
		if f, ok := s.(*ir.For); ok {
			placed(f.Body, loops+1, visit)
			placed(f.Else, loops, visit)
			continue
		}
		if n, ok := s.(*ir.NodeInst); ok {
			visit(n, loops)
		}
		for _, b := range ir.ViewBlocks(s) {
			placed(*b, loops, visit)
		}
	}
}

// keyedInstance reports whether n is an instance of a component built at run
// time that a test reaches one copy of, which is handed its key to be found by
// as a node a test indexes is stamped with one.
func (st *copyKeys) keyedInstance(n *ir.NodeInst) bool {
	return st.indexed[n.ID] && n.Component != nil && n.Component.RuntimeInstance && hasRealComponentBody(n.Component)
}
