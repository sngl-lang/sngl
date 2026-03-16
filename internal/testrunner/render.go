package testrunner

import (
	"git.duckfam.us/jonathan/sngl/ast"
)

// renderTree builds the virtual root node for the current component state.
func (env *Env) renderTree() map[string]any {
	root := map[string]any{
		"_type":     "root",
		"_children": env.renderChildren(env.body),
	}
	// Expose vars and computeds as top-level fields on root.
	for k, v := range env.vars {
		root[k] = v
	}
	for k, expr := range env.computeds {
		if v, err := env.Eval(expr.SNGL); err == nil {
			root[k] = v
		}
	}
	return root
}

// renderChildren renders a slice of visual nodes, handling if/for.
func (env *Env) renderChildren(nodes []*ast.VisualNode) []any {
	var out []any
	for _, n := range nodes {
		rendered := env.renderNode(n)
		if rendered != nil {
			if items, ok := rendered.([]any); ok {
				out = append(out, items...)
			} else {
				out = append(out, rendered)
			}
		}
	}
	return out
}

// renderNode renders a single visual node. Returns nil if the node should be
// skipped (e.g. if= is falsy). Returns []any for for= expansions.
func (env *Env) renderNode(node *ast.VisualNode) any {
	// Handle if= conditional
	if node.If != nil && node.If.SNGL != nil {
		v, err := env.Eval(node.If.SNGL)
		if err != nil || !truthy(v) {
			return nil
		}
	}

	// Handle for= loop
	if node.For != nil {
		return env.renderFor(node)
	}

	// Check if this is a user-defined component — expand inline
	if env.doc != nil {
		if comp := findComponent(env.doc, node.Component); comp != nil {
			return env.renderComponent(comp, node)
		}
	}

	// Primitive element
	m := map[string]any{
		"_type":     node.Component,
		"_children": env.renderChildren(node.Children),
	}

	// Evaluate props
	for k, expr := range node.Props {
		if expr.SNGL != nil {
			if v, err := env.Eval(expr.SNGL); err == nil {
				m[k] = v
			}
		} else if expr.Literal != nil {
			m[k] = expr.Literal
		}
	}

	// Expose special attributes
	if node.Key != nil {
		if node.Key.SNGL != nil {
			if v, err := env.Eval(node.Key.SNGL); err == nil {
				m["key"] = v
			}
		} else if node.Key.Literal != nil {
			m["key"] = node.Key.Literal
		}
	}
	if node.ID != nil {
		if node.ID.SNGL != nil {
			if v, err := env.Eval(node.ID.SNGL); err == nil {
				m["id"] = v
			}
		} else if node.ID.Literal != nil {
			m["id"] = node.ID.Literal
		}
	}
	if node.Class != nil {
		if node.Class.SNGL != nil {
			if v, err := env.Eval(node.Class.SNGL); err == nil {
				m["class"] = v
			}
		} else if node.Class.Literal != nil {
			m["class"] = node.Class.Literal
		}
	}

	// Expose event handlers for test triggering
	for name, expr := range node.Events {
		if expr.SNGL != nil {
			m["@"+name] = expr.SNGL
		}
	}

	return m
}

// renderFor expands a for= loop, rendering the node once per iteration.
func (env *Env) renderFor(node *ast.VisualNode) []any {
	iterVal, err := env.Eval(node.For.Iterable.SNGL)
	if err != nil {
		return nil
	}
	list, ok := iterVal.([]any)
	if !ok {
		return nil
	}

	var out []any
	for i, item := range list {
		// Create a child env with loop variable bound
		child := env.Snapshot()
		child.vars[node.For.Variable] = item
		if node.For.IndexVar != "" {
			child.vars[node.For.IndexVar] = i
		}

		// Render the node without the for clause
		noFor := *node
		noFor.For = nil
		if rendered := child.renderNode(&noFor); rendered != nil {
			out = append(out, rendered)
		}
	}
	return out
}

// renderComponent expands a user-defined component inline.
func (env *Env) renderComponent(comp *ast.Component, node *ast.VisualNode) any {
	childEnv := NewEnv()
	childEnv.doc = env.doc

	// Initialize params from defaults, then override with passed props
	for _, p := range comp.Params {
		childEnv.vars[p.Name] = evalInit(childEnv, p.Default)
	}
	for k, expr := range node.Props {
		if expr.SNGL != nil {
			if v, err := env.Eval(expr.SNGL); err == nil {
				childEnv.vars[k] = v
			}
		} else if expr.Literal != nil {
			childEnv.vars[k] = expr.Literal
		}
	}

	// Initialize data and computeds
	for _, d := range comp.Data {
		childEnv.vars[d.Name] = evalInit(childEnv, d.Init)
	}
	for _, c := range comp.Computeds {
		childEnv.computeds[c.Name] = c.Expr
	}
	for _, c := range comp.Consts {
		childEnv.consts[c.Name] = evalInit(childEnv, c.Init)
	}

	childEnv.body = comp.Body

	// Inline the component's children (component element itself does not appear)
	children := childEnv.renderChildren(comp.Body)
	if len(children) == 1 {
		return children[0]
	}
	return any(children)
}
