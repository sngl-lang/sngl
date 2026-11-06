package testrunner

import (
	"strings"
	"time"

	"duckfam.us/sngl/codegen"
	"duckfam.us/sngl/internal/interp"
	"duckfam.us/sngl/ir"
)

// Run executes all test functions in a package and returns results.
func Run(pkg *ir.Package) ([]*codegen.TestResult, error) {
	return Simulate(pkg, "")
}

// Simulate is Run on platform's overrides, with the natives they call stubbed
// (interp.BuildSimulatedEnv); an empty platform is the interpreter's own.
func Simulate(pkg *ir.Package, platform string) ([]*codegen.TestResult, error) {
	if pkg == nil {
		return nil, nil
	}
	// See interp.Prepare: the test runner builds envs without going through a
	// Session, so it takes what a Session takes from the pipeline itself.
	var err error
	if platform == "" {
		pkg, err = interp.Prepare(pkg)
	} else {
		pkg, err = interp.PrepareFor(pkg, platform)
	}
	if err != nil {
		return nil, err
	}
	var results []*codegen.TestResult
	for _, fn := range pkg.Funcs {
		if !fn.IsTest {
			continue
		}
		r := runTestFunc(pkg, fn, platform)
		results = append(results, r)
	}
	return results, nil
}

func runTestFunc(pkg *ir.Package, fn *ir.Func, simulate string) *codegen.TestResult {
	start := time.Now()
	r := &codegen.TestResult{Desc: fn.Name}

	var compName string
	if len(fn.Params) >= 2 {
		if fn.Params[1].Type != nil && fn.Params[1].Type.Decl != nil {
			if c, ok := fn.Params[1].Type.Decl.(*ir.Component); ok {
				compName = c.Name
			}
		}
		r.Component = compName
	}

	// One clock for the test, built here rather than on first use: the same
	// virtual clock the timer schedule runs on has to be what `time.now()`
	// answers with, or a test that reads the clock and ticks it is reading two
	// different times. It has to exist before BuildEnv, which evaluates the
	// component's var initializers.
	clock := interp.NewVirtual()

	env, err := interp.BuildTestEnv(pkg, compName, clock, simulate)
	if err != nil {
		r.Error = err.Error()
		r.Duration = time.Since(start)
		return r
	}

	var cVal *componentValue
	if compName != "" && len(fn.Params) >= 2 {
		cVal = &componentValue{
			Env:      env,
			comp:     findComponentByName(pkg, compName),
			compName: compName,
			fx:       interp.NewEffects(),
		}
		if cVal.comp != nil {
			cVal.body = cVal.comp.Stmts
		}
		// Mounted where the test first needs it (ir.TestSetup): what the
		// statements ahead of that set -- a context, a prop or var's starting
		// value -- is what the mount reads, as a call site's would be.
		cVal.unmounted = true
		cVal.preset = map[ir.Symbol]bool{}
		env.Set(fn.Params[1], cVal)
	}

	// No test reaches the network unless it says so: sngl:http's answer
	// starts as its closed one, as passTestAnswer has a compiled test start.
	if ctx, closed := testAnswer(pkg); ctx != nil {
		if v, err := env.Eval(&ir.Ident{Name: closed.Name, Type: closed.SymType(), Sym: closed}); err == nil {
			env.SetContext(ctx, v)
		}
	}

	tVal := &testingT{env: env, result: r, pkg: pkg, compName: compName, comp: cVal, clock: clock}
	if len(fn.Params) >= 1 {
		env.Set(fn.Params[0], tVal)
	}

	body := fn.Block
	if cVal != nil && cVal.settleErr != nil {
		r.Failures = append(r.Failures, codegen.TestFailure{Message: cVal.settleErr.Error(), Fatal: true})
		body = nil
	}
	for _, stmt := range body {
		// The instance mounts on the first read of it (settle), and on a
		// read of package state, which goes through no instance.
		if cVal != nil && cVal.unmounted && readsPackageState(stmt, pkg) {
			cVal.mount()
		}
		if cVal != nil && cVal.settleErr != nil {
			err := cVal.settleErr
			cVal.settleErr = nil
			r.Failures = append(r.Failures, codegen.TestFailure{Line: errorLine(err, stmt), Message: err.Error(), Fatal: true})
			break
		}
		err := env.Exec(stmt)
		if interp.IsReturn(err) {
			break
		}
		if err != nil {
			r.Failures = append(r.Failures, codegen.TestFailure{
				Line:    errorLine(err, stmt),
				Message: err.Error(),
				Fatal:   true,
			})
			break
		}
		// A statement that fired an event or called a method has run a
		// handler, and the effects it calls for run before the next
		// statement, as a compiled test settles after each write: a read of
		// package state goes through no instance to settle it.
		if cVal != nil && !cVal.unmounted {
			cVal.settle()
			if err := cVal.settleErr; err != nil {
				cVal.settleErr = nil
				r.Failures = append(r.Failures, codegen.TestFailure{
					Line:    errorLine(err, stmt),
					Message: err.Error(),
					Fatal:   true,
				})
				break
			}
		}
	}

	// A test that never needed it still mounts it, so what mounting does
	// is torn down and checked as in any other.
	if cVal != nil && cVal.unmounted && len(r.Failures) == 0 {
		cVal.mount()
		if err := cVal.settleErr; err != nil {
			cVal.settleErr = nil
			r.Failures = append(r.Failures, codegen.TestFailure{Message: err.Error(), Fatal: true})
		}
	}
	if err := tVal.teardown(); err != nil && len(r.Failures) == 0 {
		r.Failures = append(r.Failures, codegen.TestFailure{Message: err.Error(), Fatal: true})
	}

	composeFailures(r)
	r.Log = env.Log
	r.Passed = len(r.Failures) == 0 && allPassed(r.Children)
	r.Duration = time.Since(start)
	return r
}

// teardown ends the application a test is: the tree unmounts, so every
// lifetime's `@unmount` runs, newest first; the timers stop; and whatever owned
// value is still held after that is a leak. A test that already failed
// stopped where it failed, so what it leaves held says nothing more.
func (tv *testingT) teardown() error {
	tv.env.Unmounted()
	if cv := tv.comp; cv != nil && cv.fx != nil {
		if err := cv.fx.Retarget(nil, cv.Env); err != nil {
			return err
		}
	}
	tv.timers = nil
	if fn := tv.pkg.Teardown; fn != nil {
		if _, err := tv.env.CallUserFuncValues(fn, nil); err != nil {
			return err
		}
	}
	return tv.env.LeakError()
}

// composeFailures derives the legacy Error / ErrorLine fields from the
// structured Failures list so existing consumers (CLI printer, JSON
// output) keep working unchanged.
func composeFailures(r *codegen.TestResult) {
	if len(r.Failures) == 0 {
		return
	}
	parts := make([]string, len(r.Failures))
	for i, f := range r.Failures {
		parts[i] = f.Message
	}
	r.Error = strings.Join(parts, "\n")
	r.ErrorLine = r.Failures[0].Line
}

// testingT is the runtime value for the Test parameter in test functions.
type testingT struct {
	env      *interp.Env
	result   *codegen.TestResult
	pkg      *ir.Package
	compName string
	// comp is the test function's component parameter. Held rather than
	// looked up: two componentValues are live inside a subtest — the
	// enclosing test's and the subtest's own — and they are distinct
	// bindings, so searching the env for one picks arbitrarily.
	comp *componentValue
	// locale is the BCP-47 locale set by t.setLocale() or t.setContext(locale,…).
	// It overrides the env's default locale for i18n calls made within this test.
	locale string
	// contextOverrides accumulates the overrides established by t.setContext()
	// calls so they can be propagated to child sub-tests created by t.test().
	contextOverrides map[*ir.Context]any
	// clock and timers are the test's simulated time. A test advances time
	// explicitly, so the clock is virtual: t.wait() spends simulated
	// milliseconds, not real ones. The clock is the env's and is never built
	// here -- lazily, it was a *second* clock, so `time.now()` read one and
	// `t.tick()` moved the other. A subtest is handed the parent's for the
	// same reason.
	clock  *interp.Virtual
	timers *interp.Timers
}

// sched re-derives the test's timer schedule from the tree the component
// currently renders.
//
// Re-derived on every call rather than built once, because a schedule is a fact
// about the rendered tree and the tree is a fact about state a test is free to
// move: `c.running = false` before a tick has to leave nothing to fire, and
// that is the same question as whether the branch holding the timer renders.
// Phase survives it -- Retarget keys on the mounted path, so a deadline already
// scheduled keeps the one it had.
//
// env is the scope the tick will run in, and must be the same one the caller
// then hands to Tick: each entry holds the env it was mounted against, so a
// handler that writes state writes it where the caller reads it back from.
// compEnv() snapshots, so two calls are two scopes and the write would land in
// the one that is thrown away.
func (tv *testingT) sched(env *interp.Env) (*interp.Timers, error) {
	if tv.timers == nil {
		tv.timers = interp.NewTimers(tv.clock)
	}
	v, err := interp.Mount(env)
	if err != nil {
		return nil, err
	}
	tv.timers.Retarget(v)
	return tv.timers, nil
}

// componentValue is the runtime value of a test function's component
// parameter. It holds no state of its own: `c.<field>` names a declaration on
// the component, so a read or write resolves that declaration and goes
// straight to the env's binding for it. The name-addressed step is the
// boundary — a test writes `c.count`, and only the component knows which
// declaration that is.
type componentValue struct {
	Env      *interp.Env
	comp     *ir.Component // resolves c.<field> and c.<method> to what it names
	compName string        // e.g. "pricing"; used to look up receiver-qualified methods

	// body is the component's lowered body, captured at construction.
	// Used by walkChildren to enumerate direct visual statements.
	body []ir.Stmt

	// children is a memoized live slice — user-component entries are
	// *componentValue wrappers, native entries are element-map dicts.
	// Computed lazily on first GetField("children").
	children []any

	// settleErr holds the first effect failure, surfaced by the next read.
	settleErr error

	// by is the instance this one was read off as a ref, whose body wrote the
	// populations this one was handed; nil for the instance a test was given.
	by *componentValue
	// inst is the node that rendered this one, nil for the instance a test was
	// given, and site the mounted node it was read off.
	inst *ir.NodeInst
	site *interp.Node

	// unmounted holds while the test is setting the instance up: nothing is
	// rendered or settled, and each prop or var a write sets is in preset,
	// which the mount leaves as the test set it. The instance a test was
	// given only.
	unmounted bool
	preset    map[ir.Symbol]bool

	// fx is the running set of lifetime brackets, on the root wrapper only:
	// the mount walk descends through components, so one set covers every
	// effect in the tree and a child wrapper has nothing of its own to settle.
	// Nil on a child.
	fx *interp.Effects
}

// settle runs the effect handlers the current state calls for.
//
// A test harness has no event loop, so there is no moment between a handler
// returning and the next assertion for a render to happen in -- which makes
// "the node entered the tree" and "the tree was read" the same moment here, and
// reading is the only one of the two this can observe. Every field read and
// every field write goes through it, which is every way a test reaches the
// program.
//
// A failure to settle is reported rather than returned: the caller is a field
// read whose signature says nothing about effects, and a test that cannot
// settle has already gone wrong somewhere the assertion will show.
func (cv *componentValue) settle() {
	if cv != nil && cv.unmounted {
		cv.mount()
		return
	}
	if cv == nil || cv.fx == nil || cv.Env == nil {
		return
	}
	v, err := interp.Settle(cv.fx, cv.Env)
	if err == nil {
		err = cv.Env.Paint(v)
	}
	if err != nil {
		cv.settleErr = err
	}
}

// mount starts the instance a test was given from what the test set up: each
// prop and var it did not set from its initializer, which reads the contexts
// the test set, and then the first render.
func (cv *componentValue) mount() {
	if !cv.unmounted {
		return
	}
	cv.unmounted = false
	if cv.comp != nil {
		interp.InitComponentState(cv.Env, cv.comp, cv.preset)
	}
	cv.settle()
}

// testAnswer is sngl:http's answer and the function a test starts it as,
// where the program imports sngl:http, found by their marks.
func testAnswer(pkg *ir.Package) (*ir.Context, *ir.Func) {
	seen := map[*ir.Package]bool{}
	var visit func(p *ir.Package) (*ir.Context, *ir.Func)
	visit = func(p *ir.Package) (*ir.Context, *ir.Func) {
		if p == nil || seen[p] {
			return nil, nil
		}
		seen[p] = true
		for _, c := range p.Contexts {
			if c.Builtin != ir.BuiltinHTTPAnswer {
				continue
			}
			for _, f := range p.Funcs {
				if f.Builtin == ir.BuiltinHTTPClosed {
					return c, f
				}
			}
		}
		for _, imp := range p.Imports {
			if c, f := visit(imp.Pkg); c != nil {
				return c, f
			}
		}
		return nil, nil
	}
	return visit(pkg)
}

// readsPackageState reports whether s reads a var of the package's.
func readsPackageState(s ir.Stmt, pkg *ir.Package) bool {
	vars := map[ir.Symbol]bool{}
	for _, v := range pkg.Vars {
		vars[v] = true
	}
	found := false
	_ = ir.Walk(s, func(n ir.Node) error {
		if id, ok := n.(*ir.Ident); ok && vars[id.Sym] {
			found = true
			return ir.SkipAll
		}
		return nil
	})
	return found
}

// errorLine returns the 1-based source line of the failing expression. For
// AssertError it uses the asserted expression's AST position; for other
// errors it falls back to the enclosing statement's position when known.
func errorLine(err error, stmt ir.Stmt) int {
	if ae, ok := err.(*interp.AssertError); ok && ae != nil {
		if a := interp.IRASTOf(ae.Expr); a != nil {
			return a.ExprPos().Line
		}
	}
	if cs, ok := stmt.(*ir.CallStmt); ok && cs.Call != nil && cs.Call.AST != nil {
		return cs.Call.AST.Pos.Line
	}
	return 0
}

func allPassed(results []*codegen.TestResult) bool {
	for _, r := range results {
		if !r.Passed {
			return false
		}
	}
	return true
}

// findComponentByName scans the package for a component with the given name.
func findComponentByName(pkg *ir.Package, name string) *ir.Component {
	for _, c := range pkg.Components {
		if c.Name == name {
			return c
		}
	}
	return nil
}
