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
		comp  *ast.Component
		tests []*ast.FuncDef
	}
	groups := map[string]*compTests{}
	// Detect which components have list-typed fields (not compilable to Go)
	listComps := map[string]bool{}
	for _, c := range doc.AllComponents() {
		for _, d := range c.Data {
			if inferGoType(d.Init) == "any" {
				listComps[c.Name] = true
			}
		}
	}

	for _, fn := range doc.TestFuncs() {
		compName := ""
		if len(fn.Params) >= 2 {
			compName = fn.Params[1].Type
		}
		if ShouldSkipTestFunc(fn) || listComps[compName] {
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
			if fn.Block != nil && slices.ContainsFunc(fn.Block.Stmts, nodeNeedsFmt) {
				needsFmt = true
			}
		}
		for _, fn := range g.comp.Functions {
			if fn.Body.SNGL != nil && len(fn.Params) == 0 && !fn.IsStdlib && nodeNeedsFmt(fn.Body.SNGL) {
				needsFmt = true
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

func nodeNeedsFmt(n ast.Node) bool {
	switch e := n.(type) {
	case *ast.InterpolationExpr:
		return true
	case *ast.CallStmt:
		return nodeNeedsFmt(e.Call)
	case *ast.CallExpr:
		if slices.ContainsFunc(e.Args, nodeNeedsFmt) {
			return true
		}
		if e.Func == "string" {
			return true
		}
	case *ast.BinaryExpr:
		return nodeNeedsFmt(e.Left) || nodeNeedsFmt(e.Right)
	case *ast.UnaryExpr:
		return nodeNeedsFmt(e.Operand)
	case *ast.TernaryExpr:
		return nodeNeedsFmt(e.Cond) || nodeNeedsFmt(e.Then) || nodeNeedsFmt(e.Else)
	case *ast.AssignStmt:
		return nodeNeedsFmt(e.Value)
	case *ast.ParenExpr:
		return nodeNeedsFmt(e.Inner)
	}
	return false
}

func emitTestHelpers(b *strings.Builder) {
	b.WriteString("func ternary[T any](cond bool, a, b T) T {\n")
	b.WriteString("\tif cond {\n\t\treturn a\n\t}\n\treturn b\n}\n\n")
}

// emitTestModel emits a simple struct, constructor, and computed methods for a component.
func emitTestModel(b *strings.Builder, comp *ast.Component, doc *ast.Document) {
	name := comp.Name

	// Build field info
	modelFields := map[string]bool{}
	computedFields := map[string]bool{}
	structNames := map[string][]string{}

	for _, d := range comp.Data {
		modelFields[d.Name] = true
	}
	for _, fn := range comp.Functions {
		if fn.Body.SNGL != nil && len(fn.Params) == 0 && !fn.IsStdlib {
			modelFields[fn.Name] = true
			computedFields[fn.Name] = true
		}
	}
	for _, p := range comp.Params {
		modelFields[p.Name] = true
	}
	for _, sd := range doc.Structs {
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
	for _, d := range comp.Data {
		goType := inferGoType(d.Init)
		fmt.Fprintf(b, "\t%s %s\n", d.Name, goType)
	}
	for _, p := range comp.Params {
		goType := inferGoType(p.Default)
		fmt.Fprintf(b, "\t%s %s\n", p.Name, goType)
	}
	b.WriteString("}\n\n")

	// Constructor
	fmt.Fprintf(b, "func new%s() %sModel {\n", exportName(name), name)
	fmt.Fprintf(b, "\treturn %sModel{\n", name)
	for _, d := range comp.Data {
		fmt.Fprintf(b, "\t\t%s: %s,\n", d.Name, literalToGo(d.Init))
	}
	for _, p := range comp.Params {
		fmt.Fprintf(b, "\t\t%s: %s,\n", p.Name, literalToGo(p.Default))
	}
	b.WriteString("\t}\n}\n\n")

	// Computed methods (zero-arg expression-form functions)
	for _, fn := range comp.Functions {
		if fn.Body.SNGL == nil || len(fn.Params) != 0 || fn.IsStdlib {
			continue
		}
		goType := inferGoType(fn.Body)
		if goType == "any" && fn.Body.SNGL != nil {
			goType = snglNodeGoType(fn.Body.SNGL)
		}
		body := ""
		if fn.Body.SNGL != nil {
			body = ec.TranslateExpr(fn.Body.SNGL)
		} else if fn.Body.Literal != nil {
			body = literalToGo(fn.Body)
		}
		fmt.Fprintf(b, "func (m %sModel) %s() %s {\n", name, fn.Name, goType)
		fmt.Fprintf(b, "\treturn %s\n", body)
		b.WriteString("}\n\n")
	}
}

// emitTestFuncGo emits a Go test function for a single SNGL test function.
func emitTestFuncGo(b *strings.Builder, fn *ast.FuncDef, comp *ast.Component) {
	funcName := "Test" + exportName(comp.Name) + "_" + sanitizeTestName(fn.Name)

	modelFields := map[string]bool{}
	computedFields := map[string]bool{}
	for _, d := range comp.Data {
		modelFields[d.Name] = true
	}
	for _, cfn := range comp.Functions {
		if cfn.Body.SNGL != nil && len(cfn.Params) == 0 && !cfn.IsStdlib {
			modelFields[cfn.Name] = true
			computedFields[cfn.Name] = true
		}
	}
	for _, p := range comp.Params {
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

	if fn.Block != nil {
		emitTestBody(b, fn.Block.Stmts, ec, 1)
	}

	b.WriteString("}\n\n")
}

func emitTestBody(b *strings.Builder, stmts []ast.Node, ec *exprContext, depth int) {
	indent := strings.Repeat("\t", depth)
	for _, stmt := range stmts {
		switch s := stmt.(type) {
		case *ast.CallStmt:
			if s.Call.Func == "assert" && len(s.Call.Args) == 1 {
				expr := ec.TranslateExpr(s.Call.Args[0])
				original := parser.FormatNode(s.Call.Args[0])
				fmt.Fprintf(b, "%sif !(%s) {\n", indent, expr)
				fmt.Fprintf(b, "%s\tt.Fatalf(\"assert(%s) failed\")\n", indent, escapeFmt(original))
				fmt.Fprintf(b, "%s}\n", indent)
				continue
			}
			stmts := ec.TranslateMutation(s)
			for _, line := range stmts {
				fmt.Fprintf(b, "%s%s\n", indent, line)
			}
			continue
		case *ast.CallExpr:
			if s.Func == "assert" && len(s.Args) == 1 {
				expr := ec.TranslateExpr(s.Args[0])
				original := parser.FormatNode(s.Args[0])
				fmt.Fprintf(b, "%sif !(%s) {\n", indent, expr)
				fmt.Fprintf(b, "%s\tt.Fatalf(\"assert(%s) failed\")\n", indent, escapeFmt(original))
				fmt.Fprintf(b, "%s}\n", indent)
				continue
			}
		case *ast.AssignStmt:
			stmts := ec.TranslateMutation(s)
			for _, line := range stmts {
				fmt.Fprintf(b, "%s%s\n", indent, line)
			}
			continue
		case *ast.ToggleStmt:
			stmts := ec.TranslateMutation(s)
			for _, line := range stmts {
				fmt.Fprintf(b, "%s%s\n", indent, line)
			}
			continue
		case *ast.MethodExpr:
			stmts := ec.TranslateMutation(s)
			for _, line := range stmts {
				fmt.Fprintf(b, "%s%s\n", indent, line)
			}
			continue
		}
		// Fallback: expression statement
		fmt.Fprintf(b, "%s_ = %s\n", indent, ec.TranslateExpr(stmt))
	}
}

// ShouldSkipTestFunc returns true if a test function uses features not compilable to Go.
// Currently skips ALL func-based tests — the bubbletea codegen needs updating
// to handle the new t.assert(c.field) syntax.
func ShouldSkipTestFunc(fn *ast.FuncDef) bool {
	return true // TODO: update bubbletea codegen for func-based tests
}

func findComp(doc *ast.Document, name string) *ast.Component {
	return doc.FindComponent(name)
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
