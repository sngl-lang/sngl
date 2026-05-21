package testrunner

import (
	"maps"
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

	testParams := map[string]bool{}
	for _, p := range fn.Params {
		testParams[p.Name] = true
	}

	var cVal *componentValue
	if compName != "" && len(fn.Params) >= 2 {
		cVal = &componentValue{
			Env:        env,
			Vars:       make(map[string]any),
			Consts:     make(map[string]any),
			Funcs:      make(map[string]*ir.Func),
			compName:   compName,
			testParams: testParams,
		}
		maps.Copy(cVal.Vars, env.Vars)
		maps.Copy(cVal.Consts, env.Consts)
		maps.Copy(cVal.Funcs, env.Funcs)
		env.Vars[fn.Params[1].Name] = cVal
	}

	tVal := &testingT{env: env, result: r, pkg: pkg, compName: compName}
	tParamName := "t"
	if len(fn.Params) >= 1 {
		tParamName = fn.Params[0].Name
	}
	env.Vars[tParamName] = tVal

	for _, stmt := range fn.Block {
		if err := env.Exec(stmt); err != nil {
			r.Failures = append(r.Failures, codegen.TestFailure{
				Line:    errorLine(err, stmt),
				Message: err.Error(),
				Fatal:   true,
			})
			break
		}
		if cVal != nil {
			cVal.syncFromEnv(testParams)
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
	// locale is the BCP-47 locale set by t.setLocale() or t.setContext(locale,…).
	// It overrides the env's default locale for i18n calls made within this test.
	locale string
	// contextOverrides accumulates the overrides established by t.setContext()
	// calls so they can be propagated to child sub-tests created by t.test().
	contextOverrides map[*ir.Context]any
}

// componentValue wraps the test environment so that c.field accesses
// resolve to component state.
type componentValue struct {
	Env        *interp.Env
	Vars       map[string]any
	Consts     map[string]any
	Funcs      map[string]*ir.Func
	compName   string // e.g. "pricing"; used to look up receiver-qualified methods
	testParams map[string]bool
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
