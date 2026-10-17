package lower

import (
	"slices"

	"duckfam.us/sngl/ir"
)

// passTestRefOwner gives a node another component writes a name of its own
// where it carries an `#id` a test reaches on a component it takes.
//
// `c.b` names the `#b` the tested component's own body wrote, and every target
// finds what a test names by the id alone -- html's `data-sngl-id`, fyne's and
// gtk4's `__named_<id>` and `__present_<id>`, bubbletea's spliced instances,
// android's test tag. Renaming the other component's node is what makes each
// of those lookups the owner's, without any target learning what an owner is.
// The other components include the library bodies the build renders: gtk4's
// datepicker writes a `#cal`.
//
// A build of the whole program may hold tests taking several components, and
// one id may be reached on more than one of them. Each id is then kept by the
// first such component and renamed in every other, the members selected off a
// renamed component's instances following, so that a test reaching it -- a
// select through its own instance -- names the node by its new name.
//
// It runs before anything moves a node out of the body it was written in, after
// which the component a node stands in no longer says who wrote it. A handle
// the optimizer already spliced into the tested component's body is still the
// declaring component's, since a clone shares it.
var passTestRefOwner = pass{
	name:    "TestRefOwner",
	enabled: func(Features) bool { return true },
	apply: func(pkg *ir.Package, _ Features, opts Options) error {
		if pkg == nil || !opts.Tests {
			return nil
		}
		keepers := map[string]*ir.Component{}
		for _, k := range testedComponents(pkg, opts) {
			for id := range testReachedIDs(pkg, k) {
				if keepers[id] == nil {
					keepers[id] = k
				}
			}
		}
		if len(keepers) == 0 {
			return nil
		}
		renameForeignTestIDs(pkg, keepers, renderedBodies(pkg, opts), namesOrOwn(opts.names))
		return nil
	},
}

// testedComponents is each component a test is handed, in the order the tests
// are declared.
func testedComponents(pkg *ir.Package, opts Options) []*ir.Component {
	params := testInstances(pkg, opts)
	var out []*ir.Component
	for _, fn := range pkg.Funcs {
		if fn == nil || !fn.IsTest {
			continue
		}
		for _, p := range fn.Params {
			if c, ok := p.Type.Decl.(*ir.Component); ok && params[p] && !slices.Contains(out, c) {
				out = append(out, c)
			}
		}
	}
	return out
}

// testedComponentSet is each component a test is handed in a build of the
// whole program; a build rooted at one holds its instance as the root.
func testedComponentSet(pkg *ir.Package, opts Options) map[*ir.Component]bool {
	out := map[*ir.Component]bool{}
	if opts.RootComponent != "" {
		return out
	}
	for _, c := range testedComponents(pkg, opts) {
		out[c] = true
	}
	return out
}

// testReachedIDs is every `#id` comp's body writes that a function selects off
// an instance of comp.
func testReachedIDs(pkg *ir.Package, comp *ir.Component) map[string]bool {
	written := map[string]bool{}
	_ = ir.Walk(comp, func(n ir.Node) error {
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
			if t := unnarrowed(sel.Operand).ExprType(); t != nil && t.Kind == ir.TypeComponent && t.Decl == comp {
				out[sel.Field] = true
			}
			return nil
		})
	}
	return out
}

// renderedBody is one body the build renders and the component that wrote
// it, nil for the package body.
type renderedBody struct {
	owner *ir.Component
	roots []any
}

// renderedBodies is the package body, each component of the program and each
// library component the build renders, the last read through the override
// this target picks. A library declaration here is the build's copy, which
// the build may write.
func renderedBodies(pkg *ir.Package, opts Options) []renderedBody {
	out := []renderedBody{{roots: []any{pkg.Body}}}
	seen := map[*ir.Component]bool{}
	var queue []*ir.Component
	add := func(c *ir.Component) {
		if c != nil && !seen[c] {
			seen[c] = true
			queue = append(queue, c)
		}
	}
	for _, c := range pkg.Components {
		add(c)
	}
	visit := func(n ir.Node) error {
		if ni, ok := n.(*ir.NodeInst); ok {
			add(ni.Component)
		}
		return nil
	}
	_ = ir.Walk(pkg.Body, visit)
	for len(queue) > 0 {
		c := queue[0]
		queue = queue[1:]
		rb := renderedBody{owner: c, roots: []any{c}}
		if ov, ok := ir.ComponentOverride(c, opts.Platform, opts.Language); ok {
			rb.roots = append(rb.roots, ov.Stmts)
			for _, f := range ov.Funcs {
				rb.roots = append(rb.roots, f)
			}
		}
		for _, r := range rb.roots {
			_ = ir.Walk(r, visit)
		}
		out = append(out, rb)
	}
	return out
}

func renameForeignTestIDs(pkg *ir.Package, keepers map[string]*ir.Component, bodies []renderedBody, names renamer) {
	// A handle is a keeper's own where no other component's body holds it: a
	// node the optimizer spliced into the keeper from another body shares the
	// writer's handle, and the writer's body holds it too.
	holders := map[*ir.Var]map[*ir.Component]bool{}
	reads := map[*ir.Var][]*ir.Ident{}
	seenRead := map[*ir.Ident]bool{}
	collect := func(owner *ir.Component) func(ir.Node) error {
		return func(n ir.Node) error {
			switch x := n.(type) {
			case *ir.NodeInst:
				if x.Handle != nil && owner != nil {
					if holders[x.Handle] == nil {
						holders[x.Handle] = map[*ir.Component]bool{}
					}
					holders[x.Handle][owner] = true
				}
			case *ir.Ident:
				if v, ok := x.Sym.(*ir.Var); ok && v.NodeHandle && !seenRead[x] {
					seenRead[x] = true
					reads[v] = append(reads[v], x)
				}
			}
			return nil
		}
	}
	for _, b := range bodies {
		for _, r := range b.roots {
			_ = ir.Walk(r, collect(b.owner))
		}
	}
	_ = ir.Walk(pkg, collect(nil))
	keeps := func(k *ir.Component, ni *ir.NodeInst, in *ir.Component) bool {
		if ni.Handle == nil {
			return in == k
		}
		h := holders[ni.Handle]
		return h[k] && len(h) == 1
	}

	renamed := map[*ir.Var]string{}
	members := map[*ir.Component]map[string]string{}
	for _, b := range bodies {
		visit := func(n ir.Node) error {
			ni, ok := n.(*ir.NodeInst)
			if !ok {
				return nil
			}
			k := keepers[ni.ID]
			if k == nil || keeps(k, ni, b.owner) {
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
			// A foreign node standing in a keeper was spliced there from a
			// body the walk names elsewhere, so no member select is renamed
			// for the keeper.
			if b.owner != nil && b.owner != k {
				if members[b.owner] == nil {
					members[b.owner] = map[string]string{}
				}
				members[b.owner][old] = name
			}
			return nil
		}
		for _, r := range b.roots {
			_ = ir.Walk(r, visit)
		}
	}
	if len(members) == 0 {
		return
	}
	rename := func(n ir.Node) error {
		sel, ok := n.(*ir.Select)
		if !ok {
			return nil
		}
		if t := unnarrowed(sel.Operand).ExprType(); t != nil && t.Kind == ir.TypeComponent {
			if c, ok := t.Decl.(*ir.Component); ok {
				if name, ok := members[c][sel.Field]; ok {
					sel.Field = name
				}
			}
		}
		return nil
	}
	_ = ir.Walk(pkg, rename)
	for _, b := range bodies {
		for _, r := range b.roots {
			_ = ir.Walk(r, rename)
		}
	}
}
