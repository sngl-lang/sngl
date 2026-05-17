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
		ae, err := env.runAssert(args[0])
		if err != nil {
			return nil, err
		}
		if ae != nil {
			tv.recordFailure(ae, false)
		}
		return nil, nil

	case "must":
		if len(args) != 1 {
			return nil, fmt.Errorf("t.must() requires 1 argument")
		}
		ae, err := env.runAssert(args[0])
		if err != nil {
			return nil, err
		}
		if ae != nil {
			return nil, ae
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

	case "wait":
		if len(args) != 2 {
			return nil, fmt.Errorf("t.wait() requires 2 arguments")
		}
		predVal, err := env.Eval(args[0])
		if err != nil {
			return nil, err
		}
		lv, ok := predVal.(*lambdaValue)
		if !ok {
			return nil, fmt.Errorf("t.wait() first argument must be a function, got %T", predVal)
		}
		timeoutVal, err := env.Eval(args[1])
		if err != nil {
			return nil, err
		}
		timeoutMs := toInt(timeoutVal)
		deadline := time.Now().Add(time.Duration(timeoutMs) * time.Millisecond)

		var cv *componentValue
		for _, v := range env.vars {
			if c, ok := v.(*componentValue); ok {
				cv = c
				break
			}
		}

		for {
			// Predicate runs in its captured env so c reads observe in-place
			// component-var updates from fireTimers.
			out, callErr := lv.call(nil)
			if callErr != nil {
				return nil, callErr
			}
			if b, ok := out.(bool); ok && b {
				return nil, nil
			}
			if cv == nil {
				return nil, nil
			}
			if !time.Now().Before(deadline) {
				return nil, nil
			}
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

	case "setContext":
		if len(args) != 2 {
			return nil, fmt.Errorf("t.setContext() requires 2 arguments")
		}
		// arg[0] must be a *ir.ContextRead — a bare context name in expression
		// position. Extract the *ir.Context handle without evaluating it.
		cr, ok := args[0].(*ir.ContextRead)
		if !ok {
			return nil, fmt.Errorf("t.setContext() first argument must be a context name, got %T", args[0])
		}
		val, err := env.Eval(args[1])
		if err != nil {
			return nil, err
		}
		env.SetContext(cr.Ref, val)
		tv.locale = env.locale // sync locale field if "locale" context was set
		if tv.contextOverrides == nil {
			tv.contextOverrides = make(map[*ir.Context]any)
		}
		tv.contextOverrides[cr.Ref] = val
		// Re-evaluate any component vars whose init expression is a ContextRead
		// for this context, so subsequent c.<field> reads see the new value.
		if tv.compName != "" {
			comp := findComponent(tv.pkg, tv.compName)
			if comp != nil {
				for _, v := range comp.Vars {
					if cr2, ok2 := v.Init.(*ir.ContextRead); ok2 && cr2.Ref == cr.Ref {
						newVal, evalErr := env.Eval(v.Init)
						if evalErr == nil {
							env.vars[v.Name] = newVal
							// Also update any componentValue in scope.
							for _, sv := range env.vars {
								if cv, ok3 := sv.(*componentValue); ok3 {
									if _, exists := cv.vars[v.Name]; exists {
										cv.vars[v.Name] = newVal
									}
								}
							}
						}
					}
				}
			}
		}
		return nil, nil

	case "setLocale":
		if len(args) != 1 {
			return nil, fmt.Errorf("t.setLocale() requires 1 argument")
		}
		locVal, err := env.Eval(args[0])
		if err != nil {
			return nil, err
		}
		loc, ok := locVal.(string)
		if !ok {
			return nil, fmt.Errorf("t.setLocale() argument must be a string, got %T", locVal)
		}
		tv.locale = loc
		env.locale = loc
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
		childT := &testingT{env: childEnv, result: childResult, pkg: tv.pkg, compName: tv.compName, locale: tv.locale, contextOverrides: tv.contextOverrides}

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

// recordFailure appends a soft assertion failure to the enclosing test
// result. fatal=true is used by must() / runtime errors so the runner can
// stop execution after appending.
func (tv *testingT) recordFailure(ae *AssertError, fatal bool) {
	if tv == nil || tv.result == nil || ae == nil {
		return
	}
	line := 0
	if a := irASTOf(ae.Expr); a != nil {
		line = a.ExprPos().Line
	}
	tv.result.Failures = append(tv.result.Failures, codegen.TestFailure{
		Line:    line,
		Message: ae.Msg,
		Fatal:   fatal,
	})
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
	// Element ref lookup in the component body. Returns nil (not an error)
	// when the ref exists in the body tree but is currently hidden by an
	// if/for-else branch — tests assert against null for "not visible".
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
