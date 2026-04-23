package testrunner

import (
	"fmt"
	"maps"
	"time"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/ir"
)

// callMethod dispatches method calls on the Test value.
func (tv *testingT) callMethod(env *Env, method string, args []ir.Expr) (any, error) {
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
		var cv *componentValue
		for _, v := range env.vars {
			if c, ok := v.(*componentValue); ok {
				cv = c
				break
			}
		}
		if cv != nil {
			compEnv := cv.compEnv()
			if err := fireTimers(tv.pkg, compEnv); err != nil {
				return nil, err
			}
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
		childResult := &codegen.TestResult{Desc: fmt.Sprintf("%v", desc)}
		childEnv := env.Snapshot()
		childT := &testingT{env: childEnv, result: childResult, pkg: tv.pkg, compName: tv.compName}

		callArgs := []any{childT}
		if tv.compName != "" {
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
				funcs:  make(map[string]*ir.Func),
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
	}
	return nil, fmt.Errorf("Test has no method %q", method)
}

// getField resolves c.field on a componentValue.
func (cv *componentValue) getField(field string) (any, error) {
	if v, ok := cv.vars[field]; ok {
		return v, nil
	}
	if v, ok := cv.consts[field]; ok {
		return v, nil
	}
	if fn, ok := cv.funcs[field]; ok {
		if len(fn.Params) == 0 {
			compEnv := cv.compEnv()
			return compEnv.evalUserFunc(fn, nil)
		}
	}
	// Element ref lookup in the component body.
	compEnv := cv.compEnv()
	if v, err := compEnv.resolveElementRef(field); err == nil {
		return v, nil
	}
	return nil, fmt.Errorf("component has no field %q", field)
}

// setField handles c.field = value.
func (cv *componentValue) setField(op ast.AssignOp, field string, val any) error {
	cur, exists := cv.vars[field]
	if !exists {
		return fmt.Errorf("cannot assign to undefined component field %q", field)
	}
	cv.vars[field] = applyOp(op, cur, val)
	if !cv.testParams[field] {
		cv.env.vars[field] = cv.vars[field]
	}
	return nil
}

func (cv *componentValue) compEnv() *Env {
	env := cv.env.Snapshot()
	maps.Copy(env.vars, cv.vars)
	return env
}

func (cv *componentValue) syncFromEnv(testParams map[string]bool) {
	for k := range cv.vars {
		if !testParams[k] {
			if v, ok := cv.env.vars[k]; ok {
				cv.vars[k] = v
			}
		}
	}
}

// fireTimers runs each component timer's handler once, honoring the Enabled
// expression. In IR, timers live on ir.Component (not scattered through the
// body), so we walk pkg.Components for the active component.
func fireTimers(pkg *ir.Package, env *Env) error {
	if env.comp == nil {
		return nil
	}
	for _, t := range env.comp.Timers {
		enabled := true
		if t.Enabled != nil {
			v, err := env.Eval(t.Enabled)
			if err != nil {
				return err
			}
			if b, ok := v.(bool); ok {
				enabled = b
			}
		}
		if !enabled || t.Handler == nil {
			continue
		}
		for _, st := range t.Handler.Block {
			if err := env.Exec(st); err != nil {
				return err
			}
		}
	}
	return nil
}
