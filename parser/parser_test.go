package parser

import (
	"os"
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/ast"
)

func parseFixture(t *testing.T, name string) *ast.Document {
	t.Helper()
	f, err := os.Open("testdata/" + name)
	if err != nil {
		t.Fatalf("open fixture %s: %v", name, err)
	}
	defer f.Close()
	doc, err := Parse(name, f)
	if err != nil {
		t.Fatalf("parse %s: %v", name, err)
	}
	return doc
}

func parseFixtureExpectError(t *testing.T, name string) (*ast.Document, error) {
	t.Helper()
	f, err := os.Open("testdata/" + name)
	if err != nil {
		t.Fatalf("open fixture %s: %v", name, err)
	}
	defer f.Close()
	return Parse(name, f)
}

func TestMinimal(t *testing.T) {
	doc := parseFixture(t, "minimal.kdl")
	if doc.App == nil {
		t.Fatal("expected app node")
	}
	if len(doc.App.Children) != 0 {
		t.Errorf("expected 0 children, got %d", len(doc.App.Children))
	}
}

func TestBindForms(t *testing.T) {
	doc := parseFixture(t, "bind_forms.kdl")

	// 2 individual + 2 block = 4 binds
	if got := len(doc.Binds); got != 4 {
		t.Fatalf("expected 4 binds, got %d", got)
	}

	// Individual: bind "count" (int)0
	b := doc.Binds[0]
	if b.Name != "count" {
		t.Errorf("bind[0] name = %q, want %q", b.Name, "count")
	}
	if b.Init.Literal != 0 {
		t.Errorf("bind[0] literal = %v, want 0", b.Init.Literal)
	}
	if b.Init.TypeHint != "int" {
		t.Errorf("bind[0] type hint = %q, want %q", b.Init.TypeHint, "int")
	}

	// Individual: bind "name" (string)"World"
	b = doc.Binds[1]
	if b.Name != "name" {
		t.Errorf("bind[1] name = %q, want %q", b.Name, "name")
	}
	if b.Init.Literal != "World" {
		t.Errorf("bind[1] literal = %v, want %q", b.Init.Literal, "World")
	}

	// Block: x (float)1.0
	b = doc.Binds[2]
	if b.Name != "x" {
		t.Errorf("bind[2] name = %q, want %q", b.Name, "x")
	}
	if b.Init.TypeHint != "float" {
		t.Errorf("bind[2] type hint = %q, want %q", b.Init.TypeHint, "float")
	}

	// Block: active (bool)true
	b = doc.Binds[3]
	if b.Name != "active" {
		t.Errorf("bind[3] name = %q, want %q", b.Name, "active")
	}
	if b.Init.Literal != true {
		t.Errorf("bind[3] literal = %v, want true", b.Init.Literal)
	}

	// 1 individual + 2 block = 3 computeds
	if got := len(doc.Computeds); got != 3 {
		t.Fatalf("expected 3 computeds, got %d", got)
	}

	c := doc.Computeds[0]
	if c.Name != "greeting" {
		t.Errorf("computed[0] name = %q, want %q", c.Name, "greeting")
	}
	if c.Expr.CEL != "'Hello, ' + name" {
		t.Errorf("computed[0] CEL = %q, want %q", c.Expr.CEL, "'Hello, ' + name")
	}

	c = doc.Computeds[1]
	if c.Name != "doubled" {
		t.Errorf("computed[1] name = %q, want %q", c.Name, "doubled")
	}

	c = doc.Computeds[2]
	if c.Name != "isActive" {
		t.Errorf("computed[2] name = %q, want %q", c.Name, "isActive")
	}
}

func TestStyleForms(t *testing.T) {
	doc := parseFixture(t, "style_forms.kdl")

	vbox := doc.App.Children[0]
	if vbox.Component != "vbox" {
		t.Fatalf("expected vbox, got %q", vbox.Component)
	}

	// Inline style attrs
	if _, ok := vbox.StyleAttrs["padding"]; !ok {
		t.Error("vbox missing style.padding")
	}
	if _, ok := vbox.StyleAttrs["gap"]; !ok {
		t.Error("vbox missing style.gap")
	}

	// Text with @style block
	text := vbox.Children[0]
	if text.Component != "text" {
		t.Fatalf("expected text, got %q", text.Component)
	}
	if _, ok := text.StyleBlock["font-size"]; !ok {
		t.Error("text missing @style font-size")
	}
	if _, ok := text.StyleBlock["font-weight"]; !ok {
		t.Error("text missing @style font-weight")
	}
	if _, ok := text.StyleBlock["color"]; !ok {
		t.Error("text missing @style color")
	}

	// Button with both inline and block styles
	button := vbox.Children[1]
	if _, ok := button.StyleAttrs["margin"]; !ok {
		t.Error("button missing style.margin")
	}
	if _, ok := button.StyleBlock["padding"]; !ok {
		t.Error("button missing @style padding")
	}
}

func TestComponent(t *testing.T) {
	doc := parseFixture(t, "component.kdl")

	if got := len(doc.Components); got != 1 {
		t.Fatalf("expected 1 component, got %d", got)
	}

	comp := doc.Components[0]
	if comp.Name != "Counter" {
		t.Errorf("component name = %q, want %q", comp.Name, "Counter")
	}

	if got := len(comp.Params); got != 3 {
		t.Fatalf("expected 3 params, got %d", got)
	}

	// param "label" (string)""
	if comp.Params[0].Name != "label" {
		t.Errorf("param[0] name = %q, want %q", comp.Params[0].Name, "label")
	}
	if comp.Params[0].Default.TypeHint != "string" {
		t.Errorf("param[0] type hint = %q, want %q", comp.Params[0].Default.TypeHint, "string")
	}
	if comp.Params[0].Required {
		t.Error("param[0] should not be required")
	}

	// param "start" (int)0
	if comp.Params[1].Name != "start" {
		t.Errorf("param[1] name = %q, want %q", comp.Params[1].Name, "start")
	}

	// param "name" (string)"" "required"
	if !comp.Params[2].Required {
		t.Error("param[2] should be required")
	}

	// Body: hbox with children
	if got := len(comp.Body); got != 1 {
		t.Fatalf("expected 1 body node, got %d", got)
	}
	if comp.Body[0].Component != "hbox" {
		t.Errorf("body[0] = %q, want %q", comp.Body[0].Component, "hbox")
	}

	// App uses Counter
	counter := doc.App.Children[0]
	if counter.Component != "Counter" {
		t.Errorf("app child = %q, want %q", counter.Component, "Counter")
	}
	if v, ok := counter.Props["label"]; !ok || v.Literal != "Clicks" {
		t.Errorf("Counter label = %v, want %q", v, "Clicks")
	}
}

func TestFullExample(t *testing.T) {
	doc := parseFixture(t, "full_example.kdl")

	// Imports
	if got := len(doc.Imports); got != 1 {
		t.Fatalf("expected 1 import, got %d", got)
	}
	if doc.Imports[0].Path != "app.proto" {
		t.Errorf("import path = %q, want %q", doc.Imports[0].Path, "app.proto")
	}

	// Binds
	if got := len(doc.Binds); got != 2 {
		t.Fatalf("expected 2 binds, got %d", got)
	}
	if doc.Binds[0].Name != "user" {
		t.Errorf("bind[0] = %q, want %q", doc.Binds[0].Name, "user")
	}
	if doc.Binds[0].Init.CEL == "" {
		t.Error("bind[0] should be a CEL expression")
	}
	if doc.Binds[1].Name != "count" {
		t.Errorf("bind[1] = %q, want %q", doc.Binds[1].Name, "count")
	}

	// Computeds
	if got := len(doc.Computeds); got != 2 {
		t.Fatalf("expected 2 computeds, got %d", got)
	}

	// Components
	if got := len(doc.Components); got != 1 {
		t.Fatalf("expected 1 component, got %d", got)
	}

	// App structure
	vbox := doc.App.Children[0]
	if vbox.Component != "vbox" {
		t.Fatalf("expected vbox, got %q", vbox.Component)
	}

	// vbox style attrs
	if _, ok := vbox.StyleAttrs["padding"]; !ok {
		t.Error("vbox missing style.padding")
	}

	// First child: text with @style block
	text := vbox.Children[0]
	if text.Component != "text" {
		t.Errorf("child[0] = %q, want text", text.Component)
	}
	if _, ok := text.StyleBlock["font-size"]; !ok {
		t.Error("text missing @style font-size")
	}

	// Conditional texts
	adultText := vbox.Children[1]
	if adultText.If == nil || adultText.If.CEL != "isAdult" {
		t.Error("child[1] should have if=isAdult")
	}

	// Input with event
	input := vbox.Children[3]
	if input.Component != "input" {
		t.Errorf("child[3] = %q, want input", input.Component)
	}
	if _, ok := input.Events["input"]; !ok {
		t.Error("input missing on:input event")
	}

	// Counter usage
	counter := vbox.Children[4]
	if counter.Component != "Counter" {
		t.Errorf("child[4] = %q, want Counter", counter.Component)
	}

	// Button with CEL text and event
	button := vbox.Children[5]
	if button.Component != "button" {
		t.Errorf("child[5] = %q, want button", button.Component)
	}
	if _, ok := button.Events["click"]; !ok {
		t.Error("button missing on:click event")
	}
}

func TestComponentPropertyParams(t *testing.T) {
	doc := parseFixture(t, "component_prop_params.kdl")

	if got := len(doc.Components); got != 1 {
		t.Fatalf("expected 1 component, got %d", got)
	}

	comp := doc.Components[0]
	if comp.Name != "Counter" {
		t.Errorf("component name = %q, want %q", comp.Name, "Counter")
	}

	if got := len(comp.Params); got != 2 {
		t.Fatalf("expected 2 params, got %d", got)
	}

	// label=(string)""
	if comp.Params[0].Name != "label" {
		t.Errorf("param[0] name = %q, want %q", comp.Params[0].Name, "label")
	}
	if comp.Params[0].Default.TypeHint != "string" {
		t.Errorf("param[0] type hint = %q, want %q", comp.Params[0].Default.TypeHint, "string")
	}

	// start=(int)0
	if comp.Params[1].Name != "start" {
		t.Errorf("param[1] name = %q, want %q", comp.Params[1].Name, "start")
	}
	if comp.Params[1].Default.TypeHint != "int" {
		t.Errorf("param[1] type hint = %q, want %q", comp.Params[1].Default.TypeHint, "int")
	}

	// Body
	if got := len(comp.Body); got != 1 {
		t.Fatalf("expected 1 body node, got %d", got)
	}
}

func TestCELParsing(t *testing.T) {
	doc := parseFixture(t, "bind_forms.kdl")

	// computed "greeting" (cel)"'Hello, ' + name" should have a parsed AST
	c := doc.Computeds[0]
	if c.Expr.CEL == "" {
		t.Fatal("expected CEL expression")
	}
	if c.Expr.AST == nil {
		t.Fatal("expected parsed CEL AST")
	}

	// Literal binds should not have AST
	b := doc.Binds[0] // count (int)0
	if b.Init.AST != nil {
		t.Error("literal bind should not have CEL AST")
	}
}

func TestErrorMissingApp(t *testing.T) {
	_, err := parseFixtureExpectError(t, "error_missing_app.kdl")
	if err == nil {
		t.Fatal("expected error for missing app")
	}
	if !strings.Contains(err.Error(), "missing app") {
		t.Errorf("error = %q, want mention of missing app", err.Error())
	}
}

func TestErrorDuplicateApp(t *testing.T) {
	_, err := parseFixtureExpectError(t, "error_duplicate_app.kdl")
	if err == nil {
		t.Fatal("expected error for duplicate app")
	}
	if !strings.Contains(err.Error(), "duplicate app") {
		t.Errorf("error = %q, want mention of duplicate app", err.Error())
	}
}

func TestErrorUnknownTopLevel(t *testing.T) {
	_, err := parseFixtureExpectError(t, "error_unknown_toplevel.kdl")
	if err == nil {
		t.Fatal("expected error for unknown top-level node")
	}
	if !strings.Contains(err.Error(), "unknown top-level node") {
		t.Errorf("error = %q, want mention of unknown top-level node", err.Error())
	}
}
