package html

// IR-native helpers used by the per-element renderers. Bridging layer
// between the IR codegen pipeline and the AST-oriented expression
// translator; per-element helpers access props/children/handlers through
// these instead of ast.VisualNode accessors.

import (
	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/ir"
)

// nodeProps builds a name → ir.Expr map from a NodeInst's Props.
func nodeProps(n *ir.NodeInst) map[string]ir.Expr {
	if n == nil {
		return nil
	}
	out := make(map[string]ir.Expr, len(n.Props))
	for _, p := range n.Props {
		if p.Name == "" {
			continue
		}
		out[p.Name] = p.Value
	}
	return out
}

// nodePos extracts the source position of a NodeInst (for preview mode).
func nodePos(n *ir.NodeInst) ast.Pos {
	if n == nil {
		return ast.Pos{}
	}
	switch s := n.AST.(type) {
	case *ast.VisualNode:
		return s.Pos
	case *ast.CallStmt:
		return s.Pos
	}
	return ast.Pos{}
}

// nodeIsReactiveIR reports whether a NodeInst depends on runtime state
// (any prop expression is non-literal, any handler is registered, or an
// explicit reactive marker is set).
func (g *htmlGen) nodeIsReactiveIR(n *ir.NodeInst) bool {
	if n == nil {
		return false
	}
	for _, p := range n.Props {
		if codegen.IRIsReactive(p.Value) {
			return true
		}
	}
	if len(n.Handlers) > 0 {
		return true
	}
	return false
}

// previewAttrsIR returns preview mode data attributes (sngl-line/col) for
// a NodeInst's source position.
func (g *htmlGen) previewAttrsIR(n *ir.NodeInst) string {
	return g.previewAttrs(nodePos(n))
}

// exprLiteralAnyIR extracts a literal Go value from an IR expression.
// Returns nil for non-literals or unrepresentable types. Used by
// renderers that iterate over static list/struct literals (tables,
// trees, etc.).
func exprLiteralAnyIR(e ir.Expr) any {
	if e == nil {
		return nil
	}
	return codegen.ExprLiteralAny(ir.ConvertExpr(e))
}
