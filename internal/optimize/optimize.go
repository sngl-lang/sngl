package optimize

import (
	"fmt"
	"log/slog"
	"maps"

	"git.duckfam.us/jonathan/sngl/ast"
)

// Config holds compile-time constants for the optimization pass.
type Config struct {
	Platform string // "html", "bubbletea"
	Language string // "js", "go"
	Dir      string // project directory (for compile-time go run execution)
}

// foldCtx carries state needed during constant folding.
type foldCtx struct {
	vars          map[string]any
	nativeImports map[string]*ast.NativeDecls
	dir           string
	fileAssets    []ast.FileAsset // files referenced via file:// path() calls
}

// Optimize mutates doc in place: evaluates constant SNGL expressions referencing
// PLATFORM/LANGUAGE and eliminates dead branches. Callers targeting multiple
// outputs should Clone the document first.
func Optimize(doc *ast.Document, cfg Config) error {
	ctx := &foldCtx{
		vars: map[string]any{
			"PLATFORM": cfg.Platform,
			"LANGUAGE": cfg.Language,
		},
		nativeImports: doc.NativeImports,
		dir:           cfg.Dir,
	}

	// Fold const declarations and register their values for downstream use.
	for _, c := range doc.Consts {
		foldExpr(&c.Init, ctx)
		if c.Init.Literal != nil {
			ctx.vars[c.Name] = c.Init.Literal
			slog.Debug("const folded", "name", c.Name, "value", fmt.Sprint(c.Init.Literal))
		}
	}

	// Fold constant expressions in data fields and computeds.
	for _, d := range doc.Data {
		foldExpr(&d.Init, ctx)
	}
	for _, fn := range doc.Functions {
		if fn.Body.SNGL != nil {
			foldExpr(&fn.Body, ctx)
		}
	}

	// Fold constant expressions in struct field defaults.
	for _, s := range doc.Structs {
		for _, f := range s.Fields {
			foldExpr(&f.Default, ctx)
		}
	}

	// Fold constant expressions in style declarations.
	for _, s := range doc.Styles {
		foldExprMap(s.Props, ctx)
	}

	// Fold constant expressions in component params, data, computeds, and bodies.
	for _, comp := range doc.Components {
		for _, p := range comp.Params {
			foldExpr(&p.Default, ctx)
		}
		for _, d := range comp.Data {
			foldExpr(&d.Init, ctx)
		}
		for _, fn := range comp.Functions {
			if fn.Body.SNGL != nil {
				foldExpr(&fn.Body, ctx)
			}
		}
		comp.Body = optimizeNodes(comp.Body, ctx)
	}

	// Fold and prune the app tree.
	if doc.App != nil {
		doc.App.Children = optimizeNodes(doc.App.Children, ctx)
		for _, win := range doc.App.Windows {
			foldExprMap(win.Props, ctx)
			for _, d := range win.Data {
				foldExpr(&d.Init, ctx)
			}
			for _, fn := range win.Functions {
				if fn.Body.SNGL != nil {
					foldExpr(&fn.Body, ctx)
				}
			}
			win.Children = optimizeNodes(win.Children, ctx)
		}
		doc.App.Windows = expandForWindows(doc.App.Windows, ctx)
	}
	doc.FileAssets = append(doc.FileAssets, ctx.fileAssets...)

	// Dead code elimination: remove consts/data/functions that are no longer
	// referenced after compile-time expansion.
	shakeUnused(doc)

	return nil
}

// expandForWindows expands windows with For clauses over const iterables
// into concrete windows, one per element.
func expandForWindows(windows []*ast.Window, ctx *foldCtx) []*ast.Window {
	var out []*ast.Window
	for _, win := range windows {
		if win.For == nil {
			out = append(out, win)
			continue
		}
		// Try to evaluate the iterable as a const list
		foldExpr(&win.For.Iterable, ctx)
		if win.For.Iterable.Literal == nil {
			out = append(out, win) // can't expand, keep as-is
			continue
		}
		items, ok := win.For.Iterable.Literal.([]any)
		if !ok {
			out = append(out, win)
			continue
		}
		for i, item := range items {
			// Create a new fold context with the loop variable bound
			loopCtx := &foldCtx{
				vars:          make(map[string]any, len(ctx.vars)+2),
				nativeImports: ctx.nativeImports,
				dir:           ctx.dir,
			}
			maps.Copy(loopCtx.vars, ctx.vars)
			loopCtx.vars[win.For.Variable] = item
			if win.For.IndexVar != "" {
				loopCtx.vars[win.For.IndexVar] = i
			}

			// Create a concrete window, cloning children so each iteration gets its own copy.
			clonedChildren := ast.CloneVisualNodes(win.Children)
			clonedChildren = optimizeNodes(clonedChildren, loopCtx)
			concrete := &ast.Window{
				Pos:      win.Pos,
				Name:     win.Name,
				HasProps: win.HasProps,
				Children: clonedChildren,
			}
			// Fold window name from props if available
			if win.Props != nil {
				concrete.Props = make(map[string]ast.Expr, len(win.Props))
				concrete.PropOrder = make([]string, len(win.PropOrder))
				copy(concrete.PropOrder, win.PropOrder)
				for k, v := range win.Props {
					e := v
					foldExpr(&e, loopCtx)
					concrete.Props[k] = e
				}
			}
			// Derive window name from props if no static name.
			// Prefer href (URL path), then slug, then title.
			if concrete.Name == "" {
				for _, key := range []string{"href", "slug", "title"} {
					if e, ok := concrete.Props[key]; ok && e.Literal != nil {
						if s, ok := e.Literal.(string); ok && s != "" {
							concrete.Name = s
							break
						}
					}
				}
			}
			out = append(out, concrete)
		}
	}
	return out
}

// foldExpr attempts to evaluate a SNGL expression as a constant and replace it with a literal.
func foldExpr(expr *ast.Expr, ctx *foldCtx) {
	if expr.SNGL == nil {
		return
	}
	if !isConstExpr(expr.SNGL, ctx) {
		return
	}
	val, ok := evalConst(expr.SNGL, ctx)
	if !ok {
		return
	}
	expr.Literal = val
	expr.SNGL = nil
}

// foldExprMap folds all constant expressions in a map.
func foldExprMap(m map[string]ast.Expr, ctx *foldCtx) {
	for k, e := range m {
		foldExpr(&e, ctx)
		m[k] = e
	}
}

// optimizeNodes folds expressions in visual nodes and eliminates dead branches.
func optimizeNodes(nodes []*ast.VisualNode, ctx *foldCtx) []*ast.VisualNode {
	var out []*ast.VisualNode
	for _, vn := range nodes {
		// Fold the If expression first.
		if vn.If != nil {
			foldExpr(vn.If, ctx)
		}

		// Check if the If was folded to a literal.
		if vn.If != nil && vn.If.SNGL == nil {
			if b, ok := vn.If.Literal.(bool); ok {
				if !b {
					slog.Debug("dead branch eliminated", "node", vn.Component, "pos", vn.Pos)
					continue // dead branch — remove node
				}
				slog.Debug("guard removed (always true)", "node", vn.Component, "pos", vn.Pos)
				vn.If = nil // always true — remove guard
			}
		}

		// Expand or eliminate for-loops over const literal lists.
		if vn.For != nil {
			foldExpr(&vn.For.Iterable, ctx)
			// Check folded literal: expand const list at compile time.
			if items, ok := vn.For.Iterable.Literal.([]any); ok {
				if len(items) == 0 {
					continue // for over empty list — dead code
				}
				// Expand: replace for-loop with N copies of the node,
				// each with the loop variable bound to the element.
				for i, item := range items {
					loopCtx := &foldCtx{
						vars:          make(map[string]any, len(ctx.vars)+2),
						nativeImports: ctx.nativeImports,
						dir:           ctx.dir,
					}
					maps.Copy(loopCtx.vars, ctx.vars)
					loopCtx.vars[vn.For.Variable] = item
					if vn.For.IndexVar != "" {
						loopCtx.vars[vn.For.IndexVar] = i
					}
					// Clone the node itself (not just children), remove the For clause
					clone := ast.CloneVisualNodes([]*ast.VisualNode{vn})[0]
					clone.For = nil
					clone.Children = optimizeNodes(clone.Children, loopCtx)
					// Fold props/class/key on the cloned node
					foldExprMap(clone.Props, loopCtx)
					foldExprPtr(clone.Key, loopCtx)
					foldExprPtr(clone.Class, loopCtx)
					foldExprPtr(clone.Ref, loopCtx)
					out = append(out, clone)
				}
				continue
			}
			// Check AST: ListExpr with no elements
			if list, ok := vn.For.Iterable.SNGL.(*ast.ListExpr); ok && len(list.Elements) == 0 {
				continue // for over empty list — dead code
			}
		}

		// Fold expressions in the node's properties.
		foldExprPtr(vn.Key, ctx)
		foldExprPtr(vn.Class, ctx)
		foldExprPtr(vn.Ref, ctx)
		if vn.For != nil {
			foldExpr(&vn.For.Iterable, ctx)
		}
		foldExprMap(vn.Props, ctx)
		for k, eh := range vn.Events {
			foldExpr(&eh.Body, ctx)
			vn.Events[k] = eh
		}

		// Unwrap trivial interpolations: "{expr}" → expr
		unwrapTrivialInterpolations(vn.Props)
		unwrapTrivialInterpolationPtr(vn.Key)
		unwrapTrivialInterpolationPtr(vn.Class)

		// Recurse into children.
		vn.Children = optimizeNodes(vn.Children, ctx)

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

func foldExprPtr(e *ast.Expr, ctx *foldCtx) {
	if e != nil {
		foldExpr(e, ctx)
	}
}
