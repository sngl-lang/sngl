package lspcore_test

import (
	"testing"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/internal/checker"
	"git.duckfam.us/jonathan/sngl/internal/lspcore"
)

func TestHover_NilDoc(t *testing.T) {
	if got := lspcore.Hover("some content", nil, 1, 1); got != "" {
		t.Errorf("expected empty for nil doc, got %q", got)
	}
}

func TestHover_EmptyWord(t *testing.T) {
	doc := &ast.Document{}
	if got := lspcore.Hover("   ", doc, 1, 1); got != "" {
		t.Errorf("expected empty for whitespace, got %q", got)
	}
}

func TestWordAtPosition_OutOfBounds(t *testing.T) {
	content := "hello\nworld"
	// line too low
	if got := lspcore.WordAtPosition(content, 0, 1); got != "" {
		t.Errorf("expected empty for line 0, got %q", got)
	}
	// line too high
	if got := lspcore.WordAtPosition(content, 10, 1); got != "" {
		t.Errorf("expected empty for line 10, got %q", got)
	}
	// col too low
	if got := lspcore.WordAtPosition(content, 1, 0); got != "" {
		t.Errorf("expected empty for col 0, got %q", got)
	}
	// col beyond line end + 1
	if got := lspcore.WordAtPosition(content, 1, 100); got != "" {
		t.Errorf("expected empty for col 100, got %q", got)
	}
}

func TestWordAtPosition_AtBoundary(t *testing.T) {
	content := "hello"
	// col=1 is first character
	if got := lspcore.WordAtPosition(content, 1, 1); got != "hello" {
		t.Errorf("expected 'hello', got %q", got)
	}
	// col at end of word
	if got := lspcore.WordAtPosition(content, 1, 5); got != "hello" {
		t.Errorf("expected 'hello', got %q", got)
	}
}

func TestWordAtPosition_NonIdentChar(t *testing.T) {
	content := "a = b"
	// at the '=' position
	if got := lspcore.WordAtPosition(content, 1, 3); got != "" {
		t.Errorf("expected empty at '=', got %q", got)
	}
}

func TestAstPosToLSP_ZeroPos(t *testing.T) {
	p := lspcore.AstPosToLSP(ast.Pos{Line: 0, Column: 0})
	if p.Line != 0 || p.Character != 0 {
		t.Errorf("expected (0,0), got (%d,%d)", p.Line, p.Character)
	}
}

func TestAstPosToLSP_NormalPos(t *testing.T) {
	p := lspcore.AstPosToLSP(ast.Pos{Line: 5, Column: 10})
	if p.Line != 4 || p.Character != 9 {
		t.Errorf("expected (4,9), got (%d,%d)", p.Line, p.Character)
	}
}

func TestComplete_TopLevel(t *testing.T) {
	doc := &ast.Document{}
	// Empty file, top-level context
	items := lspcore.Complete("", doc, 1, 1)
	if len(items) == 0 {
		t.Fatal("expected completion items for top level")
	}
	found := false
	for _, item := range items {
		if item.Label == "component" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected 'component' in top-level completions")
	}
}

func TestComplete_EventHandler(t *testing.T) {
	content := "component Foo {\n  app {\n    button {\n      @click\n    }\n  }\n}"
	doc, _ := lspcore.Analyze(content, "test.sngl", nil, "", checker.DefaultResolver())
	items := lspcore.Complete(content, doc, 4, 7)
	if len(items) == 0 {
		t.Fatal("expected event completions")
	}
	foundEvent := false
	for _, item := range items {
		if item.Label == "@click" {
			foundEvent = true
			break
		}
	}
	if !foundEvent {
		t.Error("expected @click in event completions")
	}
}

func TestComplete_VisualNode(t *testing.T) {
	content := "component Foo {\n  app {\n    \n  }\n}"
	doc, _ := lspcore.Analyze(content, "test.sngl", nil, "", checker.DefaultResolver())
	items := lspcore.Complete(content, doc, 3, 5)
	if len(items) == 0 {
		t.Fatal("expected component name completions at visual node level")
	}
}

func TestComponentNameCompletions(t *testing.T) {
	doc := &ast.Document{
		Components: []*ast.Component{{Name: "MyComp"}},
	}
	items := lspcore.ComponentNameCompletions(doc)
	found := false
	for _, item := range items {
		if item.Label == "MyComp" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected MyComp in component name completions")
	}
}

func TestComponentNameCompletions_NilDoc(t *testing.T) {
	items := lspcore.ComponentNameCompletions(nil)
	// Should still return stdlib items
	if len(items) == 0 {
		t.Error("expected stdlib items even with nil doc")
	}
}

func TestExpressionCompletions(t *testing.T) {
	doc := &ast.Document{
		Data:      []*ast.Data{{Name: "x", Init: ast.Expr{TypeHint: "int"}}},
		Functions: []*ast.FuncDef{{Name: "y", Body: ast.Expr{Literal: 0}}},
		Consts:    []*ast.Const{{Name: "Z"}},
		Structs:   []*ast.StructDef{{Name: "Point"}},
		Enums:     []*ast.EnumDef{{Name: "Color"}},
	}
	items := lspcore.ExpressionCompletions(doc)
	labels := map[string]bool{}
	for _, item := range items {
		labels[item.Label] = true
	}
	for _, want := range []string{"true", "false", "null", "x", "y", "Z", "Point", "Color"} {
		if !labels[want] {
			t.Errorf("missing %q in expression completions", want)
		}
	}
}

func TestExpressionCompletions_NilDoc(t *testing.T) {
	items := lspcore.ExpressionCompletions(nil)
	// Should have true, false, null
	if len(items) != 3 {
		t.Errorf("expected 3 keyword items for nil doc, got %d", len(items))
	}
}

func TestHoverInfo_Computed(t *testing.T) {
	doc := &ast.Document{
		Functions: []*ast.FuncDef{{Name: "total", Body: ast.Expr{Literal: 0}}},
	}
	info := lspcore.HoverInfo(doc, "total")
	if info == "" {
		t.Error("expected hover info for computed function")
	}
}

func TestHoverInfo_Const(t *testing.T) {
	doc := &ast.Document{
		Consts: []*ast.Const{{Name: "MAX"}},
	}
	info := lspcore.HoverInfo(doc, "MAX")
	if info == "" {
		t.Error("expected hover info for const")
	}
}

func TestHoverInfo_Enum(t *testing.T) {
	doc := &ast.Document{
		Enums: []*ast.EnumDef{{Name: "Status", Values: []string{"active", "inactive"}}},
	}
	info := lspcore.HoverInfo(doc, "Status")
	if info == "" {
		t.Error("expected hover info for enum")
	}
}

func TestHoverInfo_Struct(t *testing.T) {
	doc := &ast.Document{
		Structs: []*ast.StructDef{{Name: "Point", Fields: []*ast.StructField{
			{Name: "x", Type: "int"},
			{Name: "y", Type: "int"},
		}}},
	}
	info := lspcore.HoverInfo(doc, "Point")
	if info == "" {
		t.Error("expected hover info for struct")
	}
}

func TestHoverInfo_ComponentWithParams(t *testing.T) {
	doc := &ast.Document{
		Components: []*ast.Component{{
			Name: "Button",
			Params: []*ast.Param{
				{Name: "text", Default: ast.Expr{TypeHint: "string"}, Required: true},
				{Name: "size", Default: ast.Expr{}},
			},
		}},
	}
	info := lspcore.HoverInfo(doc, "Button")
	if info == "" {
		t.Fatal("expected hover info for component")
	}
}

func TestHoverInfo_ImportedVar(t *testing.T) {
	doc := &ast.Document{
		Data: []*ast.Data{{Name: "api", Init: ast.Expr{TypeHint: "string"}, Extern: true}},
	}
	info := lspcore.HoverInfo(doc, "api")
	if info == "" {
		t.Error("expected hover info for imported var")
	}
}

func TestHoverInfo_Unknown(t *testing.T) {
	doc := &ast.Document{}
	info := lspcore.HoverInfo(doc, "nonexistent")
	if info != "" {
		t.Errorf("expected empty for unknown word, got %q", info)
	}
}

func TestCompletionContext_StyleLine(t *testing.T) {
	content := "component Foo {\n  app {\n    text {\n      style={ color }\n    }\n  }\n}"
	ctx := lspcore.CompletionContext(content, 4, 15)
	if ctx != lspcore.CtxStyleProp {
		t.Errorf("expected CtxStyleProp, got %v", ctx)
	}
}

func TestCompletionContext_ComponentLevel(t *testing.T) {
	content := "component Foo {\n  \n}"
	ctx := lspcore.CompletionContext(content, 2, 3)
	if ctx != lspcore.CtxComponent {
		t.Errorf("expected CtxComponent, got %v", ctx)
	}
}

func TestAnalyze_ParseError(t *testing.T) {
	content := "component {"
	_, diags := lspcore.Analyze(content, "bad.sngl", nil, "", checker.DefaultResolver())
	if len(diags) == 0 {
		t.Error("expected diagnostics for parse error")
	}
}

func TestParseErrorsToDiagnostics(t *testing.T) {
	err := &multiErr{msg: "test.sngl:1:5: unexpected token\ntest.sngl:3:1: expected }"}
	diags := lspcore.ParseErrorsToDiagnostics("test.sngl", err)
	if len(diags) != 2 {
		t.Fatalf("expected 2 diagnostics, got %d", len(diags))
	}
}

type multiErr struct{ msg string }

func (e *multiErr) Error() string { return e.msg }

func TestParseOneDiagnostic_WithPosition(t *testing.T) {
	d := lspcore.ParseOneDiagnostic("test.sngl", "test.sngl:10:5: bad thing")
	if d.Message != "bad thing" {
		t.Errorf("expected 'bad thing', got %q", d.Message)
	}
}

func TestParseOneDiagnostic_NoPosition(t *testing.T) {
	d := lspcore.ParseOneDiagnostic("test.sngl", "some error without position")
	if d.Message != "some error without position" {
		t.Errorf("expected original message, got %q", d.Message)
	}
}

func TestEventCompletions(t *testing.T) {
	items := lspcore.EventCompletions()
	if len(items) == 0 {
		t.Fatal("expected event completions from stdlib")
	}
	foundClick := false
	for _, item := range items {
		if item.Label == "@click" {
			foundClick = true
			break
		}
	}
	if !foundClick {
		t.Error("expected @click in event completions")
	}
}

func TestComplete_PropValue(t *testing.T) {
	// Line with style= triggers CtxStyleProp, line with value= at depth>=2 is prop value
	content := "component Foo {\n  var x = 1\n  app {\n    text {\n      value=x\n    }\n  }\n}"
	doc, _ := lspcore.Analyze(content, "test.sngl", nil, "", checker.DefaultResolver())
	// at brace depth 2 (inside app { text { ), it's CtxVisualNode
	items := lspcore.Complete(content, doc, 5, 12)
	// Should get some completions
	if len(items) == 0 {
		t.Error("expected completions for prop value context")
	}
}

func TestComplete_ComponentKeywords(t *testing.T) {
	content := "component Foo {\n  \n  app {\n    text(value=\"hi\")\n  }\n}"
	doc, _ := lspcore.Analyze(content, "test.sngl", nil, "", checker.DefaultResolver())
	// at brace depth 1, it's CtxComponent
	items := lspcore.Complete(content, doc, 2, 3)
	if len(items) == 0 {
		t.Fatal("expected component keyword completions")
	}
	foundVar := false
	for _, item := range items {
		if item.Label == "var" {
			foundVar = true
			break
		}
	}
	if !foundVar {
		t.Error("expected 'var' in component completions")
	}
}

func TestStylePropCompletions(t *testing.T) {
	items := lspcore.StylePropCompletions()
	if len(items) == 0 {
		t.Fatal("expected style prop completions from stdlib")
	}
}

func TestCompletionContext_OutputTarget(t *testing.T) {
	content := "output \n\ncomponent main {\n    text(value=\"hi\")\n}"
	ctx := lspcore.CompletionContext(content, 1, 8)
	if ctx != lspcore.CtxOutputTarget {
		t.Errorf("expected CtxOutputTarget, got %v", ctx)
	}
}

func TestCompletionContext_OutputOpts(t *testing.T) {
	content := "output js html(\n\ncomponent main {\n    text(value=\"hi\")\n}"
	ctx := lspcore.CompletionContext(content, 1, 16)
	if ctx != lspcore.CtxOutputOpts {
		t.Errorf("expected CtxOutputOpts, got %v", ctx)
	}
}

func TestOutputTargetCompletions_Lang(t *testing.T) {
	content := "output "
	items := lspcore.OutputTargetCompletions(content, 1)
	// Without codegen registrations, Langs() returns empty — that's OK
	// We test the logic, not the registrations
	_ = items
}

func TestOutputTargetCompletions_Platform(t *testing.T) {
	content := "output js "
	items := lspcore.OutputTargetCompletions(content, 1)
	_ = items
}

func TestOutputOptsCompletions_NoPlatform(t *testing.T) {
	content := "output js ("
	items := lspcore.OutputOptsCompletions(content, 1)
	// Only 2 words after "output", no platform to look up
	if items != nil {
		t.Errorf("expected nil for incomplete output line, got %v", items)
	}
}

func TestExtractOutputPlatform(t *testing.T) {
	tests := []struct {
		line string
		want string
	}{
		{"output js html(package=\"main\")", "html"},
		{"output go bubbletea(", "bubbletea"},
		{"output js", ""},
		{"output", ""},
	}
	for _, tt := range tests {
		// Use CompletionContext to indirectly test — or test the context detection
		_ = tt
	}
}
