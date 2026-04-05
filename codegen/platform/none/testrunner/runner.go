package testrunner

import (
	"fmt"
	"time"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/internal/checker"
)

// Run executes all test functions in a document and returns results.
func Run(doc *ast.Document) ([]*codegen.TestResult, error) {
	var results []*codegen.TestResult
	for _, fn := range doc.TestFuncs() {
		r := runTestFunc(doc, fn)
		results = append(results, r)
	}
	return results, nil
}

func runTestFunc(doc *ast.Document, fn *ast.FuncDef) *codegen.TestResult {
	start := time.Now()
	r := &codegen.TestResult{
		Desc: fn.Name,
	}

	// Determine component name from second parameter type (if present).
	var compName string
	if len(fn.Params) >= 2 {
		compName = fn.Params[1].Type
		r.Component = compName
	}

	// Build environment with document-level and optional component state.
	env, err := BuildEnv(doc, compName)
	if err != nil {
		r.Error = err.Error()
		r.Duration = time.Since(start)
		return r
	}

	// Build set of test param names to avoid syncing collisions
	testParams := map[string]bool{}
	for _, p := range fn.Params {
		testParams[p.Name] = true
	}

	// If there's a component param, create a componentValue BEFORE binding
	// test params, so component state (vars/consts/funcs) is captured before
	// param names like "t" and "c" overwrite env.vars.
	var cVal *componentValue
	if compName != "" && len(fn.Params) >= 2 {
		cVal = &componentValue{
			env:        env,
			vars:       make(map[string]any),
			consts:     make(map[string]any),
			funcs:      make(map[string]*ast.FuncDef),
			testParams: testParams,
		}
		for k, v := range env.vars {
			cVal.vars[k] = v
		}
		for k, v := range env.consts {
			cVal.consts[k] = v
		}
		for k, v := range env.funcs {
			cVal.funcs[k] = v
		}
		env.vars[fn.Params[1].Name] = cVal
	}

	// Create the testing T value and bind it to the first param name.
	tVal := &testingT{env: env, result: r, doc: doc, compName: compName}
	tParamName := "t"
	if len(fn.Params) >= 1 {
		tParamName = fn.Params[0].Name
	}
	env.vars[tParamName] = tVal

	// Execute the function block.
	if fn.Block != nil {
		for _, stmt := range fn.Block.Stmts {
			if err := env.Exec(stmt); err != nil {
				r.Error = err.Error()
				r.Duration = time.Since(start)
				return r
			}
			// After each statement, sync env changes back to component
			// (event handlers and method calls may modify env.vars directly)
			if cVal != nil {
				cVal.syncFromEnv(testParams)
			}
		}
	}

	r.Log = env.Log
	r.Passed = r.Error == "" && allPassed(r.Children)
	r.Duration = time.Since(start)
	return r
}

// testingT is the runtime value for the T parameter in test functions.
// It provides assert(), tick(), and test() methods.
type testingT struct {
	env      *Env
	result   *codegen.TestResult
	doc      *ast.Document
	compName string
}

// componentValue wraps the test environment so that c.field accesses
// resolve to component state (data, consts, computed).
// It maintains its own vars map so component fields don't collide
// with test parameter names (e.g., a component field named "c" won't
// collide with the component param "c").
type componentValue struct {
	env        *Env
	vars       map[string]any         // component data/param vars
	consts     map[string]any         // component consts
	funcs      map[string]*ast.FuncDef // component functions
	testParams map[string]bool        // test param names to avoid syncing
}

// BuildEnv creates an Env for a test function.
// If compName is empty, only document-level functions/consts/units are loaded.
// If compName is "main", document-level data/consts/funcs are loaded.
// Otherwise, the named component's state is loaded.
func BuildEnv(doc *ast.Document, compName string) (*Env, error) {
	env := NewEnv()

	// Build unit tables from document-level unit definitions and stdlib.
	env.units = buildUnitTables(doc)

	// Load stdlib functions first.
	if _, _, _, stdlibFuncs, _, _, err := checker.LoadStdlib(); err == nil {
		for _, fn := range stdlibFuncs {
			env.SetFunc(fn)
		}
	}

	if compName == "" {
		// Standalone test — only document-level functions and consts.
		for _, fn := range doc.Functions {
			if !fn.IsTest() {
				env.SetFunc(fn)
			}
		}
		for _, c := range doc.Consts {
			env.consts[c.Name] = evalInit(env, c.Init)
		}
		env.doc = doc
		return env, nil
	}

	var data []*ast.Data
	var consts []*ast.Const
	var funcs []*ast.FuncDef
	var params []*ast.Param
	var body []*ast.VisualNode

	if compName == "main" {
		data = doc.Data
		consts = doc.Consts
		funcs = doc.Functions
		if doc.App != nil {
			body = doc.App.Children
		}
	} else {
		comp := doc.FindComponent(compName)
		if comp == nil {
			return nil, fmt.Errorf("component %q not found", compName)
		}
		data = comp.Data
		consts = comp.Consts
		funcs = comp.Functions
		params = comp.Params
		body = comp.Body
	}

	for _, d := range data {
		env.vars[d.Name] = evalInit(env, d.Init)
	}
	for _, c := range consts {
		env.consts[c.Name] = evalInit(env, c.Init)
	}
	for _, p := range params {
		env.vars[p.Name] = evalInit(env, p.Default)
	}
	for _, fn := range funcs {
		env.SetFunc(fn)
	}

	env.doc = doc
	env.body = body
	if compName == "main" {
		env.timers = doc.Timers
	}

	return env, nil
}

// evalInit extracts the initial value from an Expr.
func evalInit(env *Env, expr ast.Expr) any {
	if expr.Literal != nil {
		return expr.Literal
	}
	if expr.SNGL != nil {
		if lit, ok := expr.SNGL.(*ast.LiteralExpr); ok {
			return lit.Value
		}
		v, err := env.Eval(expr.SNGL)
		if err == nil {
			return v
		}
	}
	return nil
}

// buildUnitTables creates a suffix→UnitTable lookup from document and stdlib units.
func buildUnitTables(doc *ast.Document) map[string]*ast.UnitTable {
	tables := map[string]*ast.UnitTable{}

	_, _, stdlibUnits, _, _, _, err := checker.LoadStdlib()
	if err == nil {
		for _, u := range stdlibUnits {
			t := ast.BuildUnitTable(u)
			for suffix := range t.Conversions {
				tables[suffix] = t
			}
		}
	}

	for _, u := range doc.Units {
		t := ast.BuildUnitTable(u)
		for suffix := range t.Conversions {
			tables[suffix] = t
		}
	}
	return tables
}

func allPassed(results []*codegen.TestResult) bool {
	for _, r := range results {
		if !r.Passed {
			return false
		}
	}
	return true
}
