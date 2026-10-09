package html

// IR-native helpers used by the per-element renderers. Bridging layer
// between the IR codegen pipeline and the AST-oriented expression
// translator; per-element helpers access props/children/handlers through
// these instead of ast.VisualNode accessors.

import (
	"maps"

	"duckfam.us/sngl/ast"
	"duckfam.us/sngl/codegen"
	"duckfam.us/sngl/ir"
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
	// An attribute nobody declared was collected under its wildcard prop's
	// name; an element's attributes are the names that were written, so the
	// map is unpacked back into them here — where html turns props
	// into names.
	maps.Copy(out, codegen.WildcardProps(n))
	delete(out, attrsProp)
	return out
}

// rawElementTag reports the tag the element's tag prop names — the prop the
// #[wildcard] mark binds a matched name into, which every `html.div` sets and
// which a call site writing a tag no identifier can spell (a hyphenated custom
// element) replaces. Only a literal is one: a computed tag would have to be
// resolved at runtime, and nothing downstream can do that.
func rawElementTag(n *ir.NodeInst) (string, bool) {
	expr := codegen.NodeProp(n, tagProp)
	if expr == nil {
		return "", false
	}
	s, ok := codegen.IRLiteralString(expr)
	if !ok || s == "" {
		return "", false
	}
	return s, true
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

// nodeIsReactive reports whether a NodeInst depends on runtime state
// (any prop expression is non-literal, any handler is registered, or an
// explicit reactive marker is set).
func (g *htmlGen) nodeIsReactive(n *ir.NodeInst) bool {
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

// exprLiteralAnyIR extracts a literal Go value from an IR expression.
// Thin alias kept for call-site brevity; delegates to codegen.IRLiteralAny.
func exprLiteralAnyIR(e ir.Expr) any {
	return codegen.IRLiteralAny(e)
}
