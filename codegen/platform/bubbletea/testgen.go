package bubbletea

import (
	"fmt"
	"go/format"
	"strings"
	"unicode"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/internal/snglparser"
)

// CompileTests generates a Go test file from SNGL test definitions.
// Tests that use DOM access (root, _find, @event triggering) are skipped.
// Tests with ERROR(test) directives are skipped.
func CompileTests(doc *ast.Document, cfg Config) ([]byte, error) {
	cfg = cfg.withDefaults()

	// Group tests by component
	type compTests struct {
		comp  *ast.Component
		tests []*ast.TestDef
	}
	groups := map[string]*compTests{}
	// Detect which components have list-typed fields (not compilable to Go)
	listComps := map[string]bool{}
	for _, c := range doc.Components {
		for _, d := range c.Data {
			if inferGoType(d.Init) == "any" {
				listComps[c.Name] = true
			}
		}
	}

	for _, td := range doc.Tests {
		if ShouldSkipTest(td) || listComps[td.Component] {
			continue
		}
		g, ok := groups[td.Component]
		if !ok {
			comp := findComp(doc, td.Component)
			if comp == nil {
				continue
			}
			g = &compTests{comp: comp}
			groups[td.Component] = g
		}
		g.tests = append(g.tests, td)
	}

	if len(groups) == 0 {
		return nil, nil
	}

	var b strings.Builder
	// Detect if fmt is needed (string interpolation or string() in computeds)
	needsFmt := false
	for _, g := range groups {
		for _, td := range g.tests {
			if testNeedsFmt(td) {
				needsFmt = true
			}
		}
		for _, c := range g.comp.Computeds {
			if c.Expr.SNGL != nil && nodeNeedsFmt(c.Expr.SNGL) {
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
		for _, td := range g.tests {
			emitTestFunc(&b, td, g.comp)
		}
	}

	src := []byte(b.String())
	formatted, err := format.Source(src)
	if err != nil {
		return src, fmt.Errorf("generated test formatting error: %w\n%s", err, src)
	}
	return formatted, nil
}

func testNeedsFmt(td *ast.TestDef) bool {
	for _, stmt := range td.Body {
		if nodeNeedsFmt(stmt) {
			return true
		}
	}
	for _, sub := range td.Subtests {
		if testNeedsFmt(sub) {
			return true
		}
	}
	return false
}

func nodeNeedsFmt(n ast.Node) bool {
	switch e := n.(type) {
	case *ast.InterpolationExpr:
		return true
	case *ast.CallExpr:
		for _, arg := range e.Args {
			if nodeNeedsFmt(arg) {
				return true
			}
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
	for _, c := range comp.Computeds {
		modelFields[c.Name] = true
		computedFields[c.Name] = true
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
		modelFields:    modelFields,
		computedFields: computedFields,
		localVars:      map[string]bool{},
		structNames:    structNames,
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

	// Computed methods
	for _, c := range comp.Computeds {
		goType := inferGoType(c.Expr)
		if goType == "any" && c.Expr.SNGL != nil {
			goType = snglNodeGoType(c.Expr.SNGL)
		}
		body := ""
		if c.Expr.SNGL != nil {
			body = ec.translateExpr(c.Expr.SNGL)
		} else if c.Expr.Literal != nil {
			body = literalToGo(c.Expr)
		}
		fmt.Fprintf(b, "func (m %sModel) %s() %s {\n", name, c.Name, goType)
		fmt.Fprintf(b, "\treturn %s\n", body)
		b.WriteString("}\n\n")
	}
}

// emitTestFunc emits a Go test function for a single SNGL test.
func emitTestFunc(b *strings.Builder, td *ast.TestDef, comp *ast.Component) {
	funcName := "Test" + exportName(comp.Name) + "_" + sanitizeTestName(td.Desc)

	modelFields := map[string]bool{}
	computedFields := map[string]bool{}
	for _, d := range comp.Data {
		modelFields[d.Name] = true
	}
	for _, c := range comp.Computeds {
		modelFields[c.Name] = true
		computedFields[c.Name] = true
	}
	for _, p := range comp.Params {
		modelFields[p.Name] = true
	}

	ec := &exprContext{
		modelFields:    modelFields,
		computedFields: computedFields,
		localVars:      map[string]bool{},
		structNames:    map[string][]string{},
	}

	fmt.Fprintf(b, "func %s(t *testing.T) {\n", funcName)
	fmt.Fprintf(b, "\tm := new%s()\n", exportName(comp.Name))
	fmt.Fprintf(b, "\t_ = m\n")

	emitTestBody(b, td.Body, ec, 1)

	for _, sub := range td.Subtests {
		emitSubtest(b, sub, ec, 1)
	}

	b.WriteString("}\n\n")
}

func emitSubtest(b *strings.Builder, td *ast.TestDef, ec *exprContext, depth int) {
	indent := strings.Repeat("\t", depth)
	fmt.Fprintf(b, "%st.Run(%q, func(t *testing.T) {\n", indent, td.Desc)
	fmt.Fprintf(b, "%s\tm := m\n", indent) // copy model for isolation

	emitTestBody(b, td.Body, ec, depth+1)

	for _, sub := range td.Subtests {
		emitSubtest(b, sub, ec, depth+1)
	}

	fmt.Fprintf(b, "%s})\n", indent)
}

func emitTestBody(b *strings.Builder, stmts []ast.Node, ec *exprContext, depth int) {
	indent := strings.Repeat("\t", depth)
	for _, stmt := range stmts {
		switch s := stmt.(type) {
		case *ast.CallExpr:
			if s.Func == "assert" && len(s.Args) == 1 {
				expr := ec.translateExpr(s.Args[0])
				original := snglparser.FormatNode(s.Args[0])
				fmt.Fprintf(b, "%sif !(%s) {\n", indent, expr)
				fmt.Fprintf(b, "%s\tt.Fatalf(\"assert(%s) failed\")\n", indent, escapeFmt(original))
				fmt.Fprintf(b, "%s}\n", indent)
				continue
			}
		case *ast.AssignStmt:
			stmts := ec.translateMutation(s)
			for _, line := range stmts {
				fmt.Fprintf(b, "%s%s\n", indent, line)
			}
			continue
		case *ast.ToggleStmt:
			stmts := ec.translateMutation(s)
			for _, line := range stmts {
				fmt.Fprintf(b, "%s%s\n", indent, line)
			}
			continue
		case *ast.MethodExpr:
			stmts := ec.translateMutation(s)
			for _, line := range stmts {
				fmt.Fprintf(b, "%s%s\n", indent, line)
			}
			continue
		}
		// Fallback: expression statement
		fmt.Fprintf(b, "%s_ = %s\n", indent, ec.translateExpr(stmt))
	}
}

// ShouldSkipTest returns true if a test uses features not compilable to Go.
func ShouldSkipTest(td *ast.TestDef) bool {
	return usesUnsupportedFeature(td)
}

func usesUnsupportedFeature(td *ast.TestDef) bool {
	for _, stmt := range td.Body {
		if nodeUsesDOMAccess(stmt) || nodeUsesUnsupported(stmt) {
			return true
		}
	}
	for _, sub := range td.Subtests {
		if usesUnsupportedFeature(sub) {
			return true
		}
	}
	return false
}

// nodeUsesUnsupported detects SNGL features that don't compile to Go:
// - truthiness on non-bool (e.g., !count, !label, !null)
// - .length() / .contains() methods (SNGL builtins, not Go)
// - constant division by zero (compile-time error in Go)
func nodeUsesUnsupported(n ast.Node) bool {
	switch e := n.(type) {
	case *ast.MethodExpr:
		switch e.Method {
		case "length", "contains", "push", "remove":
			return true
		}
	case *ast.CallExpr:
		if e.Func == "assert" && len(e.Args) == 1 {
			return nodeUsesUnsupported(e.Args[0])
		}
	case *ast.UnaryExpr:
		if e.Op == ast.UnaryNot {
			// !ident where ident could be non-bool
			if _, ok := e.Operand.(*ast.LiteralExpr); ok {
				lit := e.Operand.(*ast.LiteralExpr)
				if lit.Kind == ast.LiteralNull {
					return true
				}
			}
			// !ident — can't statically tell if bool. Be conservative:
			// only allow !bool_literal and !comparison
			if isDefinitelyBool(e.Operand) {
				return false
			}
			return true
		}
	case *ast.BinaryExpr:
		if e.Op == ast.BinDiv || e.Op == ast.BinMod {
			if lit, ok := e.Right.(*ast.LiteralExpr); ok {
				if lit.Kind == ast.LiteralInt && lit.Value.(int) == 0 {
					return true
				}
			}
		}
		return nodeUsesUnsupported(e.Left) || nodeUsesUnsupported(e.Right)
	case *ast.TernaryExpr:
		return nodeUsesUnsupported(e.Cond) || nodeUsesUnsupported(e.Then) || nodeUsesUnsupported(e.Else)
	case *ast.AssignStmt:
		return nodeUsesUnsupported(e.Value)
	case *ast.StmtBlock:
		for _, s := range e.Stmts {
			if nodeUsesUnsupported(s) {
				return true
			}
		}
	}
	return false
}

// isDefinitelyBool returns true if the expression is known to produce a bool.
func isDefinitelyBool(n ast.Node) bool {
	switch e := n.(type) {
	case *ast.LiteralExpr:
		return e.Kind == ast.LiteralBool
	case *ast.BinaryExpr:
		switch e.Op {
		case ast.BinEq, ast.BinNeq, ast.BinLt, ast.BinLte, ast.BinGt, ast.BinGte, ast.BinAnd, ast.BinOr:
			return true
		}
	case *ast.UnaryExpr:
		if e.Op == ast.UnaryNot {
			return true
		}
	case *ast.IdentExpr:
		// Can't tell without type info — return false
		return false
	}
	return false
}

func nodeUsesDOMAccess(n ast.Node) bool {
	switch e := n.(type) {
	case *ast.IdentExpr:
		return e.Name == "root"
	case *ast.SelectExpr:
		return nodeUsesDOMAccess(e.Operand)
	case *ast.MethodExpr:
		if strings.HasPrefix(e.Method, "@") || e.Method == "_find" {
			return true
		}
		return nodeUsesDOMAccess(e.Receiver)
	case *ast.CallExpr:
		for _, arg := range e.Args {
			if nodeUsesDOMAccess(arg) {
				return true
			}
		}
	case *ast.BinaryExpr:
		return nodeUsesDOMAccess(e.Left) || nodeUsesDOMAccess(e.Right)
	case *ast.UnaryExpr:
		return nodeUsesDOMAccess(e.Operand)
	case *ast.TernaryExpr:
		return nodeUsesDOMAccess(e.Cond) || nodeUsesDOMAccess(e.Then) || nodeUsesDOMAccess(e.Else)
	case *ast.AssignStmt:
		return nodeUsesDOMAccess(e.Target) || nodeUsesDOMAccess(e.Value)
	case *ast.ToggleStmt:
		return nodeUsesDOMAccess(e.Target)
	case *ast.IndexExpr:
		return nodeUsesDOMAccess(e.Operand) || nodeUsesDOMAccess(e.Index)
	case *ast.StmtBlock:
		for _, s := range e.Stmts {
			if nodeUsesDOMAccess(s) {
				return true
			}
		}
	}
	return false
}

func findComp(doc *ast.Document, name string) *ast.Component {
	for _, c := range doc.Components {
		if c.Name == name {
			return c
		}
	}
	return nil
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
