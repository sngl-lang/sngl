package testrunner

import (
	"fmt"
	"maps"
	"time"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/codegen"
)

// callMethod dispatches method calls on the T value: t.assert(), t.tick(), t.test().
func (tv *testingT) callMethod(env *Env, method string, args []ast.Node) (any, error) {
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
		for _, t := range env.timers {
			active, ok := env.vars[t.Active]
			if !ok {
				continue
			}
			if b, ok := active.(bool); ok && b {
				if err := env.Exec(t.Body); err != nil {
					return nil, err
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
		return nil, fmt.Errorf("T has no method %q", method)
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
		if len(fn.Params) == 0 {
			// Evaluate the computed field using an env that has our vars
			compEnv := cv.compEnv()
			return compEnv.evalUserFunc(fn, nil)
		}
	}
	// Check #id element refs
	if len(field) > 0 && field[0] == '#' {
		return cv.env.resolveElementRef(field[1:])
	}
	return nil, fmt.Errorf("component has no field %q", field)
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
// This is needed after event handlers modify env.vars directly.
func (cv *componentValue) syncFromEnv(testParams map[string]bool) {
	for k := range cv.vars {
		if !testParams[k] {
			if v, ok := cv.env.vars[k]; ok {
				cv.vars[k] = v
			}
		}
	}
}

// callMethod dispatches method calls on componentValue (for c.@event() and c.func() calls).
func (cv *componentValue) callMethod(_ *Env, method string, args []ast.Node) (any, error) {
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
	if lv.block != nil {
		for _, stmt := range lv.block.Stmts {
			if err := env.Exec(stmt); err != nil {
				return nil, err
			}
		}
		if lv.block.Return != nil {
			return env.Eval(lv.block.Return)
		}
		return nil, nil
	}
	return env.Eval(lv.body)
}
