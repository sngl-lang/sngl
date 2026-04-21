package testrunner

import (
	"fmt"
	"maps"
	"time"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/codegen"
)

// callMethod dispatches method calls on the Test value: t.assert(), t.tick(), t.test().
func (tv *testingT) callMethod(env *Env, method string, args []ast.Expr) (any, error) {
	switch method {
	case "assert":
		if len(args) != 1 {
			return nil, fmt.Errorf("t.assert() requires 1 argument")
		}
		v, err := env.Eval(args[0])
		if err != nil {
			return nil, err
		}
		b, ok := v.(bool)
		if !ok {
			return nil, fmt.Errorf("t.assert() requires bool argument, got %T (%v)", v, v)
		}
		if !b {
			return nil, &AssertError{Expr: args[0], Got: v}
		}
		return nil, nil

	case "tick":
		// Advance every enabled timer in the component body one iteration,
		// executing its @tick handler against the current env.
		var cv *componentValue
		for _, v := range env.vars {
			if c, ok := v.(*componentValue); ok {
				cv = c
				break
			}
		}
		if cv != nil {
			compEnv := cv.compEnv()
			if err := fireTimers(compEnv.bodyStmts, compEnv); err != nil {
				return nil, err
			}
			// Sync any mutations the timer body made back onto cv.vars so
			// subsequent c.<field> reads see them.
			for k := range cv.vars {
				if v, ok := compEnv.vars[k]; ok {
					cv.vars[k] = v
					if !cv.testParams[k] {
						cv.env.vars[k] = v
					}
				}
			}
		}
		return nil, nil

	case "test":
		if len(args) < 2 {
			return nil, fmt.Errorf("t.test() requires a description and a function")
		}
		desc, err := env.Eval(args[0])
		if err != nil {
			return nil, err
		}
		fn, err := env.Eval(args[1])
		if err != nil {
			return nil, err
		}
		lv, ok := fn.(*lambdaValue)
		if !ok {
			return nil, fmt.Errorf("t.test() second argument must be a function, got %T", fn)
		}

		start := time.Now()
		childResult := &codegen.TestResult{
			Desc: fmt.Sprintf("%v", desc),
		}

		// Snapshot the env for the subtest
		childEnv := env.Snapshot()
		childT := &testingT{env: childEnv, result: childResult, doc: tv.doc, compName: tv.compName}

		// Build args for the lambda: (t) or (t, c)
		callArgs := []any{childT}
		if tv.compName != "" {
			// Find the parent componentValue and snapshot its state
			var parentCV *componentValue
			for _, v := range env.vars {
				if cv, ok := v.(*componentValue); ok {
					parentCV = cv
					break
				}
			}
			childCV := &componentValue{
				env:    childEnv,
				vars:   make(map[string]any),
				consts: make(map[string]any),
				funcs:  make(map[string]*ast.FuncDef),
			}
			if parentCV != nil {
				maps.Copy(childCV.vars, parentCV.vars)
				maps.Copy(childCV.consts, parentCV.consts)
				maps.Copy(childCV.funcs, parentCV.funcs)
			}
			callArgs = append(callArgs, childCV)
		}

		_, callErr := lv.callWithEnv(childEnv, callArgs)
		if callErr != nil {
			childResult.Error = callErr.Error()
		}
		childResult.Log = childEnv.Log
		childResult.Passed = childResult.Error == "" && allPassed(childResult.Children)
		childResult.Duration = time.Since(start)
		tv.result.Children = append(tv.result.Children, childResult)
		return nil, nil

	default:
		return nil, fmt.Errorf("Test has no method %q", method)
	}
}

// getField resolves c.field on a componentValue.
func (cv *componentValue) getField(field string) (any, error) {
	// Check vars (data, params)
	if v, ok := cv.vars[field]; ok {
		return v, nil
	}
	// Check consts
	if v, ok := cv.consts[field]; ok {
		return v, nil
	}
	// Check computed fields (zero-param functions)
	if fn, ok := cv.funcs[field]; ok {
		if len(fn.Params.Params) == 0 {
			compEnv := cv.compEnv()
			return compEnv.evalUserFunc(fn, nil)
		}
	}
	// Visual node lookup: c.<#id> resolves to the node (or list of nodes for
	// nodes emitted inside a for-loop). Returns nil for conditionally-hidden
	// nodes — tests compare against null to assert absence.
	compEnv := cv.compEnv()
	if compEnv.hasElementRefID(compEnv.bodyStmts, field, map[string]bool{}) {
		if v, err := compEnv.resolveElementRef(field); err == nil {
			return v, nil
		}
	}
	return nil, fmt.Errorf("component has no field %q", field)
}

// hasElementRefID reports whether any VisualNode or CallStmt in the body tree
// declares the given #id, descending into user-component calls. Used to
// distinguish a conditionally-hidden node (returns nil) from a truly
// undefined field.
func (env *Env) hasElementRefID(stmts []ast.Stmt, id string, visiting map[string]bool) bool {
	for _, s := range stmts {
		switch n := s.(type) {
		case *ast.VisualNode:
			if n.ID == id {
				return true
			}
			// Descend into a user-defined component target.
			if env.doc != nil {
				if comp := findComponent(env.doc, codegen.VisualNodeName(n)); comp != nil && !visiting[comp.Name] {
					visiting[comp.Name] = true
					if env.hasElementRefID(comp.Body.Stmts, id, visiting) {
						return true
					}
					delete(visiting, comp.Name)
				}
			}
			if env.hasElementRefID(n.Block.Stmts, id, visiting) {
				return true
			}
		case *ast.IfStmt:
			if env.hasElementRefID(n.Body.Stmts, id, visiting) || env.hasElementRefID(n.Else.Stmts, id, visiting) {
				return true
			}
		case *ast.ForStmt:
			if env.hasElementRefID(n.Body.Stmts, id, visiting) || env.hasElementRefID(n.Else.Stmts, id, visiting) {
				return true
			}
		case *ast.CallStmt:
			if sel, ok := n.Call.Func.(*ast.SelectExpr); ok && sel.Kind == ast.SelectElemRef && sel.Field == id {
				return true
			}
			if ident, ok := n.Call.Func.(*ast.IdentExpr); ok && env.doc != nil {
				if comp := findComponent(env.doc, ident.Name); comp != nil && !visiting[comp.Name] {
					visiting[comp.Name] = true
					if env.hasElementRefID(comp.Body.Stmts, id, visiting) {
						return true
					}
					delete(visiting, comp.Name)
				}
			}
		case *ast.PlatformStmt:
			if env.hasElementRefID(n.Body.Stmts, id, visiting) {
				return true
			}
		}
	}
	return false
}

// setField handles c.field = value assignment.
func (cv *componentValue) setField(op ast.AssignOp, field string, val any) error {
	cur, exists := cv.vars[field]
	if !exists {
		return fmt.Errorf("cannot assign to undefined component field %q", field)
	}
	cv.vars[field] = applyOp(op, cur, val)
	// Sync to env unless it would overwrite a test param
	if !cv.testParams[field] {
		cv.env.vars[field] = cv.vars[field]
	}
	return nil
}

// compEnv creates a temporary Env with the component's current state
// for evaluating computed fields.
func (cv *componentValue) compEnv() *Env {
	env := cv.env.Snapshot()
	maps.Copy(env.vars, cv.vars)
	return env
}

// syncFromEnv reads component state from the shared env back into cv.vars.
func (cv *componentValue) syncFromEnv(testParams map[string]bool) {
	for k := range cv.vars {
		if !testParams[k] {
			if v, ok := cv.env.vars[k]; ok {
				cv.vars[k] = v
			}
		}
	}
}

// currentCompVar returns the test's component param name by scanning env.vars
// for the first componentValue entry. Returns "" if no component is bound.
func (tv *testingT) currentCompVar() string {
	for k, v := range tv.env.vars {
		if _, ok := v.(*componentValue); ok {
			return k
		}
	}
	return ""
}

// fireTimers walks body statements, executing @tick handlers for each timer
// whose enabled/condition arg evaluates truthy (absent → treated as enabled).
func fireTimers(stmts []ast.Stmt, env *Env) error {
	for _, s := range stmts {
		switch n := s.(type) {
		case *ast.VisualNode:
			if codegen.VisualNodeName(n) == "timer" {
				if err := fireTimer(n, env); err != nil {
					return err
				}
			}
			if err := fireTimers(n.Block.Stmts, env); err != nil {
				return err
			}
		case *ast.CallStmt:
			if ident, ok := n.Call.Func.(*ast.IdentExpr); ok && ident.Name == "timer" {
				if err := fireTimerFromCall(n.Call, env); err != nil {
					return err
				}
			}
		case *ast.IfStmt:
			if err := fireTimers(n.Body.Stmts, env); err != nil {
				return err
			}
			if err := fireTimers(n.Else.Stmts, env); err != nil {
				return err
			}
		case *ast.ForStmt:
			if err := fireTimers(n.Body.Stmts, env); err != nil {
				return err
			}
		case *ast.PlatformStmt:
			if err := fireTimers(n.Body.Stmts, env); err != nil {
				return err
			}
		}
	}
	return nil
}

func fireTimer(vn *ast.VisualNode, env *Env) error {
	var handler *ast.EventHandler
	enabled := true
	for _, a := range vn.Args.Args {
		switch aa := a.(type) {
		case ast.Arg:
			if aa.Name == "enabled" && aa.Value != nil {
				v, err := env.Eval(aa.Value)
				if err != nil {
					return err
				}
				b, _ := v.(bool)
				enabled = b
			}
		case ast.EventHandler:
			if aa.Name == "tick" {
				h := aa
				handler = &h
			}
		}
	}
	if !enabled || handler == nil {
		return nil
	}
	return env.ExecBlock(&handler.Body)
}

func fireTimerFromCall(call *ast.CallExpr, env *Env) error {
	var handler *ast.EventHandler
	enabled := true
	for _, a := range call.Args.Args {
		switch aa := a.(type) {
		case ast.Arg:
			if aa.Name == "enabled" && aa.Value != nil {
				v, err := env.Eval(aa.Value)
				if err != nil {
					return err
				}
				b, _ := v.(bool)
				enabled = b
			}
		case ast.EventHandler:
			if aa.Name == "tick" {
				h := aa
				handler = &h
			}
		}
	}
	if !enabled || handler == nil {
		return nil
	}
	return env.ExecBlock(&handler.Body)
}

// callMethod dispatches method calls on componentValue (for c.@event() and c.func() calls).
func (cv *componentValue) callMethod(_ *Env, method string, args []ast.Expr) (any, error) {
	if len(method) > 0 && method[0] == '@' {
		// Event emission — no-op in headless mode
		return nil, nil
	}
	// Try as a user-defined function on the component
	if fn, ok := cv.funcs[method]; ok {
		compEnv := cv.compEnv()
		result, err := compEnv.evalUserFunc(fn, args)
		// Sync any mutations back
		for k := range cv.vars {
			if v, ok := compEnv.vars[k]; ok {
				cv.vars[k] = v
				if !cv.testParams[k] {
					cv.env.vars[k] = v
				}
			}
		}
		return result, err
	}
	return nil, fmt.Errorf("component has no method %q", method)
}

// callWithEnv invokes the lambda but uses the provided env instead of snapshotting.
func (lv *lambdaValue) callWithEnv(env *Env, args []any) (any, error) {
	for i, p := range lv.params {
		if i < len(args) {
			env.vars[p] = args[i]
		}
	}
	if lv.block.IsDefined() {
		for _, stmt := range lv.block.Stmts {
			if ret, ok := stmt.(*ast.ReturnStmt); ok {
				if ret.Value != nil {
					return env.Eval(ret.Value)
				}
				return nil, nil
			}
			if err := env.Exec(stmt); err != nil {
				return nil, err
			}
		}
		return nil, nil
	}
	if lv.body != nil {
		return env.Eval(lv.body)
	}
	return nil, nil
}
