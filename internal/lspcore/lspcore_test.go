package lspcore_test

import (
	"slices"
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/ast"
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

// The events offered inside a node's block are that component's, not every
// event the library declares: `@` under a `button` is a click, not a change.
func TestComplete_EventHandler(t *testing.T) {
	content := "import . \"sngl:ui\"\n\ncomponent Foo {\n  vbox {\n    button {\n      @\n    }\n  }\n}"
	doc, _ := lspcore.Analyze(content, "test.sngl", nil, "", nil)
	items := lspcore.Complete(content, doc, 6, 8)
	labels := map[string]bool{}
	for _, it := range items {
		labels[it.Label] = true
	}
	if !labels["@click"] {
		t.Errorf("button offers no @click; got %v", labels)
	}
	if labels["@change"] {
		t.Errorf("button offered @change, which is input's: %v", labels)
	}
}

// A node position offers what the file can name: its own components, and the
// ones its imports brought in.
func TestComplete_VisualNode(t *testing.T) {
	content := "import . \"sngl:ui\"\n\ncomponent Foo {\n  vbox {\n    \n  }\n}"
	doc, _ := lspcore.Analyze(content, "test.sngl", nil, "", nil)
	items := lspcore.Complete(content, doc, 5, 5)
	labels := map[string]bool{}
	for _, it := range items {
		labels[it.Label] = true
	}
	for _, name := range []string{"button", "text", "vbox"} {
		if !labels[name] {
			t.Errorf("sngl:ui is dot-imported but %q is not offered", name)
		}
	}
	if labels["circle"] {
		t.Error("circle offered without sngl:ui/draw imported")
	}
}

// Without the import, none of it is in scope, so none of it is offered.
func TestComplete_VisualNodeWithoutImports(t *testing.T) {
	content := "component Foo {\n  Bar {\n    \n  }\n}\n\ncomponent Bar {\n}"
	doc, _ := lspcore.Analyze(content, "test.sngl", nil, "", nil)
	items := lspcore.Complete(content, doc, 3, 5)
	for _, it := range items {
		if it.Label == "button" {
			t.Error("button offered with no import of sngl:ui")
		}
	}
}

func TestComponentNameCompletions(t *testing.T) {
	doc := &ast.Document{
		Stmts: []ast.Stmt{
			&ast.ComponentDecl{Name: "MyComp"},
		},
	}
	items := lspcore.ComponentNameCompletions("", doc)
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

// A nil document imports nothing, so only the ambient package contributes.
func TestComponentNameCompletions_NilDoc(t *testing.T) {
	for _, it := range lspcore.ComponentNameCompletions("", nil) {
		if it.Detail != "sngl:builtin" {
			t.Errorf("nil doc offered %q from %q", it.Label, it.Detail)
		}
	}
}

func TestExpressionCompletions(t *testing.T) {
	doc := &ast.Document{
		Stmts: []ast.Stmt{
			&ast.VarDecl{Specs: []ast.VarSpec{{Names: []string{"x"}, Type: &ast.NamedType{Name: "int"}}}},
			&ast.FuncDef{Name: "y"},
			&ast.ConstDecl{Specs: []ast.VarSpec{{Names: []string{"Z"}}}},
			&ast.StructDef{Name: "Point"},
			&ast.EnumDef{Name: "Color"},
		},
	}
	items := lspcore.ExpressionCompletions(doc)
	labels := map[string]bool{}
	for _, item := range items {
		labels[item.Label] = true
	}
	for _, want := range []string{"true", "false", "null", "x", "Z", "Point", "Color"} {
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
		Stmts: []ast.Stmt{
			&ast.FuncDef{Name: "total"},
		},
	}
	info := lspcore.HoverInfo(doc, "total")
	if info == "" {
		t.Error("expected hover info for computed function")
	}
}

func TestHoverInfo_Const(t *testing.T) {
	doc := &ast.Document{
		Stmts: []ast.Stmt{
			&ast.ConstDecl{Specs: []ast.VarSpec{{Names: []string{"MAX"}}}},
		},
	}
	info := lspcore.HoverInfo(doc, "MAX")
	if info == "" {
		t.Error("expected hover info for const")
	}
}

func TestHoverInfo_Enum(t *testing.T) {
	doc := &ast.Document{
		Stmts: []ast.Stmt{
			&ast.EnumDef{Name: "Status", Body: []ast.EnumBodyItem{&ast.EnumMember{Name: "active"}, &ast.EnumMember{Name: "inactive"}}},
		},
	}
	info := lspcore.HoverInfo(doc, "Status")
	if info == "" {
		t.Error("expected hover info for enum")
	}
}

func TestHoverInfo_Struct(t *testing.T) {
	doc := &ast.Document{
		Stmts: []ast.Stmt{
			&ast.StructDef{Name: "Point", Body: []ast.StructBodyItem{
				&ast.StructField{Names: []string{"x"}, Type: &ast.NamedType{Name: "int"}},
				&ast.StructField{Names: []string{"y"}, Type: &ast.NamedType{Name: "int"}},
			}},
		},
	}
	info := lspcore.HoverInfo(doc, "Point")
	if info == "" {
		t.Error("expected hover info for struct")
	}
}

func TestHoverInfo_ComponentWithParams(t *testing.T) {
	doc := &ast.Document{
		Stmts: []ast.Stmt{
			&ast.ComponentDecl{
				Name: "Button",
				Props: ast.PropList{
					Props: []ast.ParamOrEventDecl{
						ast.Param{Name: "text", Type: &ast.NamedType{Name: "string"}},
						ast.Param{Name: "size"},
					},
				},
			},
		},
	}
	info := lspcore.HoverInfo(doc, "Button")
	if info == "" {
		t.Fatal("expected hover info for component")
	}
}

func TestHoverInfo_Var(t *testing.T) {
	doc := &ast.Document{
		Stmts: []ast.Stmt{
			&ast.VarDecl{Specs: []ast.VarSpec{{Names: []string{"api"}, Type: &ast.NamedType{Name: "string"}}}},
		},
	}
	info := lspcore.HoverInfo(doc, "api")
	if info == "" {
		t.Error("expected hover info for var")
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

// The editor takes the same route into the checker every other caller does,
// marks included. While a marked declaration was a wrapper around itself, a
// switch over statements matched nothing: the declaration was never registered
// and every use of it was reported as undefined — a file that builds,
// underlined in red.
func TestAnalyze_MarkedDeclarationInAComponentBody(t *testing.T) {
	content := `import . "sngl:ui"
import . "sngl:macro"

component main ui {
    #[foreign("js:./api", "compute", pure)]
    func compute(a int, b int) => a + b

    text(value=string(compute(2, 3)))
}
`
	_, diags := lspcore.Analyze(content, "test.sngl", nil, "", nil)
	for _, d := range diags {
		t.Errorf("marked declaration diagnosed: %s", d.Message)
	}
}

func TestAnalyze_ParseError(t *testing.T) {
	content := "component {"
	_, diags := lspcore.Analyze(content, "bad.sngl", nil, "", nil)
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

// Nothing to offer where no node encloses the cursor.
func TestEventCompletions(t *testing.T) {
	if items := lspcore.EventCompletions("component Foo {\n@\n}", nil, 2); len(items) != 0 {
		t.Errorf("events offered outside a node: %v", items)
	}
}

func TestComplete_PropValue(t *testing.T) {
	content := "component Foo {\n  var x = 1\n  app {\n    text {\n      value=x\n    }\n  }\n}"
	doc, _ := lspcore.Analyze(content, "test.sngl", nil, "", nil)
	items := lspcore.Complete(content, doc, 5, 12)
	_ = items
}

func TestComplete_ComponentKeywords(t *testing.T) {
	content := "component Foo {\n  \n  app {\n    text(value=\"hi\")\n  }\n}"
	doc, _ := lspcore.Analyze(content, "test.sngl", nil, "", nil)
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
	if len(lspcore.StylePropCompletions()) == 0 {
		t.Error("no style props offered")
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
	_ = items
}

func TestOutputTargetCompletions_Platform(t *testing.T) {
	content := "output js "
	items := lspcore.OutputTargetCompletions(content, 1)
	_ = items
}

func TestOutputOptsCompletions_NoPlatform(t *testing.T) {
	// With no platform on the line, stdlib globals are still offered (name,
	// icon, description, version).
	content := "output js ("
	items := lspcore.OutputOptsCompletions(content, 1)
	if len(items) == 0 {
		t.Fatal("expected stdlib option items even without platform")
	}
	have := map[string]bool{}
	for _, it := range items {
		have[it.Label] = true
	}
	for _, want := range []string{"name", "icon", "description", "version"} {
		if !have[want] {
			t.Errorf("missing stdlib option %q in %v", want, items)
		}
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
		_ = tt
	}
}

// A wildcard prop is not a completion. The checker rejects binding it under its
// own name when a name its pattern covers is bound on the same call, and
// completion cannot tell the two cases apart — it has one parsed document and
// no checker — so it must not offer the name at all.
func TestPropListCompletions_OmitsWildcardProp(t *testing.T) {
	content := "component attrs(\n  label string = \"\",\n  #[wildcard(\"data[A-Za-z0-9]+\")] data map<string, string>,\n) {\n  text(value = label)\n}\ncomponent main {\n  window {\n    attrs()\n  }\n}\n"
	doc, _ := lspcore.Analyze(content, "test.sngl", nil, "", nil)
	items := lspcore.PropListCompletions(content, doc, 9, 11)
	var labels []string
	for _, it := range items {
		labels = append(labels, it.Label)
	}
	if !slices.Contains(labels, "label") {
		t.Fatalf("expected the declared prop \"label\" among %v", labels)
	}
	if slices.Contains(labels, "data") {
		t.Errorf("wildcard prop \"data\" offered as a completion: %v", labels)
	}
}

// An import path completes with the packages a program may name, and the
// compiler's own tier is not among them: `sngl:internal/<name>` resolves only
// from library source, so offering it is offering an import that cannot
// compile.
func TestImportPathCompletionsHideTheInternalTier(t *testing.T) {
	content := "import . \"\n"
	if ctx := lspcore.CompletionContext(content, 1, 11); ctx != lspcore.CtxImportPath {
		t.Fatalf("context = %v, want CtxImportPath", ctx)
	}
	items := lspcore.ImportPathCompletions(content, 1)
	if len(items) == 0 {
		t.Fatal("no import path completions")
	}
	have := map[string]bool{}
	for _, it := range items {
		have[it.Label] = true
		if strings.HasPrefix(it.Label, "sngl:internal/") {
			t.Errorf("offered %q, which only library source may import", it.Label)
		}
	}
	for _, want := range []string{"sngl:ui", "sngl:app", "sngl:ui/draw"} {
		if !have[want] {
			t.Errorf("missing %q from import completions", want)
		}
	}
	// A closed quote is not a path position.
	if ctx := lspcore.CompletionContext("import . \"sngl:ui\"\n", 1, 19); ctx == lspcore.CtxImportPath {
		t.Error("cursor past the closing quote still read as an import path")
	}
}

// Completion is asked for mid-edit, when the buffer does not parse: `button(`
// has no closing paren, and Analyze returns a document with no statements at
// all rather than a partial one. The imports decide what is in scope, so they
// are read from the text when the parse has none -- without that, everything
// below is empty exactly when an editor asks.
func TestPropListCompletions_UnparseableBuffer(t *testing.T) {
	content := "import . \"sngl:ui\"\n\ncomponent Foo {\n  button(\n}"
	doc, _ := lspcore.Analyze(content, "test.sngl", nil, "", nil)
	if doc != nil && len(doc.Stmts) > 0 {
		t.Fatal("this buffer is supposed to be unparseable; the test no longer covers what it says")
	}
	items := lspcore.Complete(content, doc, 4, 10)
	byLabel := map[string]lspcore.CompletionItem{}
	styles := 0
	for _, it := range items {
		byLabel[it.Label] = it
		if it.Label == "style" {
			styles++
		}
	}
	// The props and events are resolved, not written: `text` is a string and
	// @click carries a ClickEvent, neither of which the AST says.
	if got := byLabel["text"].Detail; got != "string" {
		t.Errorf("button.text detail = %q, want string", got)
	}
	if got := byLabel["@click"].Detail; got != "ClickEvent" {
		t.Errorf("button @click detail = %q, want ClickEvent", got)
	}
	if _, ok := byLabel["value"]; ok {
		t.Error("button offered `value`, which is input's prop")
	}
	// `style` comes from the schema with its type, and the untyped fallback
	// must not double it.
	if got := byLabel["style"].Detail; got != "Style" {
		t.Errorf("style detail = %q, want Style", got)
	}
	if styles != 1 {
		t.Errorf("style offered %d times", styles)
	}
}

// An aliased library import is reachable through its alias, which used to
// return nothing at all.
func TestNamespaceCompletions_AliasedLibraryPackage(t *testing.T) {
	content := "import d \"sngl:ui/draw\"\n\ncomponent Foo {\n  d.\n}"
	doc, _ := lspcore.Analyze(content, "test.sngl", nil, "", nil)
	labels := map[string]bool{}
	for _, it := range lspcore.Complete(content, doc, 4, 5) {
		labels[it.Label] = true
	}
	for _, name := range []string{"canvas", "circle", "shape"} {
		if !labels[name] {
			t.Errorf("d. offers no %q; got %v", name, labels)
		}
	}
	if labels["button"] {
		t.Error("d. offered button, which is sngl:ui's")
	}
}
