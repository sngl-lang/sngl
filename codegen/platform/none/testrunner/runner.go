package testrunner

import (
	"fmt"
	"maps"
	"strings"
	"time"

	"git.duckfam.us/jonathan/sngl/codegen"
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

	env, err := BuildEnv(pkg, compName)
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
			env:        env,
			vars:       make(map[string]any),
			consts:     make(map[string]any),
			funcs:      make(map[string]*ir.Func),
			testParams: testParams,
		}
		maps.Copy(cVal.vars, env.vars)
		maps.Copy(cVal.consts, env.consts)
		maps.Copy(cVal.funcs, env.funcs)
		env.vars[fn.Params[1].Name] = cVal
	}

	tVal := &testingT{env: env, result: r, pkg: pkg, compName: compName}
	tParamName := "t"
	if len(fn.Params) >= 1 {
		tParamName = fn.Params[0].Name
	}
	env.vars[tParamName] = tVal

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
	env      *Env
	result   *codegen.TestResult
	pkg      *ir.Package
	compName string
}

// componentValue wraps the test environment so that c.field accesses
// resolve to component state.
type componentValue struct {
	env        *Env
	vars       map[string]any
	consts     map[string]any
	funcs      map[string]*ir.Func
	testParams map[string]bool
}

// BuildEnv creates an Env for a test function.
func BuildEnv(pkg *ir.Package, compName string) (*Env, error) {
	env := NewEnv()
	env.pkg = pkg
	env.units = buildUnitTables(pkg)

	// Register package-level functions (includes stdlib merged by the checker).
	for _, fn := range pkg.Funcs {
		if !fn.IsTest {
			env.SetFunc(fn)
		}
	}
	// Register type-attached stdlib methods (stored in the symbol table, not pkg.Funcs).
	if pkg.Symbols != nil {
		for typeName, methods := range pkg.Symbols.Methods {
			for _, fn := range methods {
				env.funcs[typeName+"."+fn.Name] = fn
			}
		}
	}

	if compName == "" {
		for _, c := range pkg.Consts {
			env.consts[c.Name] = evalInit(env, c.Init)
		}
		return env, nil
	}

	comp := findComponent(pkg, compName)
	if comp == nil {
		if compName == "main" {
			for _, v := range pkg.Vars {
				env.vars[v.Name] = evalInit(env, v.Init)
			}
			for _, c := range pkg.Consts {
				env.consts[c.Name] = evalInit(env, c.Init)
			}
			return env, nil
		}
		return nil, fmt.Errorf("component %q not found", compName)
	}

	for _, v := range comp.Vars {
		if v.IsConst {
			env.consts[v.Name] = evalInit(env, v.Init)
		} else {
			env.vars[v.Name] = evalInit(env, v.Init)
		}
	}
	for _, p := range comp.Props {
		env.vars[p.Name] = evalInit(env, p.Default)
	}
	for _, fn := range comp.Funcs {
		env.SetFunc(fn)
	}
	env.comp = comp
	env.bodyStmts = comp.Body
	return env, nil
}

func evalInit(env *Env, expr ir.Expr) any {
	if expr == nil {
		return nil
	}
	v, err := env.Eval(expr)
	if err == nil {
		return v
	}
	return nil
}

func buildUnitTables(pkg *ir.Package) map[string]*unitTable {
	tables := map[string]*unitTable{}
	add := func(units []*ir.UnitDef) {
		for _, u := range units {
			t := buildUnitTableFromDef(u)
			for suffix := range t.Conversions {
				tables[suffix] = t
			}
		}
	}
	add(pkg.Units)
	// Stdlib and platform-injected units surface through Symbols.Types.
	if pkg.Symbols != nil {
		for _, sym := range pkg.Symbols.Types {
			if u, ok := sym.(*ir.UnitDef); ok {
				t := buildUnitTableFromDef(u)
				for suffix := range t.Conversions {
					if _, exists := tables[suffix]; !exists {
						tables[suffix] = t
					}
				}
			}
		}
	}
	return tables
}

func buildUnitTableFromDef(u *ir.UnitDef) *unitTable {
	t := &unitTable{Conversions: make(map[string]float64)}
	if len(u.Suffixes) > 0 {
		t.Base = u.Suffixes[0].Name
	}
	for _, s := range u.Suffixes {
		t.Conversions[s.Name] = s.Factor
	}
	return t
}

// errorLine returns the 1-based source line of the failing expression. For
// AssertError it uses the asserted expression's AST position; for other
// errors it falls back to the enclosing statement's position when known.
func errorLine(err error, stmt ir.Stmt) int {
	if ae, ok := err.(*AssertError); ok && ae != nil {
		if a := irASTOf(ae.Expr); a != nil {
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

func findComponent(pkg *ir.Package, name string) *ir.Component {
	for _, c := range pkg.Components {
		if c.Name == name {
			return c
		}
	}
	return nil
}
