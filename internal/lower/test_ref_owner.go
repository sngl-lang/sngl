package lower

import (
	"duckfam.us/sngl/ir"
)

// passTestRefOwner gives a node another component writes a name of its own
// where it carries an `#id` a test reaches on the component under test.
//
// `c.b` names the `#b` the tested component's own body wrote, and every target
// finds what a test names by the id alone -- html's `data-sngl-id`, fyne's and
// gtk4's `__named_<id>` and `__present_<id>`, bubbletea's spliced instances,
// android's test tag. Renaming the other component's node is what makes each
// of those lookups the owner's, without any target learning what an owner is.
//
// It runs before anything moves a node out of the body it was written in, after
// which the component a node stands in no longer says who wrote it. A handle
// the optimizer already spliced into the tested component's body is still the
// declaring component's, since a clone shares it.
var passTestRefOwner = pass{
	name:    "TestRefOwner",
	enabled: func(Features) bool { return true },
	apply: func(pkg *ir.Package, _ Features, opts Options) error {
		root := rootComponent(pkg, opts)
		if root == nil || !opts.Tests {
			return nil
		}
		reached := testReachedIDs(pkg, root)
		if len(reached) == 0 {
			return nil
		}
		renameForeignTestIDs(pkg, root, reached, namesOrOwn(opts.names))
		return nil
	},
}

// testReachedIDs is every `#id` root's body writes that a function selects off
// an instance of root: a test's own parameter, a local holding it, or a helper
// it is handed to.
func testReachedIDs(pkg *ir.Package, root *ir.Component) map[string]bool {
	written := map[string]bool{}
	_ = ir.Walk(root, func(n ir.Node) error {
		if ni, ok := n.(*ir.NodeInst); ok && ni.ID != "" {
			written[ni.ID] = true
		}
		return nil
	})
	out := map[string]bool{}
	for _, fn := range pkg.Funcs {
		if fn == nil {
			continue
		}
		_ = ir.Walk(fn.Block, func(n ir.Node) error {
			sel, ok := n.(*ir.Select)
			if !ok || !written[sel.Field] {
				return nil
			}
			if t := unnarrowed(sel.Operand).ExprType(); t != nil && t.Kind == ir.TypeComponent && t.Decl == root {
				out[sel.Field] = true
			}
			return nil
		})
	}
	return out
}

func renameForeignTestIDs(pkg *ir.Package, root *ir.Component, reached map[string]bool, names renamer) {
	foreign := map[*ir.Var]bool{}
	for _, c := range pkg.Components {
		if c == root {
			continue
		}
		_ = ir.Walk(c, func(n ir.Node) error {
			if ni, ok := n.(*ir.NodeInst); ok && ni.Handle != nil {
				foreign[ni.Handle] = true
			}
			return nil
		})
	}
	reads := map[*ir.Var][]*ir.Ident{}
	walkPackageAndComponents(pkg, func(n ir.Node) error {
		if id, ok := n.(*ir.Ident); ok {
			if v, ok := id.Sym.(*ir.Var); ok && v.NodeHandle {
				reads[v] = append(reads[v], id)
			}
		}
		return nil
	})

	renamed := map[*ir.Var]string{}
	members := map[*ir.Component]map[string]string{}
	// inRoot is the walk of the tested component, where a node is its own unless
	// its handle says another body wrote it. A node has no handle where its id
	// was already a name in scope, and is then the body's own.
	visit := func(owner *ir.Component, inRoot bool) func(ir.Node) error {
		return func(n ir.Node) error {
			ni, ok := n.(*ir.NodeInst)
			if !ok || !reached[ni.ID] || (inRoot && (ni.Handle == nil || !foreign[ni.Handle])) {
				return nil
			}
			old := ni.ID
			name := ""
			if ni.Handle != nil {
				name = renamed[ni.Handle]
			}
			if name == "" {
				name = names.next(old, "own")
				if ni.Handle != nil {
					renamed[ni.Handle] = name
				}
			}
			renameNodeID(ni, name, reads)
			if owner != nil {
				if members[owner] == nil {
					members[owner] = map[string]string{}
				}
				members[owner][old] = name
			}
			return nil
		}
	}
	for _, c := range pkg.Components {
		owner := c
		// A foreign node standing in the root was spliced there from a body
		// the walk does not name, so no member select is renamed for it.
		if c == root {
			owner = nil
		}
		_ = ir.Walk(c, visit(owner, c == root))
	}
	_ = ir.Walk(pkg.Body, visit(nil, false))
	if len(members) == 0 {
		return
	}
	walkPackageAndComponents(pkg, func(n ir.Node) error {
		sel, ok := n.(*ir.Select)
		if !ok {
			return nil
		}
		if t := sel.Operand.ExprType(); t != nil && t.Kind == ir.TypeComponent {
			if c, ok := t.Decl.(*ir.Component); ok {
				if name, ok := members[c][sel.Field]; ok {
					sel.Field = name
				}
			}
		}
		return nil
	})
}

func walkPackageAndComponents(pkg *ir.Package, visit func(ir.Node) error) {
	_ = ir.Walk(pkg, visit)
	for _, c := range pkg.Components {
		_ = ir.Walk(c, visit)
	}
}
