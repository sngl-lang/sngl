package testrunner

import (
	"strings"
	"time"

	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/internal/interp"
	"git.duckfam.us/jonathan/sngl/ir"
)

// platformName is the identifier `sngl:platform/none` is served under, and the
// key its overrides are recorded at. Spelled here rather than imported from the
// generator, which imports this package.
const platformName = "none"

// Run executes all test functions in a package and returns results.
func Run(pkg *ir.Package) ([]*codegen.TestResult, error) {
	if pkg == nil {
		return nil, nil
	}
	// The interpreter is a target, and a target gets the bodies its own platform
	// package declares. `sngl test` runs no lowering passes on purpose -- the
	// interpreter is written against checked IR -- so this is the one thing it
	// still has to take from the pipeline: without it `sngl:time`'s `timer`
	// expands to the empty stub every stdlib component is before a platform
	// implements it.
	ir.SpecializeForTarget(pkg, platformName, "")
	var results []*codegen.TestResult
	for _, fn := range pkg.Funcs {
		if !fn.IsTest {
			continue
		}
		r := runTestFunc(pkg, fn)
		results = append(results, r)
	}
	return results, nil
}

func runTestFunc(pkg *ir.Package, fn *ir.Func) *codegen.TestResult {
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

	env, err := interp.BuildEnv(pkg, compName)
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
			cVal.body = cVal.comp.Body
		}
		// Mounted here rather than on the first read. A test whose opening
		// statement is a write -- `c.running = false` before any assertion --
		// would otherwise settle for the first time after it, and the initial
		// lifetime would never have existed to be torn down: the write would
		// read as the program starting in its new state instead of moving to
		// it.
		cVal.settle()
		env.Set(fn.Params[1], cVal)
	}

	tVal := &testingT{env: env, result: r, pkg: pkg, compName: compName, comp: cVal}
	if len(fn.Params) >= 1 {
		env.Set(fn.Params[0], tVal)
	}

	for _, stmt := range fn.Block {
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
	}

	composeFailures(r)
	r.Log = env.Log
	r.Passed = len(r.Failures) == 0 && allPassed(r.Children)
	r.Duration = time.Since(start)
	return r
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
	// clock and timers are the test's simulated time. Built on first use
	// because a test function without a component parameter has neither.
	// A test advances time explicitly, so the clock is virtual: t.wait()
	// spends simulated milliseconds, not real ones.
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
	if tv.clock == nil {
		tv.clock = interp.NewVirtual()
	}
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

	// children is a memoised live slice — user-component entries are
	// *componentValue wrappers, native entries are element-map dicts.
	// Computed lazily on first GetField("children").
	children []any

	// settleErr holds the first effect failure, surfaced by the next read.
	settleErr error

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
	if cv == nil || cv.fx == nil || cv.Env == nil {
		return
	}
	if _, err := interp.Settle(cv.fx, cv.Env); err != nil {
		cv.settleErr = err
	}
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
