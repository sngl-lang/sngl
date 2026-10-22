package testrunner

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"time"

	"duckfam.us/sngl/ast"
	"duckfam.us/sngl/codegen"
	"duckfam.us/sngl/codegen/testharness/snapshot"
	"duckfam.us/sngl/internal/interp"
	"duckfam.us/sngl/ir"
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
			ts, err := tv.sched(compEnv)
			if err != nil {
				return nil, err
			}
			if _, err := ts.Tick(compEnv); err != nil {
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

		// The deadline is simulated, not wall-clock: a test asking to wait
		// 1000ms for a 10ms timer spends three ticks, not a real second.
		deadline := tv.clock.Now().Add(time.Duration(timeoutMs) * time.Millisecond)

		// A test function may take no component, in which case there is
		// nothing to tick. The predicate still runs once: it may be closing
		// over package state, and it ran once before this loop had a schedule.
		cv := tv.comp
		var ts *interp.Timers
		var compEnv *interp.Env
		if cv != nil {
			compEnv = cv.compEnv()
			var serr error
			if ts, serr = tv.sched(compEnv); serr != nil {
				return nil, serr
			}
		}

		for {
			// Predicate runs in its captured env so c reads observe in-place
			// component-var updates from the timer handlers.
			out, callErr := lv.Call(nil)
			if callErr != nil {
				return nil, callErr
			}
			if b, ok := out.(bool); ok && b {
				return nil, nil
			}
			// Nothing left to advance the clock: waiting longer cannot change
			// the answer, and looping on an unmoving clock would never return.
			if ts == nil {
				return nil, nil
			}
			if _, any := ts.Next(); !any {
				return nil, nil
			}
			if !tv.clock.Now().Before(deadline) {
				return nil, nil
			}
			if _, err := ts.Tick(compEnv); err != nil {
				return nil, err
			}
			cv.Env.RebindFrom(compEnv)
			// Re-derived each round: a handler may have moved state the next
			// deadline's position depends on, and a schedule is a fact about
			// the tree that state renders.
			if _, err := tv.sched(compEnv); err != nil {
				return nil, err
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
		childEnv := env.Isolate()
		// The parent's clock, not one of its own: childEnv is a Snapshot and
		// carries `Clock`, so a subtest's `time.now()` already reads the
		// parent's. A second clock here would start back at Epoch, so
		// `t.tick()` would advance one while `now()` read the other -- and
		// time would appear to run backwards across the subtest boundary.
		childT := &testingT{env: childEnv, result: childResult, pkg: tv.pkg, compName: tv.compName, locale: tv.locale, contextOverrides: tv.contextOverrides, clock: tv.clock}

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
func wrapChildComponent(n *interp.Node, by *componentValue) *componentValue {
	return &componentValue{
		Env:      n.CompEnv,
		comp:     n.Component,
		compName: n.Component.Name,
		body:     n.Component.Stmts,
		by:       by,
		inst:     n.Inst,
		site:     n,
	}
}

// Populated is the instance this one was read off, this one's scope, and the
// node that rendered it.
func (cv *componentValue) Populated() (interp.ComponentValue, *interp.Env, *ir.NodeInst) {
	if cv.by == nil {
		return nil, cv.Env, cv.inst
	}
	return cv.by, cv.Env, cv.inst
}

// GetField resolves c.field on a componentValue.
func (cv *componentValue) GetField(field string) (any, error) {
	cv.settle()
	if cv.settleErr != nil {
		err := cv.settleErr
		cv.settleErr = nil
		return nil, err
	}
	if sym := cv.fieldSym(field); sym != nil {
		if v, ok := cv.Env.Value(sym); ok {
			return v, nil
		}
	}
	// A user-component instance addressed by #id (`main #m()` reached as
	// `c.m`) yields its live component wrapper, so `c.m.<member>` resolves
	// against the child's own vars/props/methods/refs.
	if w := cv.childComponentByID(field); w != nil {
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
		// One the interpreter answers natively -- a stack's `current` --
		// has no body to run, and is handed the instance.
		if fn.Intrinsic != "" {
			if v, ok, err := interp.RunIntrinsic(fn.Intrinsic, []any{cv}); ok {
				return v, err
			}
		}
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

// ElementRef returns the nodes carrying #id that this instance's own body
// wrote, or that a population it wrote supplied to the instance supplied.
func (cv *componentValue) ElementRef(id string, supplied *interp.Env) (interp.RefSet, error) {
	cv.settle()
	if cv.settleErr != nil {
		err := cv.settleErr
		cv.settleErr = nil
		return interp.RefSet{}, err
	}
	// A child mounted on its own has no call site, so its slots would show their
	// fallbacks whatever the caller wrote: its refs are read off the tree the
	// outermost instance mounts.
	root := cv
	for root.by != nil {
		root = root.by
	}
	view, err := interp.Mount(root.Env)
	if err != nil {
		return interp.RefSet{}, err
	}
	view = view.OwnedBy(cv.Env)
	set := interp.RefSet{View: view, Supplied: supplied}
	for _, n := range view.Refs(id, supplied) {
		switch {
		case n.Component != nil && n.CompEnv != nil && isUserComponent(n.Component):
			set.Hits = append(set.Hits, interp.RefHit{Value: wrapChildComponent(n, cv), Node: n})
		case n.IsComponent() && n.Expanded:
		default:
			m := n.Map()
			stampOwner(m, cv)
			set.Hits = append(set.Hits, interp.RefHit{Value: m, Node: n})
		}
	}
	return set, nil
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
	cv.Env.NoteReplace(sym)
	// A two-way prop's cell is the var its call site bound, where it bound one.
	if err := cv.Env.WriteBound(sym, nv); err != nil {
		return err
	}
	// After the write, not before: the assignment is what the new lifetimes are
	// keyed on, so settling first would reconcile against the old value and
	// leave the change to whatever read came next.
	cv.settle()
	if cv.settleErr != nil {
		err := cv.settleErr
		cv.settleErr = nil
		return err
	}
	return nil
}

// callSite is the node the instance was rendered from, or nil for the
// instance a test was given or a component the program does not declare.
func (cv *componentValue) callSite() *interp.Node {
	if cv.site == nil || cv.comp == nil || cv.comp.PkgPath() != "" {
		return nil
	}
	return cv.site
}

// declaresEvent reports whether the instance's component declares the event.
func (cv *componentValue) declaresEvent(name string) bool {
	return cv.comp != nil && slices.ContainsFunc(cv.comp.Events, func(e *ir.EventDecl) bool { return e.Name == name })
}

// InvokeMethod dispatches `method` on the component. Returns (nil, false, nil)
// when the method is not defined on the component; the caller falls back to
// other dispatch paths in that case.
func (cv *componentValue) InvokeMethod(env *interp.Env, method string, args []ir.CallArg) (any, bool, error) {
	if len(method) > 0 && method[0] == '@' {
		evalArgs := func() ([]any, error) {
			vals := make([]any, 0, len(args))
			for _, a := range args {
				v, err := env.Eval(a.Value)
				if err != nil {
					return nil, err
				}
				vals = append(vals, v)
			}
			return vals, nil
		}
		// A program's component emits its events from the program's code,
		// so the event is the call site's handler, run where the call site
		// was written.
		if n := cv.callSite(); n != nil && cv.declaresEvent(method[1:]) {
			m := n.Map()
			if _, ok := m[method].(*ir.Func); !ok {
				return nil, true, fmt.Errorf("@%s fired on %s, whose call site handles none of it", method[1:], cv.compName)
			}
			return env.InvokeElementEvent(m, method[1:], evalArgs)
		}
		// The event reaches what the instance renders that reports it, as a
		// user's click reaches the widget: a `nav.link` is clicked through the
		// primitive its override renders.
		view, err := interp.Mount(cv.Env)
		if err != nil {
			return nil, true, err
		}
		// The press of one copy a loop of its body renders, whose handler
		// emits the event with the copy's index -- a tab of a strip -- before
		// anything in what it holds that reports the event: a select in the
		// panel is the panel's writer's, and the compiled targets press the
		// tab too (lower.NamePresses).
		// The arguments are evaluated once, as a compiled test does, whichever
		// of the two answers the event.
		handOn := evalArgs
		if len(args) == 1 {
			vals, err := evalArgs()
			if err != nil {
				return nil, true, err
			}
			if n, press := view.Choosing(method[1:], vals[0]); n != nil {
				return env.InvokeElementEvent(n.Map(), press, func() ([]any, error) { return nil, nil })
			}
			handOn = func() ([]any, error) { return vals, nil }
		}
		if n := view.Handling(method[1:]); n != nil {
			return env.InvokeElementEvent(n.Map(), method[1:], handOn)
		}
		return nil, true, fmt.Errorf("@%s fired on %s reaches nothing that handles it", method[1:], cv.compName)
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
	cv.Env.NoteReplace(sym)
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

// FieldCell is the binding field names in the instance's scope.
func (cv *componentValue) FieldCell(field string) (*interp.Env, ir.Symbol, bool) {
	sym, err := cv.writableFieldSym(field)
	if err != nil || sym == nil {
		return nil, nil, false
	}
	owner, ok := cv.Env.CellOf(sym)
	return owner, sym, ok
}

// childComponentByID returns the live componentValue wrapper for a user
// component instantiated in the component's body with element id == id (e.g.
// `main #m()` reached as `c.m`), or nil.
//
// It reads the mounted tree rather than walking the IR. The tree keeps a
// component instantiation as a node carrying its own scope, which is exactly
// what a wrapper needs -- and it reaches ids the old walk could not, since that
// one descended `if` branches only and skipped loops, slots and boundaries.
func (cv *componentValue) childComponentByID(id string) any {
	view, err := interp.Mount(cv.Env)
	if err != nil {
		return nil
	}
	for _, n := range view.Refs(id, nil) {
		if n.Component == nil || n.CompEnv == nil || !isUserComponent(n.Component) {
			continue
		}
		return wrapChildComponent(n, cv)
	}
	return nil
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
	return len(comp.Stmts) > 0 || len(comp.Vars) > 0 || len(comp.Funcs) > 0
}

// InstanceEnv is the instance this wrapper stands for, which is what a
// handle read as a value compares by.
func (cv *componentValue) InstanceEnv() *interp.Env { return cv.Env }

func (cv *componentValue) compEnv() *interp.Env {
	return cv.Env.Snapshot()
}

func (cv *componentValue) funcNamed(name string) *ir.Func {
	if cv.comp == nil {
		return nil
	}
	for _, fn := range cv.comp.Funcs {
		if fn.Name == name && !fn.IsMethod() {
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

// The interpreter is the none language's test harness, and the reference: it
// answers every sngl:test method, the ones no compiled harness lowers yet
// included.
func init() {
	codegen.DeclareLangImplements("none", ir.TestAssertID, ir.TestMustID, ir.TestSetLocaleID,
		ir.TestSetContextID, ir.TestWaitID, ir.TestSnapshotID, ir.TestTickID, ir.TestSubtestID)
}
