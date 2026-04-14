package testrunner

import (
	"fmt"
	"maps"
	"strconv"
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
	fnParams := fn.Params.Params
	if len(fnParams) >= 2 {
		compName = codegen.ExprTypeHint(fnParams[1].Type)
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
	for _, p := range fnParams {
		testParams[p.Name] = true
	}

	// If there's a component param, create a componentValue BEFORE binding
	// test params, so component state (vars/consts/funcs) is captured before
	// param names like "t" and "c" overwrite env.vars.
	var cVal *componentValue
	if compName != "" && len(fnParams) >= 2 {
		cVal = &componentValue{
			env:        env,
			vars:       make(map[string]any),
			consts:     make(map[string]any),
			funcs:      make(map[string]*ast.FuncDef),
			testParams: testParams,
		}
		maps.Copy(cVal.vars, env.vars)
		maps.Copy(cVal.consts, env.consts)
		maps.Copy(cVal.funcs, env.funcs)
		env.vars[fnParams[1].Name] = cVal
	}

	// Create the testing T value and bind it to the first param name.
	tVal := &testingT{env: env, result: r, doc: doc, compName: compName}
	tParamName := "t"
	if len(fnParams) >= 1 {
		tParamName = fnParams[0].Name
	}
	env.vars[tParamName] = tVal

	// Execute the function block.
	if fn.Block.IsDefined() {
		for _, stmt := range fn.Block.Stmts {
			if err := env.Exec(stmt); err != nil {
				r.Error = err.Error()
				r.Duration = time.Since(start)
				return r
			}
			// After each statement, sync env changes back to component
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
type testingT struct {
	env      *Env
	result   *codegen.TestResult
	doc      *ast.Document
	compName string
}

// componentValue wraps the test environment so that c.field accesses
// resolve to component state (data, consts, computed).
type componentValue struct {
	env        *Env
	vars       map[string]any          // component data/param vars
	consts     map[string]any          // component consts
	funcs      map[string]*ast.FuncDef // component functions
	testParams map[string]bool         // test param names to avoid syncing
}

// BuildEnv creates an Env for a test function.
// If compName is empty, only document-level functions/consts/units are loaded.
// If compName is "main", document-level data/consts/funcs are loaded.
// Otherwise, the named component's state is loaded.
func BuildEnv(doc *ast.Document, compName string) (*Env, error) {
	env := NewEnv()

	// Build unit tables from document-level unit definitions.
	env.units = buildUnitTables(doc)

	// Load stdlib functions.
	for _, stdDoc := range checker.StdlibDocs() {
		for _, fn := range codegen.DocFuncDefs(stdDoc) {
			env.SetFunc(fn)
		}
	}

	if compName == "" {
		// Standalone test — only document-level functions and consts.
		for _, fn := range codegen.DocFuncDefs(doc) {
			if !fn.IsTest() {
				env.SetFunc(fn)
			}
		}
		for _, cd := range codegen.DocConstDecls(doc) {
			for _, spec := range cd.Specs {
				val := evalInit(env, spec.Default)
				for _, name := range spec.Names {
					env.consts[name] = val
				}
			}
		}
		env.doc = doc
		return env, nil
	}

	var varDecls []*ast.VarDecl
	var constDecls []*ast.ConstDecl
	var funcs []*ast.FuncDef
	var params []ast.Param
	var bodyStmts []ast.Stmt

	comp := findComponent(doc, compName)
	if comp == nil {
		// Fallback for "main": try document-level declarations
		if compName == "main" {
			varDecls = codegen.DocVarDecls(doc)
			constDecls = codegen.DocConstDecls(doc)
			funcs = codegen.DocFuncDefs(doc)
			for _, s := range doc.Stmts {
				switch s.(type) {
				case *ast.VisualNode, *ast.CallStmt, *ast.IfStmt, *ast.ForStmt:
					bodyStmts = append(bodyStmts, s)
				}
			}
		} else {
			return nil, fmt.Errorf("component %q not found", compName)
		}
	} else {
		varDecls, constDecls, funcs = compDeclsFromBody(comp)
		params = codegen.CompParams(comp)
		bodyStmts = compBodyStmts(comp)
	}

	for _, vd := range varDecls {
		for _, spec := range vd.Specs {
			val := evalInit(env, spec.Default)
			for _, name := range spec.Names {
				env.vars[name] = val
			}
		}
	}
	for _, cd := range constDecls {
		for _, spec := range cd.Specs {
			val := evalInit(env, spec.Default)
			for _, name := range spec.Names {
				env.consts[name] = val
			}
		}
	}
	for _, p := range params {
		env.vars[p.Name] = evalInit(env, p.Default)
	}
	for _, fn := range funcs {
		env.SetFunc(fn)
	}

	env.doc = doc
	env.bodyStmts = bodyStmts

	return env, nil
}

// evalInit extracts the initial value from an Expr.
func evalInit(env *Env, expr ast.Expr) any {
	if expr == nil {
		return nil
	}
	if ul, ok := expr.(*ast.UnitLiteral); ok {
		v, err := env.evalUnitLiteral(ul)
		if err == nil {
			return v
		}
	}
	if lit, ok := expr.(*ast.LiteralExpr); ok {
		v, _ := env.evalLiteral(lit)
		return v
	}
	v, err := env.Eval(expr)
	if err == nil {
		return v
	}
	return nil
}

// buildUnitTables creates a suffix->unitTable lookup from document and stdlib units.
func buildUnitTables(doc *ast.Document) map[string]*unitTable {
	tables := map[string]*unitTable{}

	for _, stdDoc := range checker.StdlibDocs() {
		for _, u := range codegen.DocUnitDefs(stdDoc) {
			t := buildUnitTableFromDef(u)
			for suffix := range t.Conversions {
				tables[suffix] = t
			}
		}
	}

	for _, u := range codegen.DocUnitDefs(doc) {
		t := buildUnitTableFromDef(u)
		for suffix := range t.Conversions {
			tables[suffix] = t
		}
	}
	return tables
}

// buildUnitTableFromDef creates a unitTable from a UnitDef AST node.
// Resolves recursive factor references like { ms, s = 1000ms, m = 60s, h = 60m }.
func buildUnitTableFromDef(u *ast.UnitDef) *unitTable {
	t := &unitTable{
		Conversions: make(map[string]float64),
	}

	// First pass: collect raw factors with their reference suffix.
	type rawFactor struct {
		amount float64
		ref    string // suffix this references (e.g., "ms" in "1000ms")
	}
	rawFactors := make(map[string]*rawFactor)

	if len(u.Suffixes) > 0 {
		t.Base = u.Suffixes[0].Name
	}

	for _, s := range u.Suffixes {
		if s.Factor == nil {
			// Base unit or independent suffix — factor is 1
			t.Conversions[s.Name] = 1.0
			continue
		}

		// Factor is a unit literal like "1000ms" or "60s"
		if ul, ok := s.Factor.(*ast.UnitLiteral); ok {
			num, err := parseUnitNumber(ul.Raw, ul.Suffix)
			if err != nil {
				t.Conversions[s.Name] = 1.0
				continue
			}
			rawFactors[s.Name] = &rawFactor{amount: num, ref: ul.Suffix}
			continue
		}

		// Factor is a plain number literal
		if lit, ok := s.Factor.(*ast.LiteralExpr); ok {
			switch lit.Kind {
			case ast.LiteralInt:
				n, _ := strconv.Atoi(lit.Raw)
				t.Conversions[s.Name] = float64(n)
			case ast.LiteralFloat:
				f, _ := strconv.ParseFloat(lit.Raw, 64)
				t.Conversions[s.Name] = f
			default:
				t.Conversions[s.Name] = 1.0
			}
			continue
		}

		t.Conversions[s.Name] = 1.0
	}

	// Second pass: resolve recursive references.
	// E.g., s = 1000ms -> factor(s) = 1000 * factor(ms)
	//        m = 60s   -> factor(m) = 60 * factor(s) = 60 * 1000 = 60000
	var resolve func(name string) float64
	resolve = func(name string) float64 {
		if f, ok := t.Conversions[name]; ok {
			return f
		}
		rf, ok := rawFactors[name]
		if !ok {
			return 1.0
		}
		f := rf.amount * resolve(rf.ref)
		t.Conversions[name] = f
		return f
	}

	for name := range rawFactors {
		resolve(name)
	}

	return t
}

func allPassed(results []*codegen.TestResult) bool {
	for _, r := range results {
		if !r.Passed {
			return false
		}
	}
	return true
}

// --- Document/Component helpers ---

// findComponent looks up a named component in the document.
func findComponent(doc *ast.Document, name string) *ast.ComponentDecl {
	for _, c := range codegen.DocComponents(doc) {
		if c.Name == name {
			return c
		}
	}
	return nil
}

// compBodyStmts returns visual/control-flow statements from a component body.
// Includes VisualNode, CallStmt (elements without children), IfStmt, ForStmt.
func compBodyStmts(comp *ast.ComponentDecl) []ast.Stmt {
	var out []ast.Stmt
	for _, s := range comp.Body.Stmts {
		switch s.(type) {
		case *ast.VisualNode, *ast.CallStmt, *ast.IfStmt, *ast.ForStmt:
			out = append(out, s)
		}
	}
	return out
}

// compDeclsFromBody extracts var decls, const decls, and func defs from a component body.
func compDeclsFromBody(comp *ast.ComponentDecl) ([]*ast.VarDecl, []*ast.ConstDecl, []*ast.FuncDef) {
	var vars []*ast.VarDecl
	var consts []*ast.ConstDecl
	var funcs []*ast.FuncDef
	for _, s := range comp.Body.Stmts {
		switch n := s.(type) {
		case *ast.VarDecl:
			vars = append(vars, n)
		case *ast.ConstDecl:
			consts = append(consts, n)
		case *ast.FuncDef:
			funcs = append(funcs, n)
		}
	}
	return vars, consts, funcs
}
