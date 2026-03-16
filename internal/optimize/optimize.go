package optimize

import (
	"git.duckfam.us/jonathan/sngl/ast"
	"github.com/google/cel-go/cel"
)

// Config holds compile-time constants for the optimization pass.
type Config struct {
	Platform string // "html", "bubbletea"
	Language string // "js", "go"
}

// Optimize mutates doc in place: evaluates constant CEL expressions referencing
// PLATFORM/LANGUAGE and eliminates dead branches. Callers targeting multiple
// outputs should Clone the document first.
func Optimize(doc *ast.Document, cfg Config) {
	env, err := buildEnv()
	if err != nil {
		return // silently skip optimization if env creation fails
	}
	vars := map[string]any{
		"PLATFORM": cfg.Platform,
		"LANGUAGE": cfg.Language,
	}

	// Fold constant expressions in data fields and computeds.
	for _, d := range doc.Data {
		foldExpr(env, &d.Init, vars)
	}
	for _, c := range doc.Computeds {
		foldExpr(env, &c.Expr, vars)
	}

	// Fold constant expressions in struct field defaults.
	for _, s := range doc.Structs {
		for _, f := range s.Fields {
			foldExpr(env, &f.Default, vars)
		}
	}

	// Fold constant expressions in style declarations.
	for _, s := range doc.Styles {
		foldExprMap(env, s.Props, vars)
	}

	// Fold constant expressions in component params and bodies.
	for _, comp := range doc.Components {
		for _, p := range comp.Params {
			foldExpr(env, &p.Default, vars)
		}
		comp.Body = optimizeNodes(env, comp.Body, vars)
	}

	// Fold and prune the app tree.
	if doc.App != nil {
		doc.App.Children = optimizeNodes(env, doc.App.Children, vars)
	}
}

// buildEnv creates a minimal CEL environment with only PLATFORM and LANGUAGE.
func buildEnv() (*cel.Env, error) {
	return cel.NewEnv(
		cel.Variable("PLATFORM", cel.StringType),
		cel.Variable("LANGUAGE", cel.StringType),
	)
}

// foldExpr attempts to evaluate expr as a constant and replace it with a literal.
func foldExpr(env *cel.Env, expr *ast.Expr, vars map[string]any) {
	if expr.CEL == "" || expr.AST == nil {
		return
	}
	nativeAST := expr.AST.NativeRep()
	if nativeAST == nil {
		return
	}
	if !isConstExpr(nativeAST.Expr()) {
		return
	}
	val, ok := evalConst(env, expr, vars)
	if !ok {
		return
	}
	expr.Literal = val
	expr.CEL = ""
	expr.AST = nil
}

// foldExprMap folds all constant expressions in a map.
func foldExprMap(env *cel.Env, m map[string]ast.Expr, vars map[string]any) {
	for k, e := range m {
		foldExpr(env, &e, vars)
		m[k] = e
	}
}

// optimizeNodes folds expressions in visual nodes and eliminates dead branches.
func optimizeNodes(env *cel.Env, nodes []*ast.VisualNode, vars map[string]any) []*ast.VisualNode {
	var out []*ast.VisualNode
	for _, vn := range nodes {
		// Fold the If expression first.
		if vn.If != nil {
			foldExpr(env, vn.If, vars)
		}

		// Check if the If was folded to a literal.
		if vn.If != nil && vn.If.CEL == "" && vn.If.AST == nil {
			if b, ok := vn.If.Literal.(bool); ok {
				if !b {
					continue // dead branch — remove node
				}
				vn.If = nil // always true — remove guard
			}
		}

		// Fold expressions in the node's properties.
		foldExprPtr(env, vn.ID, vars)
		foldExprPtr(env, vn.Key, vars)
		foldExprPtr(env, vn.Class, vars)
		foldExprPtr(env, vn.Ref, vars)
		if vn.For != nil {
			foldExpr(env, &vn.For.Iterable, vars)
		}
		foldExprMap(env, vn.Props, vars)
		foldExprMap(env, vn.Events, vars)
		foldExprMap(env, vn.StyleAttrs, vars)
		foldExprMap(env, vn.StyleBlock, vars)
		for _, an := range vn.AttrNodes {
			foldExprMap(env, an.Props, vars)
		}

		// Recurse into children.
		vn.Children = optimizeNodes(env, vn.Children, vars)

		out = append(out, vn)
	}
	return out
}

func foldExprPtr(env *cel.Env, e *ast.Expr, vars map[string]any) {
	if e != nil {
		foldExpr(env, e, vars)
	}
}
