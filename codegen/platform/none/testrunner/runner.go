package testrunner

import (
	"fmt"
	"time"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/internal/checker"
)

// Run executes all tests in a document and returns results.
func Run(doc *ast.Document) ([]*codegen.TestResult, error) {
	var results []*codegen.TestResult
	for _, td := range doc.Tests {
		r := runTest(doc, td)
		results = append(results, r)
	}
	return results, nil
}

func runTest(doc *ast.Document, td *ast.TestDef) *codegen.TestResult {
	start := time.Now()
	r := &codegen.TestResult{
		Component: td.Component,
		Desc:      td.Desc,
	}

	env, err := BuildEnv(doc, td.Component)
	if err != nil {
		r.Error = err.Error()
		r.Duration = time.Since(start)
		return r
	}

	// Execute body
	for _, stmt := range td.Body {
		if err := env.Exec(stmt); err != nil {
			r.Error = err.Error()
			r.Duration = time.Since(start)
			return r
		}
	}

	// Run subtests
	for _, sub := range td.Subtests {
		child := runSubtest(env, sub)
		r.Children = append(r.Children, child)
	}

	r.Passed = r.Error == "" && allPassed(r.Children)
	r.Duration = time.Since(start)
	return r
}

func runSubtest(parent *Env, td *ast.TestDef) *codegen.TestResult {
	start := time.Now()
	r := &codegen.TestResult{
		Desc: td.Desc,
	}

	env := parent.Snapshot()

	for _, stmt := range td.Body {
		if err := env.Exec(stmt); err != nil {
			r.Error = err.Error()
			r.Duration = time.Since(start)
			return r
		}
	}

	for _, sub := range td.Subtests {
		child := runSubtest(env, sub)
		r.Children = append(r.Children, child)
	}

	r.Passed = r.Error == "" && allPassed(r.Children)
	r.Duration = time.Since(start)
	return r
}

// BuildEnv creates an Env for a component with initial state from data, computeds, and params.
func BuildEnv(doc *ast.Document, compName string) (*Env, error) {
	env := NewEnv()

	var data []*ast.Data
	var computeds []*ast.Computed
	var consts []*ast.Const
	var funcs []*ast.FuncDef
	var params []*ast.Param
	var body []*ast.VisualNode

	if compName == "main" {
		data = doc.Data
		computeds = doc.Computeds
		consts = doc.Consts
		funcs = doc.Functions
		if doc.App != nil {
			body = doc.App.Children
		}
	} else {
		comp := findComponent(doc, compName)
		if comp == nil {
			return nil, fmt.Errorf("component %q not found", compName)
		}
		data = comp.Data
		computeds = comp.Computeds
		consts = comp.Consts
		funcs = comp.Functions
		params = comp.Params
		body = comp.Body
	}

	// Build unit tables from document-level unit definitions and stdlib.
	env.units = buildUnitTables(doc)

	for _, d := range data {
		env.vars[d.Name] = evalInit(env, d.Init)
	}
	for _, c := range computeds {
		env.computeds[c.Name] = c.Expr
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

	return env, nil
}

func findComponent(doc *ast.Document, name string) *ast.Component {
	for _, c := range doc.Components {
		if c.Name == name {
			return c
		}
	}
	return nil
}

// evalInit extracts the initial value from an Expr.
// For SNGL expressions beyond simple literals (lists, structs, etc.),
// it falls back to Env.Eval().
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

	// Load stdlib unit definitions.
	_, _, stdlibUnits, err := checker.LoadStdlib()
	if err == nil {
		for _, u := range stdlibUnits {
			t := ast.BuildUnitTable(u)
			for suffix := range t.Conversions {
				tables[suffix] = t
			}
		}
	}

	// Document-level unit definitions (override stdlib if same name).
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
