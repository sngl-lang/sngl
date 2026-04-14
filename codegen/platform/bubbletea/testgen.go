package bubbletea

import (
	"fmt"
	"go/format"
	"slices"
	"strings"
	"unicode"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/internal/parser"
)

// CompileTests generates a Go test file from SNGL test definitions.
// Tests that use DOM access (root, _find, @event triggering) are skipped.
// Tests with ERROR(test) directives are skipped.
func CompileTests(doc *ast.Document, cfg Config) ([]byte, error) {
	cfg = cfg.withDefaults()

	// Group tests by component
	type compTests struct {
		comp  *ast.ComponentDecl
		tests []*ast.FuncDef
	}
	groups := map[string]*compTests{}

	for _, fn := range doc.TestFuncs() {
		fnParams := fn.Params.Params
		compName := ""
		if len(fnParams) >= 2 {
			compName = exprTypeHint(fnParams[1].Type)
		}
		if ShouldSkipTestFunc(fn) {
			continue
		}
		g, ok := groups[compName]
		if !ok {
			comp := findComp(doc, compName)
			if comp == nil {
				continue
			}
			g = &compTests{comp: comp}
			groups[compName] = g
		}
		g.tests = append(g.tests, fn)
	}

	if len(groups) == 0 {
		return nil, nil
	}

	var b strings.Builder
	// Detect if fmt is needed (string interpolation or string() in computeds)
	needsFmt := false
	for _, g := range groups {
		for _, fn := range g.tests {
			if len(fn.Block.Stmts) > 0 && slices.ContainsFunc(fn.Block.Stmts, nodeNeedsFmt) {
				needsFmt = true
			}
		}
		// Check computed functions in the component body
		for _, stmt := range g.comp.Body.Stmts {
			if fn, ok := stmt.(*ast.FuncDef); ok {
				if isComputed(fn) && fn.Body != nil && nodeNeedsFmtExpr(fn.Body) {
					needsFmt = true
				}
			}
		}
	}

	fmt.Fprintf(&b, "package %s\n\n", cfg.Package)
	if needsFmt {
		b.WriteString("import (\n\t\"fmt\"\n\t\"testing\"\n)\n\n")
	} else {
		b.WriteString("import \"testing\"\n\n")
	}
	emitTestHelpers(&b)

	for _, g := range groups {
		emitTestModel(&b, g.comp, doc)
		for _, fn := range g.tests {
			emitTestFuncGo(&b, fn, g.comp)
		}
	}

	src := []byte(b.String())
	formatted, err := format.Source(src)
	if err != nil {
		return src, fmt.Errorf("generated test formatting error: %w\n%s", err, src)
	}
	return formatted, nil
}

func nodeNeedsFmt(n ast.Stmt) bool {
	switch e := n.(type) {
	case *ast.CallStmt:
		if e.Call != nil {
			return nodeNeedsFmtExpr(e.Call)
		}
	case *ast.AssignStmt:
		return nodeNeedsFmtExpr(e.Value)
	}
	return false
}

func nodeNeedsFmtExpr(e ast.Expr) bool {
	if e == nil {
		return false
	}
	switch x := e.(type) {
	case *ast.InterpolationExpr:
		return true
	case *ast.CallExpr:
		if callFuncName(x) == "string" {
			return true
		}
		for _, a := range x.Args.Args {
			if arg, ok := a.(ast.Arg); ok {
				if nodeNeedsFmtExpr(arg.Value) {
					return true
				}
			}
		}
	case *ast.BinaryExpr:
		return nodeNeedsFmtExpr(x.Left) || nodeNeedsFmtExpr(x.Right)
	case *ast.UnaryExpr:
		return nodeNeedsFmtExpr(x.Operand)
	case *ast.TernaryExpr:
		return nodeNeedsFmtExpr(x.Cond) || nodeNeedsFmtExpr(x.Then) || nodeNeedsFmtExpr(x.Else)
	case *ast.ParenExpr:
		return nodeNeedsFmtExpr(x.Inner)
	}
	return false
}

func emitTestHelpers(b *strings.Builder) {
	b.WriteString("func ternary[T any](cond bool, a, b T) T {\n")
	b.WriteString("\tif cond {\n\t\treturn a\n\t}\n\treturn b\n}\n\n")
}

// emitTestModel emits a simple struct, constructor, and computed methods for a component.
func emitTestModel(b *strings.Builder, comp *ast.ComponentDecl, doc *ast.Document) {
	name := comp.Name

	// Build field info
	modelFields := map[string]bool{}
	computedFields := map[string]bool{}
	structNames := map[string][]string{}

	// Component body may contain VarDecl and FuncDef statements
	type dataField struct {
		name string
		init ast.Expr
	}
	var dataFields []dataField
	var computedFuncs []*ast.FuncDef

	for _, stmt := range comp.Body.Stmts {
		switch s := stmt.(type) {
		case *ast.VarDecl:
			for _, spec := range s.Specs {
				for _, n := range spec.Names {
					modelFields[n] = true
					dataFields = append(dataFields, dataField{name: n, init: spec.Default})
				}
			}
		case *ast.FuncDef:
			if isComputed(s) {
				modelFields[s.Name] = true
				computedFields[s.Name] = true
				computedFuncs = append(computedFuncs, s)
			}
		}
	}
	for _, p := range compParams(comp) {
		modelFields[p.Name] = true
		dataFields = append(dataFields, dataField{name: p.Name, init: p.Default})
	}
	for _, sd := range docStructDefs(doc) {
		var fields []string
		for _, f := range sd.Fields {
			fields = append(fields, f.Name)
		}
		structNames[sd.Name] = fields
	}

	ec := &exprContext{
		ModelFields:    modelFields,
		ComputedFields: computedFields,
		LocalVars:      map[string]bool{},
		StructNames:    structNames,
	}

	// Struct
	fmt.Fprintf(b, "type %sModel struct {\n", name)
	for _, d := range dataFields {
		if computedFields[d.name] {
			continue
		}
		goType := inferGoType(d.init)
		fmt.Fprintf(b, "\t%s %s\n", d.name, goType)
	}
	b.WriteString("}\n\n")

	// Constructor
	fmt.Fprintf(b, "func new%s() %sModel {\n", exportName(name), name)
	fmt.Fprintf(b, "\treturn %sModel{\n", name)
	for _, d := range dataFields {
		if computedFields[d.name] {
			continue
		}
		fmt.Fprintf(b, "\t\t%s: %s,\n", d.name, literalToGo(d.init))
	}
	b.WriteString("\t}\n}\n\n")

	// Computed methods (zero-arg expression-form functions)
	for _, fn := range computedFuncs {
		goType := inferGoType(fn.Body)
		if goType == "any" && fn.Body != nil {
			goType = snglNodeGoType(fn.Body)
		}
		body := ""
		if fn.Body != nil {
			body = ec.TranslateExpr(fn.Body)
		}
		fmt.Fprintf(b, "func (m %sModel) %s() %s {\n", name, fn.Name, goType)
		fmt.Fprintf(b, "\treturn %s\n", body)
		b.WriteString("}\n\n")
	}
}

// emitTestFuncGo emits a Go test function for a single SNGL test function.
func emitTestFuncGo(b *strings.Builder, fn *ast.FuncDef, comp *ast.ComponentDecl) {
	funcName := "Test" + exportName(comp.Name) + "_" + sanitizeTestName(fn.Name)

	modelFields := map[string]bool{}
	computedFields := map[string]bool{}
	for _, stmt := range comp.Body.Stmts {
		switch s := stmt.(type) {
		case *ast.VarDecl:
			for _, spec := range s.Specs {
				for _, n := range spec.Names {
					modelFields[n] = true
				}
			}
		case *ast.FuncDef:
			if isComputed(s) {
				modelFields[s.Name] = true
				computedFields[s.Name] = true
			}
		}
	}
	for _, p := range compParams(comp) {
		modelFields[p.Name] = true
	}

	ec := &exprContext{
		ModelFields:    modelFields,
		ComputedFields: computedFields,
		LocalVars:      map[string]bool{},
		StructNames:    map[string][]string{},
	}

	fmt.Fprintf(b, "func %s(t *testing.T) {\n", funcName)
	fmt.Fprintf(b, "\tm := new%s()\n", exportName(comp.Name))
	fmt.Fprintf(b, "\t_ = m\n")

	if len(fn.Block.Stmts) > 0 {
		emitTestBody(b, fn.Block.Stmts, ec, 1)
	}

	b.WriteString("}\n\n")
}

func emitTestBody(b *strings.Builder, stmts []ast.Stmt, ec *exprContext, depth int) {
	indent := strings.Repeat("\t", depth)
	for _, stmt := range stmts {
		switch s := stmt.(type) {
		case *ast.CallStmt:
			if s.Call != nil && callFuncName(s.Call) == "assert" {
				args := callArgs(s.Call)
				if len(args) == 1 {
					expr := ec.TranslateExpr(args[0])
					original := parser.FormatExpr(args[0])
					fmt.Fprintf(b, "%sif !(%s) {\n", indent, expr)
					fmt.Fprintf(b, "%s\tt.Fatalf(\"assert(%s) failed\")\n", indent, escapeFmt(original))
					fmt.Fprintf(b, "%s}\n", indent)
					continue
				}
			}
			translated := ec.TranslateMutation(s)
			for _, line := range translated {
				fmt.Fprintf(b, "%s%s\n", indent, line)
			}
			continue
		case *ast.AssignStmt:
			translated := ec.TranslateMutation(s)
			for _, line := range translated {
				fmt.Fprintf(b, "%s%s\n", indent, line)
			}
			continue
		case *ast.ToggleStmt:
			translated := ec.TranslateMutation(s)
			for _, line := range translated {
				fmt.Fprintf(b, "%s%s\n", indent, line)
			}
			continue
		default:
			// Try to translate as mutation
			translated := ec.TranslateMutation(s)
			for _, line := range translated {
				fmt.Fprintf(b, "%s%s\n", indent, line)
			}
		}
	}
}

// ShouldSkipTestFunc returns true if a test function uses features not compilable to Go.
// Currently skips ALL func-based tests — the bubbletea codegen needs updating
// to handle the new t.assert(c.field) syntax.
func ShouldSkipTestFunc(fn *ast.FuncDef) bool {
	return true // TODO: update bubbletea codegen for func-based tests
}

func findComp(doc *ast.Document, name string) *ast.ComponentDecl {
	return findComponentInDoc(doc, name)
}

func sanitizeTestName(desc string) string {
	var b strings.Builder
	capitalize := true
	for _, r := range desc {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			if capitalize {
				b.WriteRune(unicode.ToUpper(r))
				capitalize = false
			} else {
				b.WriteRune(r)
			}
		} else {
			capitalize = true
		}
	}
	s := b.String()
	if s == "" {
		return "Default"
	}
	return s
}

func escapeFmt(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "\"", "\\\"")
	s = strings.ReplaceAll(s, "%", "%%")
	return s
}
