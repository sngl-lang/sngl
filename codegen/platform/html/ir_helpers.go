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

// nodeEventBody returns the AST event handler body for a named event if
// present. Used while the handler-emission helpers still accept an AST
// StmtBlock.
func nodeEventBody(n *ir.NodeInst, name string) *ast.StmtBlock {
	h := codegen.NodeHandler(n, name)
	if h == nil || h.AST == nil {
		return nil
	}
	return &h.AST.Body
}

// evalStaticStringIR resolves a prop expression to a static string when it
// is a compile-time string literal. Returns "" if not a literal string.
func (g *htmlGen) evalStaticStringIR(props map[string]ir.Expr, key string) string {
	e, ok := props[key]
	if !ok {
		return ""
	}
	if s, ok := codegen.IRLiteralString(e); ok {
		return s
	}
	return ""
}

// evalInitialStringIR tries to resolve an IR expression to a static string
// for the initial HTML render — delegates to the AST path (the JS
// translator already handles component-param lookup through the scope).
func (g *htmlGen) evalInitialStringIR(e ir.Expr) string {
	if e == nil || codegen.IRIsLiteral(e) {
		return ""
	}
	return g.evalInitialString(ir.ConvertExpr(e))
}

// exprToJSIR translates an IR expression to its JavaScript form through
// the existing AST-based translator. Eventually superseded by a direct
// JsIRContext call.
func (g *htmlGen) exprToJSIR(e ir.Expr) string {
	if e == nil {
		return `""`
	}
	return g.exprToJS(ir.ConvertExpr(e))
}

// literalToJSIR translates a literal IR expression to a JS literal.
func (g *htmlGen) literalToJSIR(e ir.Expr) string {
	if e == nil {
		return `""`
	}
	return g.literalToJS(ir.ConvertExpr(e))
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
