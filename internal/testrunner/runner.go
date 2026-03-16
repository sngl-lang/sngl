package testrunner

import (
	"fmt"
	"time"

	"git.duckfam.us/jonathan/sngl/ast"
)

// Result holds the outcome of a single test.
type Result struct {
	Component string
	Desc      string
	Passed    bool
	Error     string
	Children  []*Result
	Duration  time.Duration
}

// Run executes all tests in a document and returns results.
func Run(doc *ast.Document) ([]*Result, error) {
	var results []*Result
	for _, td := range doc.Tests {
		r := runTest(doc, td)
		results = append(results, r)
	}
	return results, nil
}

func runTest(doc *ast.Document, td *ast.TestDef) *Result {
	start := time.Now()
	r := &Result{
		Component: td.Component,
		Desc:      td.Desc,
	}

	env, err := buildEnv(doc, td.Component)
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

func runSubtest(parent *Env, td *ast.TestDef) *Result {
	start := time.Now()
	r := &Result{
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

func buildEnv(doc *ast.Document, compName string) (*Env, error) {
	env := NewEnv()

	var data []*ast.Data
	var computeds []*ast.Computed
	var consts []*ast.Const
	var params []*ast.Param

	if compName == "main" {
		data = doc.Data
		computeds = doc.Computeds
		consts = doc.Consts
	} else {
		comp := findComponent(doc, compName)
		if comp == nil {
			return nil, fmt.Errorf("component %q not found", compName)
		}
		data = comp.Data
		computeds = comp.Computeds
		consts = comp.Consts
		params = comp.Params
	}

	for _, d := range data {
		env.vars[d.Name] = evalInit(d.Init)
	}
	for _, c := range computeds {
		env.computeds[c.Name] = c.Expr
	}
	for _, c := range consts {
		env.consts[c.Name] = evalInit(c.Init)
	}
	for _, p := range params {
		env.vars[p.Name] = evalInit(p.Default)
	}

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
func evalInit(expr ast.Expr) any {
	if expr.Literal != nil {
		return expr.Literal
	}
	if expr.SNGL != nil {
		if lit, ok := expr.SNGL.(*ast.LiteralExpr); ok {
			return lit.Value
		}
	}
	return nil
}

func allPassed(results []*Result) bool {
	for _, r := range results {
		if !r.Passed {
			return false
		}
	}
	return true
}
