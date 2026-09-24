package checker

import (
	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/ir"
)

// checkGenInputs checks each `cache.inputs` directive pass1 found at the root
// of a file: the tree is an ordinary one, so a misspelled input kind is an
// unresolved name and a widget written there is a member of the wrong family,
// and every value in it must be constant.
//
// Nothing downstream reads the checked tree. The store holding a generated
// file reads the directive off the parsed source before any checking, since
// deciding whether the file is current is what decides whether to check it at
// all; this is what holds a hand-written or stored directive to the same
// vocabulary the store understands.
func (c *checker) checkGenInputs() {
	for _, vn := range c.genInputs {
		c.pushScope()
		if root, ok := c.checkVisualNodeIR(vn).(*ir.NodeInst); ok {
			c.requireConstGenInputs(root)
		}
		c.popScope()
	}
}

// requireConstGenInputs holds every value in the directive to what the build
// can evaluate, for the reason requireConstOutputTree gives: an input is
// compared against the world before anything runs.
func (c *checker) requireConstGenInputs(n *ir.NodeInst) {
	for _, p := range n.Props {
		if ir.IsConst(p.Value) {
			continue
		}
		at := p.NamePos
		if at == (ast.Pos{}) {
			if sp := stmtPos(n.AST); sp != nil {
				at = *sp
			}
		}
		c.error(at, "input %q must be constant: a generated file's inputs are checked before anything runs", p.Name)
	}
	for _, child := range n.Children {
		if ni, ok := child.(*ir.NodeInst); ok {
			c.requireConstGenInputs(ni)
		}
	}
}
