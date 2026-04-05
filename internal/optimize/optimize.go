package optimize

import (
	"git.duckfam.us/jonathan/sngl/ast"
)

// Config holds compile-time constants for the optimization pass.
type Config struct {
	Platform string // "html", "bubbletea"
	Language string // "js", "go"
}

// Optimize mutates doc in place: evaluates constant SNGL expressions referencing
// PLATFORM/LANGUAGE and eliminates dead branches. Callers targeting multiple
// outputs should Clone the document first.
func Optimize(doc *ast.Document, cfg Config) error {
	vars := map[string]any{
		"PLATFORM": cfg.Platform,
		"LANGUAGE": cfg.Language,
	}

	// Fold constant expressions in data fields and computeds.
	for _, d := range doc.Data {
		foldExpr(&d.Init, vars)
	}
	for _, fn := range doc.Functions {
		if fn.Body.SNGL != nil {
			foldExpr(&fn.Body, vars)
		}
	}

	// Fold constant expressions in struct field defaults.
	for _, s := range doc.Structs {
		for _, f := range s.Fields {
			foldExpr(&f.Default, vars)
		}
	}

	// Fold constant expressions in style declarations.
	for _, s := range doc.Styles {
		foldExprMap(s.Props, vars)
	}

	// Fold constant expressions in component params, data, computeds, and bodies.
	for _, comp := range doc.Components {
		for _, p := range comp.Params {
			foldExpr(&p.Default, vars)
		}
		for _, d := range comp.Data {
			foldExpr(&d.Init, vars)
		}
		for _, fn := range comp.Functions {
			if fn.Body.SNGL != nil {
				foldExpr(&fn.Body, vars)
			}
		}
		comp.Body = optimizeNodes(comp.Body, vars)
	}

	// Fold and prune the app tree.
	if doc.App != nil {
		doc.App.Children = optimizeNodes(doc.App.Children, vars)
		for _, win := range doc.App.Windows {
			foldExprMap(win.Props, vars)
			for _, d := range win.Data {
				foldExpr(&d.Init, vars)
			}
			for _, fn := range win.Functions {
				if fn.Body.SNGL != nil {
					foldExpr(&fn.Body, vars)
				}
			}
			win.Children = optimizeNodes(win.Children, vars)
		}
	}
	return nil
}

// foldExpr attempts to evaluate a SNGL expression as a constant and replace it with a literal.
func foldExpr(expr *ast.Expr, vars map[string]any) {
	if expr.SNGL == nil {
		return
	}
	if !isConstExpr(expr.SNGL, vars) {
		return
	}
	val, ok := evalConst(expr.SNGL, vars)
	if !ok {
		return
	}
	expr.Literal = val
	expr.SNGL = nil
}

// foldExprMap folds all constant expressions in a map.
func foldExprMap(m map[string]ast.Expr, vars map[string]any) {
	for k, e := range m {
		foldExpr(&e, vars)
		m[k] = e
	}
}

// optimizeNodes folds expressions in visual nodes and eliminates dead branches.
func optimizeNodes(nodes []*ast.VisualNode, vars map[string]any) []*ast.VisualNode {
	var out []*ast.VisualNode
	for _, vn := range nodes {
		// Fold the If expression first.
		if vn.If != nil {
			foldExpr(vn.If, vars)
		}

		// Check if the If was folded to a literal.
		if vn.If != nil && vn.If.SNGL == nil {
			if b, ok := vn.If.Literal.(bool); ok {
				if !b {
					continue // dead branch — remove node
				}
				vn.If = nil // always true — remove guard
			}
		}

		// Eliminate for-loops over empty literal lists.
		if vn.For != nil {
			foldExpr(&vn.For.Iterable, vars)
			if vn.For.Iterable.SNGL == nil && vn.For.Iterable.Literal == nil {
				// Already nil — check if it was a SNGL ListExpr with no elements
			}
			if list, ok := vn.For.Iterable.SNGL.(*ast.ListExpr); ok && len(list.Elements) == 0 {
				continue // for over empty list — dead code
			}
		}

		// Fold expressions in the node's properties.
		foldExprPtr(vn.Key, vars)
		foldExprPtr(vn.Class, vars)
		foldExprPtr(vn.Ref, vars)
		if vn.For != nil {
			foldExpr(&vn.For.Iterable, vars)
		}
		foldExprMap(vn.Props, vars)
		foldExprMap(vn.Events, vars)

		// Unwrap trivial interpolations: "{expr}" → expr
		unwrapTrivialInterpolations(vn.Props)
		unwrapTrivialInterpolationPtr(vn.Key)
		unwrapTrivialInterpolationPtr(vn.Class)

		// Recurse into children.
		vn.Children = optimizeNodes(vn.Children, vars)

		out = append(out, vn)
	}
	return out
}

// unwrapTrivialInterpolations simplifies "{expr}" → expr in a prop map.
// A single-expression interpolation with no surrounding text is equivalent
// to the expression itself (implicit string conversion applies in prop context).
func unwrapTrivialInterpolations(m map[string]ast.Expr) {
	for k, e := range m {
		if interp, ok := e.SNGL.(*ast.InterpolationExpr); ok && len(interp.Parts) == 1 {
			if _, isLit := interp.Parts[0].(*ast.LiteralExpr); !isLit {
				e.SNGL = interp.Parts[0]
				m[k] = e
			}
		}
	}
}

func unwrapTrivialInterpolationPtr(e *ast.Expr) {
	if e == nil {
		return
	}
	if interp, ok := e.SNGL.(*ast.InterpolationExpr); ok && len(interp.Parts) == 1 {
		if _, isLit := interp.Parts[0].(*ast.LiteralExpr); !isLit {
			e.SNGL = interp.Parts[0]
		}
	}
}

func foldExprPtr(e *ast.Expr, vars map[string]any) {
	if e != nil {
		foldExpr(e, vars)
	}
}
