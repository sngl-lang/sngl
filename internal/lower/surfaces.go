package lower

import (
	"fmt"

	"git.duckfam.us/jonathan/sngl/ir"
)

// A target whose primitive says #[gen.renders(surface)] draws each of its nodes
// as a surface of its own, and one whose view is markup writes its document
// from one of them: html's Window, the first that no `if` over state and no
// `for` can take away, with every other shown inside it. html's documentsOf
// finds that node once everything is inlined. What the lowering needs to know
// before then is which node the program wrote will become it, because
// navigation is decided per surface: a stack in the document is the target's
// to answer, and one in any other navigates in place.
//
// A node becomes a surface when its component is the marked primitive, or the
// body this target renders for it -- its override, by now -- stands one at its
// root: on html that is every `ui.window`, and on every other target nothing.
// One level only, and not ir.RenderedPrimitive's depth: a root component
// rendering a window is no surface, the window it renders is.

// surfaceSet is what the package body's surfaces are, before inlining.
type surfaceSet struct {
	// doc is the document: the first surface no `if` over state and no `for`
	// can take away.
	doc *ir.NodeInst
	// inner is every node inside a surface other than the document.
	inner map[*ir.NodeInst]bool
}

// inSurface reports whether n is inside a surface other than the document.
func (s *surfaceSet) inSurface(n *ir.NodeInst) bool { return s != nil && s.inner[n] }

// findSurfaces is the package's surfaces, nil when it renders none. A package
// whose every surface may be absent has no document, and is refused at the
// first.
//
// The roots are the package body, and a component instantiated there is
// reached through: a root component's windows are rendered where it is. The
// optimizer has decided every `if` the build could, so an `if` still standing
// is one over state.
//
// A component's body is the one this target renders, asked of its override
// rather than read off the declaration: a pass running before
// passPlatformExtensionBody swaps the override in must see the same surfaces
// as one running after it.
func findSurfaces(pkg *ir.Package, opts Options) (*surfaceSet, error) {
	memo := map[*ir.Component]bool{}
	isSurface := func(c *ir.Component) bool {
		if c == nil {
			return false
		}
		v, ok := memo[c]
		if !ok {
			v = rootsAtSurface(c, opts)
			memo[c] = v
		}
		return v
	}
	var set *surfaceSet
	var first *ir.NodeInst
	seen := map[*ir.Component]bool{}
	var walk func(stmts []ir.Stmt, always bool)
	walk = func(stmts []ir.Stmt, always bool) {
		for _, s := range stmts {
			switch n := s.(type) {
			case *ir.NodeInst:
				if isSurface(n.Component) {
					if set == nil {
						set = &surfaceSet{inner: map[*ir.NodeInst]bool{}}
					}
					if first == nil {
						first = n
					}
					if always && set.doc == nil {
						set.doc = n
						continue
					}
					_ = ir.WalkStmts(n.Children, func(s ir.Stmt) error {
						if in, ok := s.(*ir.NodeInst); ok {
							set.inner[in] = true
						}
						return nil
					})
					for _, name := range ir.SlotNames(n.Slots) {
						_ = ir.WalkStmts(n.Slots[name].Body, func(s ir.Stmt) error {
							if in, ok := s.(*ir.NodeInst); ok {
								set.inner[in] = true
							}
							return nil
						})
					}
					continue
				}
				if c := n.Component; c != nil && !seen[c] {
					seen[c] = true
					walk(c.Body, always)
				}
			case *ir.ErrorBoundary:
				// Its content stands where it does; its fallback only once
				// a raise reaches it.
				walk(n.Children, always)
				walk(n.Failed, false)
			case *ir.ContextProvider:
				walk(n.Children, always)
			default:
				// An `if` or a `for`, which may take what it holds away.
				for _, b := range ir.TransparentBlocks(s) {
					walk(*b, false)
				}
			}
		}
	}
	walk(pkg.Body, true)
	// A harness renders its root component in place of the package body.
	if root := pkg.RootDecl(); root != nil && set == nil {
		walk(root.Body, true)
	}
	if set != nil && set.doc == nil {
		return nil, fmt.Errorf("%s: this target writes its document from the first %s that no `if` over state and no `for` can take away, and every one here is under one", ir.NodePos(first), first.Name)
	}
	return set, nil
}

// rootsAtSurface reports whether c is a surface primitive, or the body this
// target renders for it has one at its root.
func rootsAtSurface(c *ir.Component, opts Options) bool {
	if ir.IsSurface(c) {
		return true
	}
	body := c.Body
	if o, ok := ir.ComponentOverride(c, opts.Platform, opts.Language); ok {
		body = o.Stmts
	}
	for _, s := range body {
		if n, ok := s.(*ir.NodeInst); ok && n.Component != nil && ir.IsSurface(n.Component) {
			return true
		}
	}
	return false
}
