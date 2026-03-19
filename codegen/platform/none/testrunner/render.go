package testrunner

import (
	"fmt"

	"git.duckfam.us/jonathan/sngl/ast"
)

// resolveElementRef finds visual nodes with the given #id, rendering the current
// component state. Returns a single element map, a list of maps (for-loop),
// or nil (not visible / if=false).
func (env *Env) resolveElementRef(id string) (any, error) {
	if env.body == nil {
		return nil, fmt.Errorf("no visual body for element ref #%s", id)
	}
	var matches []map[string]any
	env.collectByID(env.body, id, &matches)
	if len(matches) == 0 {
		return nil, nil // element hidden or not found
	}
	if len(matches) == 1 {
		return matches[0], nil
	}
	// Multiple matches (for-loop): return as []any
	out := make([]any, len(matches))
	for i, m := range matches {
		out[i] = m
	}
	return out, nil
}

// collectByID walks the visual tree, collecting rendered nodes with matching _id.
func (env *Env) collectByID(nodes []*ast.VisualNode, id string, out *[]map[string]any) {
	for _, n := range nodes {
		env.collectNodeByID(n, id, out)
	}
}

func (env *Env) collectNodeByID(node *ast.VisualNode, id string, out *[]map[string]any) {
	// Handle if= conditional
	if node.If != nil && node.If.SNGL != nil {
		v, err := env.Eval(node.If.SNGL)
		b, _ := v.(bool)
		if err != nil || !b {
			return
		}
	}

	// Handle for= loop
	if node.For != nil {
		env.collectForByID(node, id, out)
		return
	}

	// Check if this is a user-defined component — expand inline
	if env.doc != nil {
		if comp := findComponent(env.doc, node.Component); comp != nil {
			childEnv := env.componentEnv(comp, node)
			childEnv.collectByID(comp.Body, id, out)
			return
		}
	}

	// Check this node
	if node.ID == id {
		m := env.renderNodeProps(node)
		*out = append(*out, m)
	}

	// Recurse into children
	env.collectByID(node.Children, id, out)
}

func (env *Env) collectForByID(node *ast.VisualNode, id string, out *[]map[string]any) {
	iterVal, err := env.Eval(node.For.Iterable.SNGL)
	if err != nil {
		return
	}
	list, ok := iterVal.([]any)
	if !ok {
		return
	}
	for i, item := range list {
		child := env.Snapshot()
		child.vars[node.For.Variable] = item
		if node.For.IndexVar != "" {
			child.vars[node.For.IndexVar] = i
		}
		noFor := *node
		noFor.For = nil
		child.collectNodeByID(&noFor, id, out)
	}
}

// componentEnv creates a child env for expanding a user-defined component.
func (env *Env) componentEnv(comp *ast.Component, node *ast.VisualNode) *Env {
	childEnv := NewEnv()
	childEnv.doc = env.doc
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
	return childEnv
}

// renderNodeProps renders a visual node's props and events into a map for test access.
func (env *Env) renderNodeProps(node *ast.VisualNode) map[string]any {
	m := map[string]any{
		"_type": node.Component,
	}
	for k, expr := range node.Props {
		if expr.SNGL != nil {
			if v, err := env.Eval(expr.SNGL); err == nil {
				m[k] = v
			}
		} else if expr.Literal != nil {
			m[k] = expr.Literal
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
	for name, expr := range node.Events {
		if expr.SNGL != nil {
			m["@"+name] = expr.SNGL
		}
	}
	return m
}

