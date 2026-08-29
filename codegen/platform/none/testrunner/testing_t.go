package testrunner

import (
	"fmt"
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
		if cv := tv.comp; cv != nil {
			compEnv := cv.compEnv()
			if err := fireTimers(tv.pkg, compEnv); err != nil {
				return nil, err
			}
			cv.Env.RebindFrom(compEnv)
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

		cv := tv.comp

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
			cv.Env.RebindFrom(compEnv)
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
							env.Set(v, newVal)
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
		cv := tv.comp
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
		if tv.compName != "" && tv.comp != nil {
			// Re-point the parent componentValue at the subtest env: the
			// subtest sees parent state at the time of the call (childEnv is a
			// Snapshot, which copied it), and its writes land there rather
			// than in the surrounding test's env.
			childCV := *tv.comp
			childCV.Env = childEnv
			childT.comp = &childCV
			callArgs = append(callArgs, &childCV)
		}

		_, callErr := lv.CallWithEnv(childEnv, callArgs)
		if callErr != nil {
			childResult.Error = callErr.Error()
		}
		childResult.Log = childEnv.Log
		// Its own failures count, as they do for a top-level test: a
		// subtest whose assertion failed reported PASS without this.
		childResult.Passed = childResult.Error == "" && len(childResult.Failures) == 0 && allPassed(childResult.Children)
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

// wrapChildComponent exposes a nested component instance as a value, backed
// by the env its own state lives in.
func wrapChildComponent(childEnv *interp.Env, comp *ir.Component) *componentValue {
	return &componentValue{
		Env:      childEnv,
		comp:     comp,
		compName: comp.Name,
		body:     comp.Body,
	}
}

// GetField resolves c.field on a componentValue.
func (cv *componentValue) GetField(field string) (any, error) {
	if field == "children" {
		if cv.children == nil {
			cv.children = cv.walkChildren(cv.body)
		}
		return cv.children, nil
	}
	if sym := cv.fieldSym(field); sym != nil {
		if v, ok := cv.Env.Value(sym); ok {
			return v, nil
		}
	}
	// A user-component instance addressed by #id (`main #m()` reached as
	// `c.m`) yields its live component wrapper, so `c.m.<member>` resolves
	// against the child's own vars/props/methods/refs.
	if w := cv.childComponentByID(cv.body, field); w != nil {
		return w, nil
	}
	// Element-ref takes precedence over a parameterless method of the same
	// name — `c.bump` should yield the element map even when `bump()` is
	// also defined. Try element resolution first; only fall through to
	// method auto-invoke when no element matches.
	if v, err := cv.Env.ResolveElementRef(field); err == nil && v != nil {
		stampOwner(v, cv)
		return v, nil
	}
	// A parameterless func or method on the component reads as a value.
	if fn := cv.funcNamed(field); fn != nil && effectiveArity(fn) == 0 {
		compEnv := cv.compEnv()
		var synth []ir.Expr
		if len(fn.Params) > 0 && fn.Params[0].Receiver {
			compEnv.SetReceiver(cv)
			synth = []ir.Expr{&ir.Ident{Name: ir.ReceiverParam}}
		}
		return compEnv.EvalUserFunc(fn, synth)
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
	sym, err := cv.writableFieldSym(field)
	if err != nil {
		return err
	}
	if sym == nil {
		return fmt.Errorf("cannot assign to undefined component field %q", field)
	}
	cur, _ := cv.Env.Value(sym)
	// Component-field values are dynamically typed in the test harness, so the
	// declared width is not available here; ApplyOp falls back to inferring the
	// width from the runtime carrier.
	nv, err := interp.ApplyOp(op, cur, val, nil)
	if err != nil {
		return err
	}
	cv.Env.Set(sym, nv)
	return nil
}

// InvokeMethod dispatches `method` on the component. Returns (nil, false, nil)
// when the method is not defined on the component; the caller falls back to
// other dispatch paths in that case.
func (cv *componentValue) InvokeMethod(env *interp.Env, method string, args []ir.CallArg) (any, bool, error) {
	if len(method) > 0 && method[0] == '@' {
		// Event emission is a no-op in the interpreter.
		return nil, true, nil
	}
	fn := cv.funcNamed(method)
	if fn == nil {
		return nil, false, nil
	}
	// Evaluate args against the CALLER's env (env) so that intra-component
	// recursive calls like `fib(n-1)` see the caller frame's local `n`.
	// Then run the body against compEnv (which carries the component's own
	// vars / consts / funcs, with `this` bound).
	// Evaluate arg expressions in the CALLER's env so that closures like
	// `fib(n-1)` see the caller frame's local variables. Build a by-name
	// map so named args bind to the right param; fill in defaults for gaps.
	named := map[string]any{}
	// Determine which params are addressable by positional args (skip receiver).
	addrParams := fn.Params
	if len(fn.Params) > 0 && fn.Params[0].Receiver {
		addrParams = fn.Params[1:]
	}
	positional := 0
	for _, a := range args {
		v, evErr := env.Eval(a.Value)
		if evErr != nil {
			return nil, true, evErr
		}
		if a.Name != "" {
			named[a.Name] = v
		} else if positional < len(addrParams) {
			named[addrParams[positional].Name] = v
			positional++
		}
	}
	// Build positional slice in param order, filling in defaults.
	// Skip the receiver param (param[0] when Receiver==true) — it is bound
	// via compEnv.SetReceiver below, not through the arg slice.
	// evalUserFuncCore's argOffset trick requires len(args)==len(fn.Params)-1
	// for methods with a receiver.
	compEnv := cv.compEnv()
	nonReceiverParams := fn.Params
	if len(fn.Params) > 0 && fn.Params[0].Receiver {
		nonReceiverParams = fn.Params[1:]
		compEnv.SetReceiver(cv)
	}
	evalArgs := make([]any, len(nonReceiverParams))
	for i, p := range nonReceiverParams {
		if v, ok := named[p.Name]; ok {
			evalArgs[i] = v
		} else if p.Default != nil {
			dv, evErr := env.Eval(p.Default)
			if evErr == nil {
				evalArgs[i] = dv
			}
		}
	}
	// Inherit the caller's call-depth counter so recursion through component
	// methods hits the same depth limit as plain functions.
	interp.CopyDepth(env, compEnv)
	result, err := compEnv.EvalUserFuncWithValues(fn, evalArgs)
	interp.CopyDepth(compEnv, env)
	cv.Env.RebindFrom(compEnv)
	return result, true, err
}

func (cv *componentValue) Toggle(field string) error {
	sym, err := cv.writableFieldSym(field)
	if err != nil {
		return err
	}
	if sym == nil {
		return fmt.Errorf("cannot toggle undefined field %q", field)
	}
	cur, _ := cv.Env.Value(sym)
	b, ok := cur.(bool)
	if !ok {
		return fmt.Errorf("cannot toggle non-bool field %q", field)
	}
	cv.Env.Set(sym, !b)
	return nil
}

func (cv *componentValue) WriteBackList(field string, list []any) error {
	sym, err := cv.writableFieldSym(field)
	if err != nil {
		return err
	}
	if sym == nil {
		return fmt.Errorf("no component field %q to write back", field)
	}
	cv.Env.Set(sym, list)
	return nil
}

// walkChildren returns the direct visual statements of the component's
// body as a slice in source order. User-component NodeInsts become
// *componentValue wrappers sharing the parent env's cached child envs;
// native elements become element-map dicts.
// childComponentByID returns the live componentValue wrapper for a user
// component instantiated in stmts with element id == id (e.g. `main #m()`
// reached as `c.m`), or nil. Mirrors walkChildren's wrapper construction but
// selects a single instance by id. Descends if/platform branches; for-loops
// are out of scope (per-iteration envs), matching walkChildren.
func (cv *componentValue) childComponentByID(stmts []ir.Stmt, id string) *componentValue {
	for _, s := range stmts {
		switch n := s.(type) {
		case *ir.NodeInst:
			if n.ID == id && n.Component != nil && isUserComponent(n.Component) {
				childEnv := cv.Env.ComponentEnv(n.Component, n)
				return wrapChildComponent(childEnv, n.Component)
			}
		case *ir.CallStmt:
			if n.Call != nil && n.Call.AST != nil && n.Call.AST.ID == id && cv.Env.Pkg != nil {
				if name := interp.CallStmtElemName(n); name != "" {
					if comp := interp.FindComponent(cv.Env.Pkg, name); comp != nil && isUserComponent(comp) {
						childEnv := cv.Env.ComponentEnvFromCallStmt(comp, n)
						return wrapChildComponent(childEnv, comp)
					}
				}
			}
		case *ir.If:
			cond, err := cv.Env.Eval(n.Cond)
			if err != nil {
				continue
			}
			branch := n.Body
			if b, _ := cond.(bool); !b {
				branch = n.Else
			}
			if w := cv.childComponentByID(branch, id); w != nil {
				return w
			}
		}
	}
	return nil
}

func (cv *componentValue) walkChildren(stmts []ir.Stmt) []any {
	var out []any
	for _, s := range stmts {
		switch n := s.(type) {
		case *ir.NodeInst:
			if n.Component != nil && isUserComponent(n.Component) {
				childEnv := cv.Env.ComponentEnv(n.Component, n)
				wrapper := wrapChildComponent(childEnv, n.Component)
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
						wrapper := wrapChildComponent(childEnv, comp)
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
	return cv.Env.Snapshot()
}

func (cv *componentValue) funcNamed(name string) *ir.Func {
	if cv.comp == nil {
		return nil
	}
	for _, fn := range cv.comp.Funcs {
		if fn.Name == name && fn.Receiver == "" {
			return fn
		}
	}
	return cv.comp.Methods[name]
}

func effectiveArity(fn *ir.Func) int {
	n := len(fn.Params)
	if n > 0 && fn.Params[0].Receiver {
		n--
	}
	return n
}

func (cv *componentValue) fieldSym(field string) ir.Symbol {
	if cv.comp != nil {
		for _, v := range cv.comp.Vars {
			if v.Name == field {
				return v
			}
		}
		for _, p := range cv.comp.Props {
			if p.Name == field && p.Sym != nil {
				return p.Sym
			}
		}
	}
	if pkg := cv.Env.Pkg; pkg != nil {
		for _, v := range pkg.Vars {
			if v.Name == field {
				return v
			}
		}
		for _, c := range pkg.Consts {
			if c.Name == field {
				return c
			}
		}
	}
	return nil
}

// writableFieldSym is fieldSym for the three writers. A const resolves for a
// read but is not a place: the name/value split that used to keep it out of
// SetField is gone, so the declaration has to be asked.
func (cv *componentValue) writableFieldSym(field string) (ir.Symbol, error) {
	sym := cv.fieldSym(field)
	if sym == nil {
		return nil, nil
	}
	if v, ok := sym.(*ir.Var); ok && v.IsConst {
		return nil, fmt.Errorf("cannot assign to const %q", field)
	}
	return sym, nil
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
