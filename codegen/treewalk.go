package codegen

import (
	"git.duckfam.us/jonathan/sngl/ast"
)

const maxComponentDepth = 10

// NodeVisitor is called by WalkTree for each visual node. Platforms provide
// this to control how nodes are processed without reimplementing traversal
// of if/for/component/slot structure.
type NodeVisitor interface {
	// VisitNode is called for each node (primitive or user component).
	// If the node is a user component, comp is non-nil and the visitor can
	// choose to handle it (return true) or let the walker expand it inline
	// (return false). For primitives, comp is nil and the return value is
	// ignored.
	VisitNode(vn *ast.VisualNode, comp *ast.Component) (handled bool)

	// BeforeIf is called before processing a node with an If condition.
	BeforeIf(vn *ast.VisualNode)
	// AfterIf is called after processing a node with an If condition.
	AfterIf(vn *ast.VisualNode)

	// BeforeFor is called before processing a node with a For clause.
	BeforeFor(vn *ast.VisualNode)
	// AfterFor is called after processing a node with a For clause.
	AfterFor(vn *ast.VisualNode)

	// BeforeComponent is called before inlining a user component body.
	// Return false to skip expansion (e.g., recursion limit).
	BeforeComponent(comp *ast.Component, vn *ast.VisualNode) bool
	// AfterComponent is called after inlining a user component body.
	AfterComponent(comp *ast.Component, vn *ast.VisualNode)

	// VisitSlot is called for <slot> nodes.
	VisitSlot(vn *ast.VisualNode)
}

// TreeWalker holds state for walking a visual tree with a visitor.
type TreeWalker struct {
	Doc      *ast.Document
	Platform string
	Visitor  NodeVisitor

	componentDepth int
	slotChildren   []*ast.VisualNode
}

// Walk traverses a list of visual nodes, calling the visitor for each.
func (tw *TreeWalker) Walk(nodes []*ast.VisualNode) {
	for _, vn := range nodes {
		tw.walkNode(vn)
	}
}

func (tw *TreeWalker) walkNode(vn *ast.VisualNode) {
	hasIf := vn.If != nil
	if hasIf {
		tw.Visitor.BeforeIf(vn)
	}

	hasFor := vn.For != nil
	if hasFor {
		tw.Visitor.BeforeFor(vn)
	}

	tw.walkNodeInner(vn)

	if hasFor {
		tw.Visitor.AfterFor(vn)
	}

	if hasIf {
		tw.Visitor.AfterIf(vn)
	}
}

func (tw *TreeWalker) walkNodeInner(vn *ast.VisualNode) {
	if vn.Component == "slot" {
		tw.Visitor.VisitSlot(vn)
		return
	}

	// Resolve user/abstract component
	comp := tw.Doc.FindComponent(vn.Component)

	// Let the visitor handle this node. If it's a user component and the
	// visitor returns true, the visitor handled it (e.g., BubbleTea emits
	// a method call). Otherwise we inline the component body.
	handled := tw.Visitor.VisitNode(vn, comp)

	if comp != nil && !handled {
		tw.expandComponent(comp, vn)
	}
}

// expandComponent inlines a user component body at the call site.
func (tw *TreeWalker) expandComponent(comp *ast.Component, vn *ast.VisualNode) {
	tw.componentDepth++
	if tw.componentDepth > maxComponentDepth {
		tw.componentDepth--
		return
	}
	defer func() { tw.componentDepth-- }()

	if !tw.Visitor.BeforeComponent(comp, vn) {
		return
	}

	body := ResolveComponentBody(comp, tw.Platform)

	savedSlot := tw.slotChildren
	tw.slotChildren = vn.Children

	tw.Walk(body)

	tw.slotChildren = savedSlot

	tw.Visitor.AfterComponent(comp, vn)
}

// SlotChildren returns the current slot children (caller's children for
// the component being expanded). Visitors can use this in VisitSlot.
func (tw *TreeWalker) SlotChildren() []*ast.VisualNode {
	return tw.slotChildren
}

// ResolveComponentBody returns the appropriate body for a component,
// checking PlatformBodies first for platform-specific overrides.
func ResolveComponentBody(comp *ast.Component, platform string) []*ast.VisualNode {
	if comp.PlatformBodies != nil {
		if body, ok := comp.PlatformBodies[platform]; ok {
			return body
		}
	}
	return comp.Body
}
