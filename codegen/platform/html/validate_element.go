package html

import (
	"fmt"
	"slices"

	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/ir"
)

// validateRawElements rejects a raw element this platform cannot render.
//
// Both cases were silently produced instead. A tag that is not a literal, or
// absent, fell back to the node's own name and emitted `<element>` -- markup
// for a tag that does not exist. A wildcard attribute map that is not a literal
// was skipped entirely, so a declared `attrs map<string, string>` holding real
// attributes reached the output as nothing at all.
//
// Both are static-render limits rather than facts about the DOM: the tag names
// the element to create, and the attribute names have to be known to be written
// as attributes. Saying so is the fix; guessing was the bug.
func validateRawElements(pkg *ir.Package) error {
	var bad error
	ir.WalkStmts(pkg, func(s ir.Stmt) error {
		n, ok := s.(*ir.NodeInst)
		if !ok || n.Component == nil || bad != nil {
			return nil
		}
		if !isElement(n.Component) {
			return nil
		}
		expr := codegen.NodeProp(n, tagProp)
		if expr == nil {
			bad = fmt.Errorf("%s: a raw element needs its %q prop to name a tag", nodePos(n), tagProp)
			return nil
		}
		if tag, ok := codegen.IRLiteralString(expr); !ok || tag == "" {
			bad = fmt.Errorf("%s: %q must be a string literal: the tag names the element to create, and this one is not known until it runs",
				nodePos(n), tagProp)
			return nil
		}
		if attrs := codegen.NodeProp(n, attrsProp); attrs != nil {
			if _, ok := attrs.(*ir.MapLitIR); !ok {
				bad = fmt.Errorf("%s: %q must be a map literal: each attribute is written by name, and these names are not known until it runs",
					nodePos(n), attrsProp)
				return nil
			}
		}
		return nil
	})
	return bad
}

// firstUnrenderedNode reports a library node html has nothing to render with,
// whether it is still a node or was flattened into a createNode: html writes
// any other name out as a tag, so `<vbox>` would reach the page. What html
// renders from a library is its own package's elements and a canvas; a
// library declaration the lowering copied into the package is a component.
func firstUnrenderedNode(pkg *ir.Package) error {
	var bad error
	declines := func(c *ir.Component) bool {
		return c != nil && c.Stdlib && c.Pkg != "sngl:platform/html" && !slices.Contains(pkg.Components, c)
	}
	ir.WalkStmts(pkg, func(s ir.Stmt) error {
		if bad != nil {
			return nil
		}
		switch n := s.(type) {
		case *ir.NodeInst:
			if declines(n.Component) && !ir.IsShapeContainer(n) {
				bad = codegen.UnimplementedNode(n.Component, n.AST, n.Name, "html")
			}
		case *ir.LocalVar:
			if n.NodeAST == nil || n.CanvasNode != nil || n.Type == nil || n.Type.Kind != ir.TypeComponent {
				return nil
			}
			comp, _ := n.Type.Decl.(*ir.Component)
			if declines(comp) {
				bad = codegen.UnimplementedNode(comp, n.NodeAST, n.Name, "html")
			}
		}
		return nil
	})
	return bad
}
