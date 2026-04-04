package parser_test

import (
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/internal/parser"
)

func TestKeywords(t *testing.T) {
	kws := parser.Keywords()
	if len(kws) == 0 {
		t.Fatal("expected keywords to be non-empty")
	}
	// Verify it's a copy, not the original
	kws["bogus"] = 0
	kws2 := parser.Keywords()
	if _, ok := kws2["bogus"]; ok {
		t.Error("Keywords() should return a copy, not the original map")
	}
}

func TestFormatNode_Nil(t *testing.T) {
	if got := parser.FormatNode(nil); got != "null" {
		t.Errorf("FormatNode(nil) = %q, want 'null'", got)
	}
}

func TestFormatNode_AssignStmt(t *testing.T) {
	tests := []struct {
		op   ast.AssignOp
		want string
	}{
		{ast.AssignSet, "x = 1"},
		{ast.AssignAdd, "x += 1"},
		{ast.AssignSub, "x -= 1"},
		{ast.AssignMul, "x *= 1"},
		{ast.AssignDiv, "x /= 1"},
		{ast.AssignMod, "x %= 1"},
	}
	for _, tc := range tests {
		node := &ast.AssignStmt{
			Target: &ast.IdentExpr{Name: "x"},
			Op:     tc.op,
			Value:  &ast.LiteralExpr{Value: 1, Kind: ast.LiteralInt},
		}
		got := parser.FormatNode(node)
		if got != tc.want {
			t.Errorf("FormatNode(assign %v) = %q, want %q", tc.op, got, tc.want)
		}
	}
}

func TestFormatNode_ToggleStmt(t *testing.T) {
	node := &ast.ToggleStmt{Target: &ast.IdentExpr{Name: "active"}}
	if got := parser.FormatNode(node); got != "active!!" {
		t.Errorf("FormatNode(toggle) = %q, want 'active!!'", got)
	}
}

func TestFormatNode_EmitStmt(t *testing.T) {
	node := &ast.EmitStmt{
		Name: "click",
		Args: []ast.Node{&ast.LiteralExpr{Value: 42, Kind: ast.LiteralInt}},
	}
	if got := parser.FormatNode(node); got != "@click(42)" {
		t.Errorf("FormatNode(emit) = %q, want '@click(42)'", got)
	}
}

func TestFormatNode_ReturnStmt(t *testing.T) {
	node := &ast.ReturnStmt{Value: &ast.LiteralExpr{Value: 1, Kind: ast.LiteralInt}}
	if got := parser.FormatNode(node); got != "return 1" {
		t.Errorf("got %q, want 'return 1'", got)
	}
	node2 := &ast.ReturnStmt{}
	if got := parser.FormatNode(node2); got != "return" {
		t.Errorf("got %q, want 'return'", got)
	}
}

func TestFormatNode_VarStmt(t *testing.T) {
	node := &ast.VarStmt{
		Name: "x",
		Type: "int",
		Init: &ast.LiteralExpr{Value: 0, Kind: ast.LiteralInt},
	}
	got := parser.FormatNode(node)
	if !strings.Contains(got, "var x") {
		t.Errorf("expected 'var x' in output, got %q", got)
	}
}

func TestFormatNode_CallStmt(t *testing.T) {
	node := &ast.CallStmt{
		Call: &ast.CallExpr{
			Func: "doStuff",
			Args: []ast.Node{&ast.LiteralExpr{Value: "arg", Kind: ast.LiteralString}},
		},
	}
	got := parser.FormatNode(node)
	if got != `doStuff("arg")` {
		t.Errorf("got %q, want 'doStuff(\"arg\")'", got)
	}
}

func TestFormatNode_SelectExpr(t *testing.T) {
	node := &ast.SelectExpr{
		Operand: &ast.IdentExpr{Name: "obj"},
		Field:   "name",
	}
	if got := parser.FormatNode(node); got != "obj.name" {
		t.Errorf("got %q, want 'obj.name'", got)
	}
}

func TestFormatNode_IndexExpr(t *testing.T) {
	node := &ast.IndexExpr{
		Operand: &ast.IdentExpr{Name: "list"},
		Index:   &ast.LiteralExpr{Value: 0, Kind: ast.LiteralInt},
	}
	if got := parser.FormatNode(node); got != "list[0]" {
		t.Errorf("got %q, want 'list[0]'", got)
	}
}

func TestFormatNode_StructExpr(t *testing.T) {
	node := &ast.StructExpr{
		Name: "Point",
		Fields: []ast.StructFieldLit{
			{Name: "x", Value: &ast.LiteralExpr{Value: 1, Kind: ast.LiteralInt}},
			{Name: "y", Value: &ast.LiteralExpr{Value: 2, Kind: ast.LiteralInt}},
		},
	}
	got := parser.FormatNode(node)
	if got != "Point{x: 1, y: 2}" {
		t.Errorf("got %q, want 'Point{x: 1, y: 2}'", got)
	}
}

func TestFormatNode_ElementRefExpr(t *testing.T) {
	node := &ast.ElementRefExpr{Name: "myBtn"}
	if got := parser.FormatNode(node); got != "#myBtn" {
		t.Errorf("got %q, want '#myBtn'", got)
	}
}

func TestFormatNode_InterpolationExpr(t *testing.T) {
	node := &ast.InterpolationExpr{
		Parts: []ast.Node{
			&ast.LiteralExpr{Value: "hello ", Kind: ast.LiteralString},
			&ast.IdentExpr{Name: "name"},
		},
	}
	got := parser.FormatNode(node)
	if got != `"hello {name}"` {
		t.Errorf("got %q", got)
	}
}

func TestFormatStmt_Block(t *testing.T) {
	block := &ast.StmtBlock{
		Stmts: []ast.Node{
			&ast.AssignStmt{
				Target: &ast.IdentExpr{Name: "x"},
				Op:     ast.AssignSet,
				Value:  &ast.LiteralExpr{Value: 1, Kind: ast.LiteralInt},
			},
			&ast.AssignStmt{
				Target: &ast.IdentExpr{Name: "y"},
				Op:     ast.AssignSet,
				Value:  &ast.LiteralExpr{Value: 2, Kind: ast.LiteralInt},
			},
		},
	}
	got := parser.FormatStmt(block)
	if got != "x = 1; y = 2" {
		t.Errorf("got %q, want 'x = 1; y = 2'", got)
	}
}

func TestFormatLiteral_Default(t *testing.T) {
	doc := &ast.Document{
		Components: []*ast.Component{{
			Name: "main",
			Body: []*ast.VisualNode{{
				Component: "text",
				Props: map[string]ast.Expr{
					"value": {Literal: struct{ x int }{42}},
				},
			}},
		}},
	}
	result := parser.Format(doc)
	if !strings.Contains(result, "42") {
		t.Errorf("expected default literal to contain '42', got %s", result)
	}
}

func TestEscapeStringContent_ControlChars(t *testing.T) {
	doc := &ast.Document{
		Components: []*ast.Component{{
			Name: "main",
			Body: []*ast.VisualNode{{
				Component: "text",
				Props: map[string]ast.Expr{
					"value": {Literal: "a\x01b\x7fc"},
				},
			}},
		}},
	}
	result := parser.Format(doc)
	if strings.Contains(result, "\x01") || strings.Contains(result, "\x7f") {
		t.Error("expected control characters to be stripped")
	}
}

func TestEscapeStringContent_Backslash(t *testing.T) {
	doc := &ast.Document{
		Components: []*ast.Component{{
			Name: "main",
			Body: []*ast.VisualNode{{
				Component: "text",
				Props: map[string]ast.Expr{
					"value": {Literal: `a\b`},
				},
			}},
		}},
	}
	result := parser.Format(doc)
	if !strings.Contains(result, `\\`) {
		t.Error("expected backslash to be escaped")
	}
}

func TestEscapeStringContent_CarriageReturn(t *testing.T) {
	doc := &ast.Document{
		Components: []*ast.Component{{
			Name: "main",
			Body: []*ast.VisualNode{{
				Component: "text",
				Props: map[string]ast.Expr{
					"value": {Literal: "a\rb"},
				},
			}},
		}},
	}
	result := parser.Format(doc)
	if !strings.Contains(result, `\r`) {
		t.Error("expected carriage return to be escaped")
	}
}

func TestEscapeStringContent_BraceEscape(t *testing.T) {
	doc := &ast.Document{
		Components: []*ast.Component{{
			Name: "main",
			Body: []*ast.VisualNode{{
				Component: "text",
				Props: map[string]ast.Expr{
					"value": {Literal: "a{b"},
				},
			}},
		}},
	}
	result := parser.Format(doc)
	if !strings.Contains(result, `\{`) {
		t.Errorf("expected brace to be escaped, got %s", result)
	}
}

func TestParseScanString_EscapeSequences(t *testing.T) {
	src := `component main {
    var x = "line1\nline2\ttab\rret\\slash\"quote"
    text(value=x)
}`
	doc, err := parser.Parse("test.sngl", strings.NewReader(src))
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if doc == nil {
		t.Fatal("expected non-nil doc")
	}
}

func TestParseScanString_UnknownEscape(t *testing.T) {
	// \x is an unknown escape — should be preserved as \x
	src := `component main {
    var x = "test\xval"
    text(value=x)
}`
	doc, err := parser.Parse("test.sngl", strings.NewReader(src))
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if doc == nil {
		t.Fatal("expected non-nil doc")
	}
}

func TestParseScanString_Unterminated(t *testing.T) {
	src := "component main {\n    var x = \"unterminated\n}"
	_, err := parser.Parse("test.sngl", strings.NewReader(src))
	if err == nil {
		t.Error("expected parse error for unterminated string")
	}
}

func TestParseEmitStmt(t *testing.T) {
	src := `component Button {
    event myEvent int
    button(text="go", @click={ @myEvent(42) })
}

component main {
    Button(@myEvent={ count = event })
    var count = 0
    text(value=string(count))
}`
	doc, err := parser.Parse("test.sngl", strings.NewReader(src))
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	result := parser.Format(doc)
	if !strings.Contains(result, "@myEvent(42)") {
		t.Errorf("expected @myEvent(42) in formatted output:\n%s", result)
	}
}

func TestParseAssignmentOperators(t *testing.T) {
	src := `component main {
    var x = 0
    button(text="+", @click={ x += 1 })
    button(text="-", @click={ x -= 1 })
    button(text="*", @click={ x *= 2 })
    button(text="/", @click={ x /= 2 })
    button(text="%", @click={ x %= 3 })
    text(value=string(x))
}`
	doc, err := parser.Parse("test.sngl", strings.NewReader(src))
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	result := parser.Format(doc)
	for _, op := range []string{"+=", "-=", "*=", "/=", "%="} {
		if !strings.Contains(result, op) {
			t.Errorf("expected %q in formatted output:\n%s", op, result)
		}
	}
}

func TestParseToggleStmt(t *testing.T) {
	src := `component main {
    var active = false
    button(text="toggle", @click={ active!! })
    text(value=string(active))
}`
	doc, err := parser.Parse("test.sngl", strings.NewReader(src))
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	result := parser.Format(doc)
	if !strings.Contains(result, "active!!") {
		t.Errorf("expected 'active!!' in output:\n%s", result)
	}
}

func TestParseStringInterpolation(t *testing.T) {
	src := `component main {
    var name = "world"
    func greeting() "hello {name}!"
    text(value=greeting)
}`
	doc, err := parser.Parse("test.sngl", strings.NewReader(src))
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	result := parser.Format(doc)
	if !strings.Contains(result, "{name}") {
		t.Errorf("expected interpolation in output:\n%s", result)
	}
}

func TestParseImport(t *testing.T) {
	src := `import "widgets"

component main {
    text(value="hi")
}`
	doc, err := parser.Parse("test.sngl", strings.NewReader(src))
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if len(doc.Imports) != 1 {
		t.Fatalf("expected 1 import, got %d", len(doc.Imports))
	}
	imp := doc.Imports[0]
	if imp.Namespace != "widgets" {
		t.Errorf("expected namespace 'widgets', got %q", imp.Namespace)
	}
}

func TestParseImportWithScheme(t *testing.T) {
	src := `import "test://api/types.d.ts"

component main {
    text(value="hi")
}`
	doc, err := parser.Parse("test.sngl", strings.NewReader(src))
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if len(doc.Imports) != 1 {
		t.Fatalf("expected 1 import, got %d", len(doc.Imports))
	}
	imp := doc.Imports[0]
	if imp.Scheme != "test" {
		t.Errorf("expected scheme 'test', got %q", imp.Scheme)
	}
	if imp.Namespace != "types" {
		t.Errorf("expected namespace 'types', got %q", imp.Namespace)
	}
}

func TestFormatTimer(t *testing.T) {
	src := `component main {
    var count = 0
    var running = true
    timer 100ms running {
        count += 1
    }
    text(value=string(count))
}`
	doc, err := parser.Parse("test.sngl", strings.NewReader(src))
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	result := parser.Format(doc)
	if !strings.Contains(result, "timer") {
		t.Errorf("expected 'timer' in formatted output:\n%s", result)
	}
}

func TestFormatFuncDefs_StdlibSkipped(t *testing.T) {
	doc := &ast.Document{
		Components: []*ast.Component{{
			Name: "main",
			Functions: []*ast.FuncDef{
				{Name: "userFunc", Body: ast.Expr{Literal: 42}, IsStdlib: false},
				{Name: "stdlibFunc", Body: ast.Expr{Literal: 0}, IsStdlib: true},
			},
			Body: []*ast.VisualNode{{Component: "text"}},
		}},
	}
	result := parser.Format(doc)
	if !strings.Contains(result, "userFunc") {
		t.Error("expected userFunc in output")
	}
	if strings.Contains(result, "stdlibFunc") {
		t.Error("expected stdlibFunc to be skipped")
	}
}

func TestCanInferType_NullLiteral(t *testing.T) {
	src := `component main {
    var x int = null
    text(value=string(x))
}`
	doc, err := parser.Parse("test.sngl", strings.NewReader(src))
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	result := parser.Format(doc)
	if !strings.Contains(result, "int") {
		t.Errorf("expected type hint kept for null value:\n%s", result)
	}
}

func TestFormatLiteralExpr_ColorLiteral(t *testing.T) {
	node := &ast.LiteralExpr{Value: "#ff0000", Kind: ast.LiteralColor}
	got := parser.FormatNode(node)
	if got != "#ff0000" {
		t.Errorf("got %q, want '#ff0000'", got)
	}
}

func TestFormatLiteralExpr_NullLiteral(t *testing.T) {
	node := &ast.LiteralExpr{Value: nil, Kind: ast.LiteralNull}
	got := parser.FormatNode(node)
	if got != "null" {
		t.Errorf("got %q, want 'null'", got)
	}
}

func TestFormatLiteralExpr_UnitLiteral(t *testing.T) {
	node := &ast.LiteralExpr{
		Value: ast.UnitLiteral{Number: "10", Suffix: "px"},
		Kind:  ast.LiteralUnit,
	}
	got := parser.FormatNode(node)
	if got != "10px" {
		t.Errorf("got %q, want '10px'", got)
	}
}

func TestFormatBinOps(t *testing.T) {
	ops := []struct {
		op   ast.BinaryOp
		want string
	}{
		{ast.BinAdd, "+"},
		{ast.BinSub, "-"},
		{ast.BinMul, "*"},
		{ast.BinDiv, "/"},
		{ast.BinMod, "%"},
		{ast.BinEq, "=="},
		{ast.BinNeq, "!="},
		{ast.BinLt, "<"},
		{ast.BinLte, "<="},
		{ast.BinGt, ">"},
		{ast.BinGte, ">="},
		{ast.BinAnd, "&&"},
		{ast.BinOr, "||"},
	}
	for _, tc := range ops {
		node := &ast.BinaryExpr{
			Op:    tc.op,
			Left:  &ast.IdentExpr{Name: "a"},
			Right: &ast.IdentExpr{Name: "b"},
		}
		got := parser.FormatNode(node)
		if !strings.Contains(got, tc.want) {
			t.Errorf("expected %q in %q for op %v", tc.want, got, tc.op)
		}
	}
}

func TestFormatPostfixOperand_NumericLiteral(t *testing.T) {
	node := &ast.MethodExpr{
		Receiver: &ast.LiteralExpr{Value: 0, Kind: ast.LiteralInt},
		Method:   "toString",
	}
	got := parser.FormatNode(node)
	if !strings.Contains(got, "(0)") {
		t.Errorf("expected (0) for numeric receiver, got %q", got)
	}
}

func TestParseFuncBlock(t *testing.T) {
	src := `component main {
    func clamp(val int, lo int, hi int) int {
        var clamped = val < lo ? lo : val
        var result = clamped > hi ? hi : clamped
        return result
    }
    text(value=string(clamp(5, 0, 10)))
}`
	doc, err := parser.Parse("test.sngl", strings.NewReader(src))
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	result := parser.Format(doc)
	if !strings.Contains(result, "func clamp") {
		t.Errorf("expected 'func clamp' in output:\n%s", result)
	}
	if !strings.Contains(result, "return") {
		t.Errorf("expected 'return' in output:\n%s", result)
	}
}

func TestParseGroupedVars(t *testing.T) {
	src := `component main {
    var (
        a = 0
        b = "hello"
        c = true
    )
    text(value=b)
}`
	doc, err := parser.Parse("test.sngl", strings.NewReader(src))
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	// component main hoists to doc level
	if len(doc.Data) != 3 {
		t.Errorf("expected 3 vars, got %d", len(doc.Data))
	}
}

func TestParseGroupedComputeds(t *testing.T) {
	src := `component main {
    var x = 1
    func double() x * 2
    func triple() x * 3
    text(value=string(double))
}`
	doc, err := parser.Parse("test.sngl", strings.NewReader(src))
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	// Two zero-arg expression-form functions (formerly computeds)
	count := 0
	for _, fn := range doc.Functions {
		if fn.Body.SNGL != nil && len(fn.Params) == 0 {
			count++
		}
	}
	if count != 2 {
		t.Errorf("expected 2 computed functions, got %d", count)
	}
}

func TestParseGroupedConsts(t *testing.T) {
	src := `component main {
    const (
        A = 1
        B = 2
    )
    text(value=string(A))
}`
	doc, err := parser.Parse("test.sngl", strings.NewReader(src))
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if len(doc.Consts) != 2 {
		t.Errorf("expected 2 consts, got %d", len(doc.Consts))
	}
}

func TestParseForLoop(t *testing.T) {
	src := `component main {
    var items = [1, 2, 3]
    for item in items {
        text(value=string(item))
    }
}`
	doc, err := parser.Parse("test.sngl", strings.NewReader(src))
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	result := parser.Format(doc)
	if !strings.Contains(result, "for") {
		t.Errorf("expected 'for' in output:\n%s", result)
	}
}

func TestParseMultilineEvent(t *testing.T) {
	src := `component main {
    var a = 0
    var b = 0
    button(text="go", @click={ a += 1; b += 2 })
    text(value=string(a))
}`
	doc, err := parser.Parse("test.sngl", strings.NewReader(src))
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	result := parser.Format(doc)
	if !strings.Contains(result, "a += 1") {
		t.Errorf("expected compound event:\n%s", result)
	}
}

func TestParseEnum(t *testing.T) {
	src := `enum Color { red, green, blue }

component main {
    var c Color = "red"
    text(value=c)
}`
	doc, err := parser.Parse("test.sngl", strings.NewReader(src))
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if len(doc.Enums) != 1 {
		t.Fatalf("expected 1 enum, got %d", len(doc.Enums))
	}
}

func TestParseStringInterpolation_Empty(t *testing.T) {
	// Empty interpolation should produce error
	src := `component main {
    var x = "hello {} world"
    text(value=x)
}`
	_, err := parser.Parse("test.sngl", strings.NewReader(src))
	if err == nil {
		t.Error("expected error for empty interpolation")
	}
}

func TestParseStringInterpolation_Nested(t *testing.T) {
	src := `component main {
    var x = 1
    var y = 2
    func msg() "sum={x + y}"
    text(value=msg)
}`
	doc, err := parser.Parse("test.sngl", strings.NewReader(src))
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	result := parser.Format(doc)
	if !strings.Contains(result, "{x + y}") {
		t.Errorf("expected nested expr in interpolation:\n%s", result)
	}
}

func TestParseOutput(t *testing.T) {
	src := `output {
    js { html }
}

component main {
    text(value="hi")
}`
	doc, err := parser.Parse("test.sngl", strings.NewReader(src))
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if len(doc.Outputs) != 1 {
		t.Fatalf("expected 1 output, got %d", len(doc.Outputs))
	}
	if doc.Outputs[0].Lang != "js" || doc.Outputs[0].Platform != "html" {
		t.Errorf("expected js html, got %s %s", doc.Outputs[0].Lang, doc.Outputs[0].Platform)
	}
}

func TestParseOutputGroup(t *testing.T) {
	src := `output {
    js { html }
    go { bubbletea }
}

component main {
    text(value="hi")
}`
	doc, err := parser.Parse("test.sngl", strings.NewReader(src))
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if len(doc.Outputs) != 2 {
		t.Fatalf("expected 2 outputs, got %d", len(doc.Outputs))
	}
}

func TestParseOutputDefaults(t *testing.T) {
	src := `output(name="My App", icon="icon.svg") {
    js { html }
}

component main {
    text(value="hi")
}`
	doc, err := parser.Parse("test.sngl", strings.NewReader(src))
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if len(doc.Outputs) != 1 {
		t.Fatalf("expected 1 output, got %d", len(doc.Outputs))
	}
	if doc.OutputDefaults["name"] != "My App" {
		t.Errorf("expected name=My App, got %q", doc.OutputDefaults["name"])
	}
	if doc.OutputDefaults["icon"] != "icon.svg" {
		t.Errorf("expected icon=icon.svg, got %q", doc.OutputDefaults["icon"])
	}
}


func TestParseElementRef(t *testing.T) {
	src := `component main {
    var count = 0
    button #myBtn (text="go", @click={ count += 1 })
    text(value=string(count))
}`
	doc, err := parser.Parse("test.sngl", strings.NewReader(src))
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	result := parser.Format(doc)
	if !strings.Contains(result, "#myBtn") {
		t.Errorf("expected '#myBtn' in output:\n%s", result)
	}
}

func TestParseStruct(t *testing.T) {
	src := `struct Point {
    x int = 0
    y int = 0
}

component main {
    var p = Point{x: 1, y: 2}
    text(value=string(p.x))
}`
	doc, err := parser.Parse("test.sngl", strings.NewReader(src))
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if len(doc.Structs) != 1 {
		t.Fatalf("expected 1 struct, got %d", len(doc.Structs))
	}
}
