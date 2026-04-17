package codegen

import (
	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/ir"
)

const maxComponentDepth = 10

// NodeVisitor is called by WalkTree for each IR node.
type NodeVisitor interface {
	VisitNode(n *ir.NodeInst) (handled bool)
	BeforeIf(stmt *ir.If)
	AfterIf(stmt *ir.If)
	BeforeFor(stmt *ir.For)
	AfterFor(stmt *ir.For)
	BeforeComponent(comp *ir.Component, inst *ir.NodeInst) bool
	AfterComponent(comp *ir.Component, inst *ir.NodeInst)
	VisitSlot(n *ir.SlotInst)
}

// TreeWalker holds state for walking a visual tree with a visitor.
type TreeWalker struct {
	Pkg      *ir.Package
	Platform string
	Visitor  NodeVisitor

	componentDepth int
	slotChildren   []ir.Stmt
}

// WalkStmts traverses a list of IR statements, calling the visitor for visual nodes.
func (tw *TreeWalker) WalkStmts(stmts []ir.Stmt) {
	for _, s := range stmts {
		tw.walkStmt(s)
	}
}

func (tw *TreeWalker) walkStmt(s ir.Stmt) {
	switch n := s.(type) {
	case *ir.NodeInst:
		tw.walkNodeInst(n)
	case *ir.If:
		tw.Visitor.BeforeIf(n)
		tw.WalkStmts(n.Body)
		if len(n.Else) > 0 {
			tw.WalkStmts(n.Else)
		}
		tw.Visitor.AfterIf(n)
	case *ir.For:
		tw.Visitor.BeforeFor(n)
		tw.WalkStmts(n.Body)
		tw.Visitor.AfterFor(n)
	case *ir.PlatformFilter:
		tw.WalkStmts(n.Body)
	case *ir.SlotInst:
		tw.Visitor.VisitSlot(n)
	}
}

func (tw *TreeWalker) walkNodeInst(n *ir.NodeInst) {
	// Component already resolved in IR: n.Component is non-nil for user components.
	handled := tw.Visitor.VisitNode(n)

	if n.Component != nil && !handled {
		tw.expandComponent(n.Component, n)
	}
}

func (tw *TreeWalker) expandComponent(comp *ir.Component, inst *ir.NodeInst) {
	tw.componentDepth++
	if tw.componentDepth > maxComponentDepth {
		tw.componentDepth--
		return
	}
	defer func() { tw.componentDepth-- }()

	if !tw.Visitor.BeforeComponent(comp, inst) {
		return
	}

	savedSlot := tw.slotChildren
	tw.slotChildren = inst.Children

	tw.WalkStmts(comp.Body)

	tw.slotChildren = savedSlot
	tw.Visitor.AfterComponent(comp, inst)
}

// SlotChildren returns the current slot children.
func (tw *TreeWalker) SlotChildren() []ir.Stmt {
	return tw.slotChildren
}

// VisualNodeName extracts the component/element name from a VisualNode's Target.
// Kept for backward compatibility with platform compat layers.
func VisualNodeName(vn *ast.VisualNode) string {
	if vn.Target == nil {
		return ""
	}
	switch t := vn.Target.(type) {
	case *ast.IdentExpr:
		return t.Name
	case *ast.SelectExpr:
		// pkg.Component
		if ident, ok := t.Operand.(*ast.IdentExpr); ok {
			return ident.Name + "." + t.Field
		}
	}
	return ""
}

// FindComponent finds a ComponentDecl by name in the document.
// Kept for backward compatibility.
func FindComponent(doc *ast.Document, name string) *ast.ComponentDecl {
	for _, s := range doc.Stmts {
		if comp, ok := s.(*ast.ComponentDecl); ok && comp.Name == name {
			return comp
		}
	}
	return nil
}
