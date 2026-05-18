package testrunner

import (
	"fmt"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/ir"
)

// resolveElementRef finds visual nodes with the given #id in the current body.
// Returns a single element map, a list of maps (for-loops), or nil.
func (env *Env) resolveElementRef(id string) (any, error) {
	if env.bodyStmts == nil {
		return nil, fmt.Errorf("no visual body for element ref #%s", id)
	}
	var matches []map[string]any
	env.collectByStmts(env.bodyStmts, id, &matches)
	if len(matches) == 0 {
		return nil, nil
	}
	if len(matches) == 1 {
		return matches[0], nil
	}
	out := make([]any, len(matches))
	for i, m := range matches {
		out[i] = m
	}
	return out, nil
}

// collectByStmts walks IR statements collecting rendered nodes with matching id.
func (env *Env) collectByStmts(stmts []ir.Stmt, id string, out *[]map[string]any) {
	for _, s := range stmts {
		switch n := s.(type) {
		case *ir.NodeInst:
			env.collectNodeByID(n, id, out)
		case *ir.CallStmt:
			env.collectCallStmtByID(n, id, out)
		case *ir.If:
			cond, err := env.Eval(n.Cond)
			b, _ := cond.(bool)
			if err != nil || !b {
				env.collectByStmts(n.Else, id, out)
				continue
			}
			env.collectByStmts(n.Body, id, out)
		case *ir.For:
			iterVal, err := env.Eval(n.Iter)
			if err != nil {
				continue
			}
			list, ok := iterVal.([]any)
			if !ok || len(list) == 0 {
				env.collectByStmts(n.Else, id, out)
				continue
			}
			for i, item := range list {
				child := env.Snapshot()
				child.vars[n.Key] = item
				if n.Value != "" {
					child.vars[n.Value] = i
				}
				child.collectByStmts(n.Body, id, out)
			}
		case *ir.PlatformFilter:
			if n.Platform == "" || n.Platform == "none" {
				env.collectByStmts(n.Body, id, out)
			}
		case *ir.SlotInst:
			env.collectByStmts(n.Children, id, out)
		case *ir.ErrorBoundary:
			env.collectByStmts(n.Children, id, out)
		case *ir.Window:
			env.collectByStmts(n.Body, id, out)
		case *ir.ContextProvider:
			env.collectByStmts(n.Children, id, out)
		case *ir.Assign, *ir.LocalVar, *ir.Return, *ir.Emit, *ir.Toggle:
			// Imperative stmts contain no rendered nodes.
		default:
			panic(fmt.Sprintf("testrunner.collectByStmts: unhandled ir.Stmt %T", n))
		}
	}
}

func (env *Env) collectNodeByID(node *ir.NodeInst, id string, out *[]map[string]any) {
	// User-defined component with a real body — expand inline.
	if node.Component != nil && len(node.Component.Body) > 0 {
		if env.renderDepth >= maxCallDepth {
			return
		}
		childEnv := env.componentEnv(node.Component, node)
		childEnv.renderDepth = env.renderDepth + 1
		childEnv.collectByStmts(childEnv.bodyStmts, id, out)
		return
	}
	if node.ID == id {
		m := env.renderNodeProps(node)
		*out = append(*out, m)
	}
	env.collectByStmts(node.Children, id, out)
}

func (env *Env) componentEnv(comp *ir.Component, inst *ir.NodeInst) *Env {
	child := NewEnv()
	child.pkg = env.pkg
	child.units = env.units
	child.comp = comp

	for _, p := range comp.Props {
		child.vars[p.Name] = evalInit(child, p.Default)
	}
	// Override with instance prop values.
	for _, arg := range inst.Props {
		if arg.Name == "" {
			continue
		}
		v, err := env.Eval(arg.Value)
		if err == nil {
			child.vars[arg.Name] = v
		}
	}
	for _, v := range comp.Vars {
		if v.IsConst {
			child.consts[v.Name] = evalInit(child, v.Init)
		} else {
			child.vars[v.Name] = evalInit(child, v.Init)
		}
	}
	for _, fn := range comp.Funcs {
		child.SetFunc(fn)
	}
	child.bodyStmts = comp.Body
	return child
}

// collectCallStmtByID handles children-less element calls (`text #id(...)`)
// which the checker emits as CallStmt rather than NodeInst. The element name
// and #id live on the AST back-reference.
func (env *Env) collectCallStmtByID(cs *ir.CallStmt, id string, out *[]map[string]any) {
	elemName, elemID := elemCallInfo(cs)
	if elemName == "" {
		return
	}
	// User-defined component — expand inline.
	if env.pkg != nil {
		if comp := findComponent(env.pkg, elemName); comp != nil {
			if env.renderDepth >= maxCallDepth {
				return
			}
			child := env.componentEnvFromCall(comp, cs.Call)
			child.renderDepth = env.renderDepth + 1
			child.collectByStmts(child.bodyStmts, id, out)
			return
		}
	}
	if elemID == id {
		m := env.renderCallStmtProps(cs, elemName)
		*out = append(*out, m)
	}
}

// elemCallInfo extracts (name, id) from an element CallStmt's AST back-ref.
func elemCallInfo(cs *ir.CallStmt) (string, string) {
	if cs == nil || cs.Call == nil {
		return "", ""
	}
	name := ""
	id := ""
	if call := cs.Call.AST; call != nil {
		switch f := call.Func.(type) {
		case *ast.SelectExpr:
			if f.Kind == ast.SelectElemRef {
				id = f.Field
				if ident, ok := f.Operand.(*ast.IdentExpr); ok {
					name = ident.Name
				}
			} else {
				if ident, ok := f.Operand.(*ast.IdentExpr); ok {
					name = ident.Name
				}
			}
		case *ast.IdentExpr:
			name = f.Name
		}
	}
	return name, id
}

func (env *Env) renderCallStmtProps(cs *ir.CallStmt, elemName string) map[string]any {
	m := map[string]any{"_type": elemName}
	if cs.Call == nil {
		return m
	}
	for _, arg := range cs.Call.Args {
		if arg.Name == "" {
			continue
		}
		if v, err := env.Eval(arg.Value); err == nil {
			m[arg.Name] = v
		}
	}
	// Event handlers aren't captured in ir.CallStmt.Args — reach into the
	// AST back-ref to collect their bodies for later invocation via @event().
	if cstmt, ok := cs.AST.(*ast.CallStmt); ok {
		for _, a := range cstmt.Call.Args.Args {
			if h, ok := a.(ast.EventHandler); ok {
				m["@"+h.Name] = &h.Body
			}
		}
	}
	return m
}

// componentEnvFromCall creates a child env for expanding a user component
// invoked as a CallStmt (no children block).
func (env *Env) componentEnvFromCall(comp *ir.Component, call *ir.Call) *Env {
	child := NewEnv()
	child.pkg = env.pkg
	child.units = env.units
	child.comp = comp

	for _, p := range comp.Props {
		child.vars[p.Name] = evalInit(child, p.Default)
	}
	// Override with named positional args from the call.
	if call != nil {
		for _, a := range call.Args {
			if a.Name == "" {
				continue
			}
			if v, err := env.Eval(a.Value); err == nil {
				child.vars[a.Name] = v
			}
		}
	}
	for _, v := range comp.Vars {
		if v.IsConst {
			child.consts[v.Name] = evalInit(child, v.Init)
		} else {
			child.vars[v.Name] = evalInit(child, v.Init)
		}
	}
	for _, fn := range comp.Funcs {
		child.SetFunc(fn)
	}
	child.bodyStmts = comp.Body
	return child
}

func (env *Env) renderNodeProps(node *ir.NodeInst) map[string]any {
	m := map[string]any{"_type": node.Name}
	for _, p := range node.Props {
		if p.Name == "" {
			continue
		}
		if v, err := env.Eval(p.Value); err == nil {
			m[p.Name] = v
		}
	}
	for _, h := range node.Handlers {
		m["@"+h.Name] = h.Func
	}
	return m
}
