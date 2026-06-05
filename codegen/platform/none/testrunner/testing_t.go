package testrunner

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"time"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/codegen/testharness/snapshot"
	"git.duckfam.us/jonathan/sngl/internal/interp"
	"git.duckfam.us/jonathan/sngl/ir"
)

// CallMethod dispatches method calls on the Test value.
func (tv *testingT) CallMethod(env *interp.Env, method string, args []ir.Expr) (any, error) {
	switch method {
	case "assert":
		if len(args) != 1 {
			return nil, fmt.Errorf("t.assert() requires 1 argument")
		}
		ae, err := env.RunAssert(args[0])
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
		ae, err := env.RunAssert(args[0])
		if err != nil {
			return nil, err
		}
		if ae != nil {
			return nil, ae
		}
		return nil, nil

	case "tick":
		var cv *componentValue
		for _, v := range env.Vars {
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
			for k := range cv.Vars {
				if v, ok := compEnv.Vars[k]; ok {
					cv.Vars[k] = v
					if !cv.testParams[k] {
						cv.Env.Vars[k] = v
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
		lv, ok := predVal.(*interp.LambdaValue)
		if !ok {
			return nil, fmt.Errorf("t.wait() first argument must be a function, got %T", predVal)
		}
		timeoutVal, err := env.Eval(args[1])
		if err != nil {
			return nil, err
		}
		timeoutMs := interp.ToInt(timeoutVal)
		deadline := time.Now().Add(time.Duration(timeoutMs) * time.Millisecond)

		var cv *componentValue
		for _, v := range env.Vars {
			if c, ok := v.(*componentValue); ok {
				cv = c
				break
			}
		}

		for {
			// Predicate runs in its captured env so c reads observe in-place
			// component-var updates from fireTimers.
			out, callErr := lv.Call(nil)
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
			for k := range cv.Vars {
				if v, ok := compEnv.Vars[k]; ok {
					cv.Vars[k] = v
					if !cv.testParams[k] {
						cv.Env.Vars[k] = v
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
		tv.locale = env.Locale // sync locale field if "locale" context was set
		if tv.contextOverrides == nil {
			tv.contextOverrides = make(map[*ir.Context]any)
		}
		tv.contextOverrides[cr.Ref] = val
		// Re-evaluate any component vars whose init expression is a ContextRead
		// for this context, so subsequent c.<field> reads see the new value.
		if tv.compName != "" {
			comp := interp.FindComponent(tv.pkg, tv.compName)
			if comp != nil {
				for _, v := range comp.Vars {
					if cr2, ok2 := v.Init.(*ir.ContextRead); ok2 && cr2.Ref == cr.Ref {
						newVal, evalErr := env.Eval(v.Init)
						if evalErr == nil {
							env.Vars[v.Name] = newVal
							// Also update any componentValue in scope.
							for _, sv := range env.Vars {
								if cv, ok3 := sv.(*componentValue); ok3 {
									if _, exists := cv.Vars[v.Name]; exists {
										cv.Vars[v.Name] = newVal
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
		env.Locale = loc
		return nil, nil

	case "snapshot":
		if len(args) != 1 {
			return nil, fmt.Errorf("t.snapshot() requires 1 argument")
		}
		nameVal, err := env.Eval(args[0])
		if err != nil {
			return nil, err
		}
		name, ok := nameVal.(string)
		if !ok {
			return nil, fmt.Errorf("t.snapshot() argument must be a string, got %T", nameVal)
		}
		// Locate the component instance under test.
		var cv *componentValue
		for _, v := range env.Vars {
			if c, ok := v.(*componentValue); ok {
				cv = c
				break
			}
		}
		if cv == nil {
			return nil, fmt.Errorf("t.snapshot() requires a component parameter")
		}
		src, err := renderSnapshot(cv)
		if err != nil {
			return nil, err
		}
		if tv.pkg == nil || tv.pkg.SourcePath == "" {
			return nil, fmt.Errorf("t.snapshot(): no source fixture path available for golden resolution")
		}
		store := &snapshot.Store{
			Dir:    filepath.Dir(tv.pkg.SourcePath),
			Update: os.Getenv("SNGL_UPDATE_SNAPSHOTS") == "1",
		}
		fixtureBase := filepath.Base(tv.pkg.SourcePath)
		res, err := store.Assert(fixtureBase, name, "text/sngl", []byte(src))
		if err != nil {
			return nil, fmt.Errorf("t.snapshot(): %w", err)
		}
		if !res.Pass {
			msg := fmt.Sprintf("snapshot %q mismatch", name)
			if res.Diff != "" {
				msg += "\n" + res.Diff
			}
			line := 0
			if cs, ok2 := args[0].(interface{ ExprPos() ast.Pos }); ok2 {
				line = cs.ExprPos().Line
			}
			tv.result.Failures = append(tv.result.Failures, codegen.TestFailure{
				Line:    line,
				Message: msg,
				Fatal:   false,
			})
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
		lv, ok := fn.(*interp.LambdaValue)
		if !ok {
			return nil, fmt.Errorf("t.test() second argument must be a function, got %T", fn)
		}

		start := time.Now()
		childResult := &codegen.TestResult{Desc: fmt.Sprintf("%v", desc)}
		childEnv := env.Snapshot()
		childT := &testingT{env: childEnv, result: childResult, pkg: tv.pkg, compName: tv.compName, locale: tv.locale, contextOverrides: tv.contextOverrides}

		callArgs := []any{childT}
		if tv.compName != "" {
			// Snapshot the parent componentValue: the subtest sees parent
			// state at the time of the call, but mutations inside don't leak
			// back to the surrounding test. Preserve compName/body/Funcs/
			// testParams so method dispatch and element-ref walks behave the
			// same as the parent.
			var parentCV *componentValue
			for _, v := range env.Vars {
				if cv, ok := v.(*componentValue); ok {
					parentCV = cv
					break
				}
			}
			if parentCV != nil {
				childCV := &componentValue{
					Env:        childEnv,
					Vars:       make(map[string]any, len(parentCV.Vars)),
					Consts:     parentCV.Consts,
					Funcs:      parentCV.Funcs,
					compName:   parentCV.compName,
					testParams: parentCV.testParams,
					body:       parentCV.body,
				}
				maps.Copy(childCV.Vars, parentCV.Vars)
				// childEnv is a Snapshot() of env, which already has its own
				// Vars copy — make sure the subtest's component-var writes
				// land there (not in the parent env).
				maps.Copy(childEnv.Vars, parentCV.Vars)
				callArgs = append(callArgs, childCV)
			}
		}

		_, callErr := lv.CallWithEnv(childEnv, callArgs)
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
func (tv *testingT) recordFailure(ae *interp.AssertError, fatal bool) {
	if tv == nil || tv.result == nil || ae == nil {
		return
	}
	line := 0
	if a := interp.IRASTOf(ae.Expr); a != nil {
		line = a.ExprPos().Line
	}
	tv.result.Failures = append(tv.result.Failures, codegen.TestFailure{
		Line:    line,
		Message: ae.Msg,
		Fatal:   fatal,
	})
}

// GetField resolves c.field on a componentValue.
func (cv *componentValue) GetField(field string) (any, error) {
	if field == "children" {
		if cv.children == nil {
			cv.children = cv.walkChildren(cv.body)
		}
		return cv.children, nil
	}
	if v, ok := cv.Vars[field]; ok {
		return v, nil
	}
	if v, ok := cv.Consts[field]; ok {
		return v, nil
	}
	// Element-ref takes precedence over a parameterless method of the same
	// name — `c.bump` should yield the element map even when `bump()` is
	// also defined. Try element resolution first; only fall through to
	// method auto-invoke when no element matches.
	if v, err := cv.Env.ResolveElementRef(field); err == nil && v != nil {
		stampOwner(v, cv)
		return v, nil
	}
	// Direct func by bare name (legacy closure-style).
	if fn, ok := cv.Funcs[field]; ok {
		effective := len(fn.Params)
		if effective > 0 && fn.Receiver != "" && fn.Params[0].Name == "this" {
			effective--
		}
		if effective == 0 {
			compEnv := cv.compEnv()
			return compEnv.EvalUserFunc(fn, nil)
		}
	}
	// Receiver-qualified component method (post-#75 desugaring): env.Funcs
	// stores these under "<compName>.<method>". Auto-invoke when zero
	// effective args.
	if cv.compName != "" {
		var fn *ir.Func
		var ok bool
		fn, ok = cv.Funcs[cv.compName+"."+field]
		if !ok {
			fn, ok = cv.Env.Funcs[cv.compName+"."+field]
		}
		if !ok && cv.Env.Pkg != nil {
			for _, f := range cv.Env.Pkg.Funcs {
				if f.Receiver == cv.compName && f.Name == field {
					fn = f
					ok = true
					break
				}
			}
		}
		if ok {
			effective := len(fn.Params)
			if effective > 0 && fn.Receiver != "" && fn.Params[0].Name == "this" {
				effective--
			}
			if effective == 0 {
				compEnv := cv.compEnv()
				var synth []ir.Expr
				if len(fn.Params) > 0 && fn.Params[0].Name == "this" {
					compEnv.Vars["this"] = cv
					synth = []ir.Expr{&ir.Ident{Name: "this"}}
				}
				return compEnv.EvalUserFunc(fn, synth)
			}
		}
	}
	// Element ref lookup in the component body. Returns nil (not an error)
	// when the ref exists in the body tree but is currently hidden by an
	// if/for-else branch — tests assert against null for "not visible".
	// Use the cached child env (not a snapshot) so that any handler bound
	// to the rendered element map invokes against the real component state.
	if v, err := cv.Env.ResolveElementRef(field); err == nil {
		stampOwner(v, cv)
		return v, nil
	}
	return nil, fmt.Errorf("component has no field %q", field)
}

// SetField handles c.field = value.
func (cv *componentValue) SetField(op ast.AssignOp, field string, val any) error {
	cur, exists := cv.Vars[field]
	if !exists {
		return fmt.Errorf("cannot assign to undefined component field %q", field)
	}
	cv.Vars[field] = interp.ApplyOp(op, cur, val)
	if !cv.testParams[field] {
		cv.Env.Vars[field] = cv.Vars[field]
	}
	return nil
}

// InvokeMethod dispatches `method` on the component. Returns (nil, false, nil)
// when the method is not defined on the component; the caller falls back to
// other dispatch paths in that case.
func (cv *componentValue) InvokeMethod(env *interp.Env, method string, args []ir.Expr) (any, bool, error) {
	if len(method) > 0 && method[0] == '@' {
		// Event emission is a no-op in the interpreter.
		return nil, true, nil
	}
	fn, ok := cv.Funcs[method]
	if !ok && cv.compName != "" {
		qual := cv.compName + "." + method
		if extFn, extOK := cv.Env.Funcs[qual]; extOK {
			fn = extFn
			ok = true
		} else if cv.Env.Pkg != nil {
			for _, f := range cv.Env.Pkg.Funcs {
				if f.Receiver == cv.compName && f.Name == method {
					fn = f
					ok = true
					break
				}
			}
		}
	}
	if !ok {
		return nil, false, nil
	}
	// Evaluate args against the CALLER's env (env) so that intra-component
	// recursive calls like `fib(n-1)` see the caller frame's local `n`.
	// Then run the body against compEnv (which carries the component's own
	// vars / consts / funcs, with `this` bound).
	evalArgs := make([]any, len(args))
	for i, ae := range args {
		v, evErr := env.Eval(ae)
		if evErr != nil {
			return nil, true, evErr
		}
		evalArgs[i] = v
	}
	compEnv := cv.compEnv()
	if len(fn.Params) > 0 && fn.Params[0].Name == "this" {
		compEnv.Vars["this"] = cv
	}
	// Inherit the caller's call-depth counter so recursion through component
	// methods hits the same depth limit as plain functions.
	interp.CopyDepth(env, compEnv)
	result, err := compEnv.EvalUserFuncWithValues(fn, evalArgs)
	interp.CopyDepth(compEnv, env)
	for k := range cv.Vars {
		if v, ok := compEnv.Vars[k]; ok {
			cv.Vars[k] = v
			if !cv.testParams[k] {
				cv.Env.Vars[k] = v
			}
		}
	}
	return result, true, err
}

// Toggle flips a bool field, propagating into the underlying env unless the
// field is a test parameter (in which case the field is the canonical value).
func (cv *componentValue) Toggle(field string) error {
	cur, exists := cv.Vars[field]
	if !exists {
		return fmt.Errorf("cannot toggle undefined field %q", field)
	}
	b, ok := cur.(bool)
	if !ok {
		return fmt.Errorf("cannot toggle non-bool field %q", field)
	}
	cv.Vars[field] = !b
	if !cv.testParams[field] {
		cv.Env.Vars[field] = !b
	}
	return nil
}

// WriteBackList stores a (typically mutated-in-place) list back into a
// component field, propagating to env when the field is not a test parameter.
func (cv *componentValue) WriteBackList(field string, list []any) error {
	cv.Vars[field] = list
	if !cv.testParams[field] {
		cv.Env.Vars[field] = list
	}
	return nil
}

// walkChildren returns the direct visual statements of the component's
// body as a slice in source order. User-component NodeInsts become
// *componentValue wrappers sharing the parent env's cached child envs;
// native elements become element-map dicts.
func (cv *componentValue) walkChildren(stmts []ir.Stmt) []any {
	var out []any
	for _, s := range stmts {
		switch n := s.(type) {
		case *ir.NodeInst:
			if n.Component != nil && isUserComponent(n.Component) {
				childEnv := cv.Env.ComponentEnv(n.Component, n)
				wrapper := &componentValue{
					Env:      childEnv,
					Vars:     childEnv.Vars,
					Consts:   childEnv.Consts,
					Funcs:    childEnv.Funcs,
					compName: n.Component.Name,
					body:     n.Component.Body,
				}
				out = append(out, wrapper)
			} else {
				out = append(out, cv.Env.RenderNodeProps(n))
			}
		case *ir.CallStmt:
			// User-component instantiation (`comp()`): expose as a live
			// componentValue wrapper sharing the cached child env.
			if n.Call != nil && cv.Env.Pkg != nil {
				name := interp.CallStmtElemName(n)
				if name != "" {
					if comp := interp.FindComponent(cv.Env.Pkg, name); comp != nil && isUserComponent(comp) {
						childEnv := cv.Env.ComponentEnvFromCallStmt(comp, n)
						wrapper := &componentValue{
							Env:      childEnv,
							Vars:     childEnv.Vars,
							Consts:   childEnv.Consts,
							Funcs:    childEnv.Funcs,
							compName: comp.Name,
							body:     comp.Body,
						}
						out = append(out, wrapper)
						continue
					}
				}
			}
			if rendered := cv.Env.RenderCallStmtNode(n); rendered != nil {
				out = append(out, rendered)
			}
		case *ir.If:
			cond, err := cv.Env.Eval(n.Cond)
			if err != nil {
				continue
			}
			if b, _ := cond.(bool); b {
				out = append(out, cv.walkChildren(n.Body)...)
			} else {
				out = append(out, cv.walkChildren(n.Else)...)
			}
		case *ir.PlatformFilter:
			if n.Platform == "" || n.Platform == "none" {
				out = append(out, cv.walkChildren(n.Body)...)
			}
			// *ir.For: out of scope per spec; components inside for loops
			// need per-iteration child envs. Skipped here.
		}
	}
	return out
}

// stampOwner attaches __ownerComponent to element maps returned by
// ResolveElementRef so that event handlers running in the component's
// env can bind `this` to the owning componentValue.
func stampOwner(v any, owner *componentValue) {
	switch t := v.(type) {
	case map[string]any:
		t["__ownerComponent"] = owner
	case []any:
		for _, item := range t {
			stampOwner(item, owner)
		}
	}
}

// isUserComponent returns true when comp is a user-defined component (has
// its own body, vars, or funcs) rather than a stdlib native element. Native
// elements like `text`/`button` have Component populated with prop schemas
// only, and must not be exposed as live componentValue wrappers.
func isUserComponent(comp *ir.Component) bool {
	if comp == nil {
		return false
	}
	return len(comp.Body) > 0 || len(comp.Vars) > 0 || len(comp.Funcs) > 0
}

func (cv *componentValue) compEnv() *interp.Env {
	env := cv.Env.Snapshot()
	maps.Copy(env.Vars, cv.Vars)
	return env
}

func (cv *componentValue) syncFromEnv(testParams map[string]bool) {
	for k := range cv.Vars {
		if !testParams[k] {
			if v, ok := cv.Env.Vars[k]; ok {
				cv.Vars[k] = v
			}
		}
	}
}

// fireTimers runs each component timer's handler once, honoring the Enabled
// expression. In IR, timers live on ir.Component (not scattered through the
// body), so we walk pkg.Components for the active component.
func fireTimers(pkg *ir.Package, env *interp.Env) error {
	if env.Comp == nil {
		return nil
	}
	for _, t := range env.Comp.Timers {
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
