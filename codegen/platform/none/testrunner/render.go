package testrunner

import (
	"fmt"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/codegen"
)

// resolveElementRef finds visual nodes with the given #id, rendering the current
// component state. Returns a single element map, a list of maps (for-loop),
// or nil (not visible / if=false).
func (env *Env) resolveElementRef(id string) (any, error) {
	if env.bodyStmts == nil {
		return nil, fmt.Errorf("no visual body for element ref #%s", id)
	}
	var matches []map[string]any
	env.collectByStmts(env.bodyStmts, id, &matches)
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

// collectByStmts walks statements (body of a component or visual node) for element refs.
func (env *Env) collectByStmts(stmts []ast.Stmt, id string, out *[]map[string]any) {
	for _, s := range stmts {
		switch n := s.(type) {
		case *ast.VisualNode:
			env.collectNodeByID(n, id, out)
		case *ast.IfStmt:
			env.collectIfByID(n, id, out)
		case *ast.ForStmt:
			env.collectForByID(n, id, out)
		case *ast.CallStmt:
			// In v2, elements without children blocks are CallStmt.
			// Check if the Func is a SelectExpr with SelectElemRef kind.
			env.collectCallStmtByID(n.Call, id, out)
		}
	}
}

func (env *Env) collectNodeByID(node *ast.VisualNode, id string, out *[]map[string]any) {
	name := codegen.VisualNodeName(node)

	// Check if this is a user-defined component — expand inline
	if env.doc != nil {
		if comp := findComponent(env.doc, name); comp != nil {
			if env.renderDepth >= maxCallDepth {
				return // prevent infinite component recursion
			}
			childEnv := env.componentEnv(comp, node)
			childEnv.renderDepth = env.renderDepth + 1
			childEnv.collectByStmts(childEnv.bodyStmts, id, out)
			return
		}
	}

	// Check this node
	if node.ID == id {
		m := env.renderNodeProps(node)
		*out = append(*out, m)
	}

	// Recurse into children
	env.collectByStmts(codegen.VnChildren(node), id, out)
}

func (env *Env) collectIfByID(ifStmt *ast.IfStmt, id string, out *[]map[string]any) {
	v, err := env.Eval(ifStmt.Cond)
	b, _ := v.(bool)
	if err != nil || !b {
		// Render else block if present
		if ifStmt.Else.IsDefined() {
			env.collectByStmts(ifStmt.Else.Stmts, id, out)
		}
		return
	}
	env.collectByStmts(ifStmt.Body.Stmts, id, out)
}

func (env *Env) collectForByID(forStmt *ast.ForStmt, id string, out *[]map[string]any) {
	iterVal, err := env.Eval(forStmt.Iter)
	if err != nil {
		return
	}
	list, ok := iterVal.([]any)
	if !ok {
		list = nil
	}
	if len(list) == 0 {
		// Render else block if present
		if forStmt.Else.IsDefined() {
			env.collectByStmts(forStmt.Else.Stmts, id, out)
		}
		return
	}
	for i, item := range list {
		child := env.Snapshot()
		child.vars[forStmt.Key] = item
		if forStmt.Value != "" {
			child.vars[forStmt.Value] = i
		}
		child.collectByStmts(forStmt.Body.Stmts, id, out)
	}
}

// componentEnvFromCall creates a child env for expanding a user-defined component from a CallExpr.
func (env *Env) componentEnvFromCall(comp *ast.ComponentDecl, call *ast.CallExpr) *Env {
	childEnv := NewEnv()
	childEnv.doc = env.doc
	childEnv.units = env.units

	// Set default param values
	for _, p := range codegen.CompParams(comp) {
		childEnv.vars[p.Name] = evalInit(childEnv, p.Default)
	}

	// Override with actual prop values from the call args
	args := codegen.CallArgs(call)
	params := codegen.CompParams(comp)
	// Match positional args to params, or named args
	for _, a := range call.Args.Args {
		if arg, ok := a.(ast.Arg); ok && arg.Name != "" && arg.Value != nil {
			if v, err := env.Eval(arg.Value); err == nil {
				childEnv.vars[arg.Name] = v
			}
		}
	}
	// Positional args (unnamed)
	paramIdx := 0
	for _, a := range call.Args.Args {
		if arg, ok := a.(ast.Arg); ok && arg.Name == "" && arg.Value != nil {
			if paramIdx < len(params) {
				if v, err := env.Eval(arg.Value); err == nil {
					childEnv.vars[params[paramIdx].Name] = v
				}
			}
			paramIdx++
		}
	}
	_ = args

	// Load component body declarations
	varDecls, constDecls, funcs := compDeclsFromBody(comp)
	for _, vd := range varDecls {
		for _, spec := range vd.Specs {
			val := evalInit(childEnv, spec.Default)
			for _, name := range spec.Names {
				childEnv.vars[name] = val
			}
		}
	}
	for _, fn := range funcs {
		childEnv.funcs[fn.Name] = fn
	}
	for _, cd := range constDecls {
		for _, spec := range cd.Specs {
			val := evalInit(childEnv, spec.Default)
			for _, name := range spec.Names {
				childEnv.consts[name] = val
			}
		}
	}

	childEnv.bodyStmts = comp.Body.Stmts
	return childEnv
}

// componentEnv creates a child env for expanding a user-defined component from a VisualNode.
func (env *Env) componentEnv(comp *ast.ComponentDecl, node *ast.VisualNode) *Env {
	childEnv := NewEnv()
	childEnv.doc = env.doc
	childEnv.units = env.units

	// Set default param values
	for _, p := range codegen.CompParams(comp) {
		childEnv.vars[p.Name] = evalInit(childEnv, p.Default)
	}

	// Override with actual prop values from the visual node
	props := codegen.VnProps(node)
	for k, expr := range props {
		v, err := env.Eval(expr)
		if err == nil {
			childEnv.vars[k] = v
		}
	}

	// Load component body declarations
	varDecls, constDecls, funcs := compDeclsFromBody(comp)
	for _, vd := range varDecls {
		for _, spec := range vd.Specs {
			val := evalInit(childEnv, spec.Default)
			for _, name := range spec.Names {
				childEnv.vars[name] = val
			}
		}
	}
	for _, fn := range funcs {
		childEnv.funcs[fn.Name] = fn
	}
	for _, cd := range constDecls {
		for _, spec := range cd.Specs {
			val := evalInit(childEnv, spec.Default)
			for _, name := range spec.Names {
				childEnv.consts[name] = val
			}
		}
	}

	childEnv.bodyStmts = comp.Body.Stmts
	return childEnv
}

// collectCallStmtByID checks a CallStmt (element without children) for matching #id.
func (env *Env) collectCallStmtByID(call *ast.CallExpr, id string, out *[]map[string]any) {
	var elemName string
	var elemID string

	switch f := call.Func.(type) {
	case *ast.SelectExpr:
		if f.Kind == ast.SelectElemRef {
			elemID = f.Field
			if ident, ok := f.Operand.(*ast.IdentExpr); ok {
				elemName = ident.Name
			}
		}
	case *ast.IdentExpr:
		elemName = f.Name
	}

	// Check if this is a user-defined component — expand inline
	if elemName != "" && env.doc != nil {
		if comp := findComponent(env.doc, elemName); comp != nil {
			if env.renderDepth >= maxCallDepth {
				return
			}
			childEnv := env.componentEnvFromCall(comp, call)
			childEnv.renderDepth = env.renderDepth + 1
			childEnv.collectByStmts(childEnv.bodyStmts, id, out)
			return
		}
	}

	if elemID == id {
		m := env.renderCallProps(call, elemName)
		*out = append(*out, m)
	}
}

// renderCallProps renders a CallStmt-based element's props and events into a map.
func (env *Env) renderCallProps(call *ast.CallExpr, elemName string) map[string]any {
	m := map[string]any{
		"_type": elemName,
	}
	for _, a := range call.Args.Args {
		switch aa := a.(type) {
		case ast.Arg:
			if aa.Name != "" && aa.Value != nil {
				if v, err := env.Eval(aa.Value); err == nil {
					m[aa.Name] = v
				}
			}
		case ast.EventHandler:
			if aa.Body.IsDefined() {
				body := aa.Body
				m["@"+aa.Name] = &body
			}
		}
	}
	return m
}

// renderNodeProps renders a visual node's props and events into a map for test access.
func (env *Env) renderNodeProps(node *ast.VisualNode) map[string]any {
	m := map[string]any{
		"_type": codegen.VisualNodeName(node),
	}

	props := codegen.VnProps(node)
	for k, expr := range props {
		if v, err := env.Eval(expr); err == nil {
			m[k] = v
		}
	}

	events := codegen.VnEvents(node)
	for name, handler := range events {
		// Store the event handler body so it can be invoked via @event() calls
		if handler.Body.IsDefined() {
			m["@"+name] = &handler.Body
		}
	}

	return m
}
