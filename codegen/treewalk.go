package codegen

import (
	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/internal/checker"
)

const maxComponentDepth = 10

// NodeVisitor is called by WalkTree for each visual node.
type NodeVisitor interface {
	VisitNode(vn *ast.VisualNode, comp *ast.ComponentDecl) (handled bool)
	BeforeIf(stmt *ast.IfStmt)
	AfterIf(stmt *ast.IfStmt)
	BeforeFor(stmt *ast.ForStmt)
	AfterFor(stmt *ast.ForStmt)
	BeforeComponent(comp *ast.ComponentDecl, vn *ast.VisualNode) bool
	AfterComponent(comp *ast.ComponentDecl, vn *ast.VisualNode)
	VisitSlot(vn *ast.VisualNode)
}

// TreeWalker holds state for walking a visual tree with a visitor.
type TreeWalker struct {
	Doc      *ast.Document    // v1: find components by walking Stmts
	Pkg      *checker.Package // v2: find components via Package.Components
	Platform string
	Visitor  NodeVisitor

	componentDepth int
	slotChildren   []ast.Stmt
}

// WalkStmts traverses a list of statements, calling the visitor for visual nodes.
func (tw *TreeWalker) WalkStmts(stmts []ast.Stmt) {
	for _, s := range stmts {
		tw.walkStmt(s)
	}
}

func (tw *TreeWalker) walkStmt(s ast.Stmt) {
	switch n := s.(type) {
	case *ast.VisualNode:
		tw.walkVisualNode(n)
	case *ast.IfStmt:
		tw.Visitor.BeforeIf(n)
		tw.WalkStmts(n.Body.Stmts)
		if len(n.Else.Stmts) > 0 {
			tw.WalkStmts(n.Else.Stmts)
		}
		tw.Visitor.AfterIf(n)
	case *ast.ForStmt:
		tw.Visitor.BeforeFor(n)
		tw.WalkStmts(n.Body.Stmts)
		tw.Visitor.AfterFor(n)
	}
}

func (tw *TreeWalker) walkVisualNode(vn *ast.VisualNode) {
	name := VisualNodeName(vn)
	if name == "slot" {
		tw.Visitor.VisitSlot(vn)
		return
	}

	comp := tw.findComponent(name)
	handled := tw.Visitor.VisitNode(vn, comp)

	if comp != nil && !handled {
		tw.expandComponent(comp, vn)
	}
}

// findComponent looks up a component by name, preferring Package when available.
func (tw *TreeWalker) findComponent(name string) *ast.ComponentDecl {
	if tw.Pkg != nil {
		for _, c := range tw.Pkg.Components {
			if c.Name == name {
				return c.AST
			}
		}
		return nil
	}
	return FindComponent(tw.Doc, name)
}

func (tw *TreeWalker) expandComponent(comp *ast.ComponentDecl, vn *ast.VisualNode) {
	tw.componentDepth++
	if tw.componentDepth > maxComponentDepth {
		tw.componentDepth--
		return
	}
	defer func() { tw.componentDepth-- }()

	if !tw.Visitor.BeforeComponent(comp, vn) {
		return
	}

	savedSlot := tw.slotChildren
	tw.slotChildren = vn.Block.Stmts

	tw.WalkStmts(comp.Body.Stmts)

	tw.slotChildren = savedSlot
	tw.Visitor.AfterComponent(comp, vn)
}

// SlotChildren returns the current slot children.
func (tw *TreeWalker) SlotChildren() []ast.Stmt {
	return tw.slotChildren
}

// FindComponent finds a ComponentDecl by name in the document.
func FindComponent(doc *ast.Document, name string) *ast.ComponentDecl {
	for _, s := range doc.Stmts {
		if comp, ok := s.(*ast.ComponentDecl); ok && comp.Name == name {
			return comp
		}
	}
	return nil
}
