package testrunner

import (
	"strings"
	"time"

	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/internal/interp"
	"git.duckfam.us/jonathan/sngl/ir"
)

// Run executes all test functions in a package and returns results.
func Run(pkg *ir.Package) ([]*codegen.TestResult, error) {
	if pkg == nil {
		return nil, nil
	}
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
		}
		if cVal.comp != nil {
			cVal.body = cVal.comp.Body
		}
		env.Set(fn.Params[1], cVal)
	}

	tVal := &testingT{env: env, result: r, pkg: pkg, compName: compName, comp: cVal}
	if len(fn.Params) >= 1 {
		env.Set(fn.Params[0], tVal)
	}

	for _, stmt := range fn.Block {
		if err := env.Exec(stmt); err != nil {
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
