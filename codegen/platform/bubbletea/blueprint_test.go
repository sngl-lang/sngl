package bubbletea

import (
	"strings"
	"testing"

	"duckfam.us/sngl/codegen"
	"duckfam.us/sngl/internal/checker"
	"duckfam.us/sngl/internal/parser"
	"duckfam.us/sngl/ir"
)

// compileBubbletea drives a SNGL source string through
// parse → check → optimize → lower(Platform:"bubbletea") → CompileIR and
// returns the generated Go source. It reuses compileAndVerify (which runs the
// optimize/lower/codegen pipeline and asserts the output is valid Go).
//
// Crucially it passes the registered Platforms/Languages to the checker (as
// the CLI does), so the stdlib platform extensions in bubbletea.sngl — i.e.
// the new-form `component sngl.text { platform bubbletea { ... } }` bodies —
// are merged via mergePlatformExtensions. Without this the checker silently
// falls back to the language-agnostic lib/ stdlib component and never
// exercises the platform body at all.
func compileBubbletea(t *testing.T, src string) string {
	t.Helper()
	doc, err := parser.Parse("test.sngl", []byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	var plats []ir.Platform
	if p := codegen.LookupPlatform("bubbletea"); p != nil {
		plats = append(plats, p)
	}
	var langs []ir.Language
	if l := codegen.LookupLang("go"); l != nil {
		langs = append(langs, l)
	}
	pkg, diags := checker.Check(doc, &checker.Config{
		IsMain:    true,
		Platforms: plats,
		Targets:   []ir.StaticTarget{{Platform: "bubbletea", Language: "go"}},
		Languages: langs,
	})
	if hasErrors(diags) {
		t.Fatalf("check: %s", firstError(diags))
	}
	return string(compileAndVerify(t, doc, pkg))
}

// TestNewFormTextBindsValue guards the new-form platform-body binding
// mechanism: a stdlib component declared as
//
//	component sngl.text { platform bubbletea { Styled(content=value) {} } }
//
// called as text(value="HELLO") must render the caller's argument, not "".
func TestNewFormTextBindsValue(t *testing.T) {
	src := `import . "sngl:ui"
import ui "sngl:ui"
output { go { bubbletea } }
ui.window {
    text(value="HELLO")
}`
	out := compileBubbletea(t, src)
	if !strings.Contains(out, `"HELLO"`) {
		t.Fatalf("generated source missing bound value; got:\n%s", out)
	}
}

// --- extractBlueprint unit tests ---
//
// These construct the inlined primitive *ir.NodeInst directly. Phase 1 only
// declares the blueprint vocabulary + extractor; the wrapper-body inlining that
// produces these nodes at lower time lands in Phase 2,
// so there is no inlined node to compile through the harness yet. The shapes
// built here mirror exactly what the inlined primitives carry: struct-literal
// props for Model/Focus, list-of-struct-literal props for binds/events, an
// enum-member Ident for join, and string-literal props for content.

// Value is the value, not its source spelling — the delimiters are never part
// of it. This helper used to add them, which only read correctly because
// IRLiteralString stripped a leading and trailing quote back off.
func irStr(s string) *ir.Literal {
	return &ir.Literal{Type: ir.TypString, Value: s}
}

func irBool(b bool) *ir.Literal {
	raw := "false"
	if b {
		raw = "true"
	}
	return &ir.Literal{Type: ir.TypBool, Value: raw}
}

func irEnumMember(member string) *ir.Ident {
	return &ir.Ident{Member: member}
}

func irStruct(fields map[string]ir.Expr) *ir.StructLit {
	sl := &ir.StructLit{}
	for name, val := range fields {
		sl.Fields = append(sl.Fields, ir.FieldInit{Name: name, Value: val})
	}
	return sl
}

func TestExtractLayout(t *testing.T) {
	n := &ir.NodeInst{
		Name: "Layout",
		Props: []ir.Arg{
			{Name: "join", Value: irEnumMember("horizontal")},
		},
	}
	bp := extractBlueprint(n)
	if bp.Kind != bpLayout {
		t.Fatalf("kind=%v, want bpLayout", bp.Kind)
	}
	if bp.Join != joinHorizontal {
		t.Fatalf("join=%v, want joinHorizontal", bp.Join)
	}

	// vertical default
	nv := &ir.NodeInst{
		Name:  "Layout",
		Props: []ir.Arg{{Name: "join", Value: irEnumMember("vertical")}},
	}
	if bp := extractBlueprint(nv); bp.Join != joinVertical {
		t.Fatalf("join=%v, want joinVertical", bp.Join)
	}
}

func TestExtractStyled(t *testing.T) {
	n := &ir.NodeInst{
		Name: "Styled",
		Props: []ir.Arg{
			{Name: "content", Value: irStr("hello")},
			{Name: "focus", Value: irStruct(map[string]ir.Expr{"enabled": irBool(true)})},
		},
	}
	bp := extractBlueprint(n)
	if bp.Kind != bpStyled {
		t.Fatalf("kind=%v, want bpStyled", bp.Kind)
	}
	if s, ok := codegen.IRLiteralString(bp.Content); !ok || s != "hello" {
		t.Fatalf("content=%q ok=%v", s, ok)
	}
	if !bp.Focus {
		t.Fatalf("expected focusable")
	}

	// Styled without focus → not focusable.
	nb := &ir.NodeInst{
		Name:  "Styled",
		Props: []ir.Arg{{Name: "content", Value: irStr("x")}},
	}
	if bp := extractBlueprint(nb); bp.Focus {
		t.Fatalf("unexpected focus")
	}
}

func TestExtractWidget(t *testing.T) {
	n := &ir.NodeInst{
		Name: "Widget",
		Props: []ir.Arg{
			{Name: "model", Value: irStruct(map[string]ir.Expr{
				"type": irStr("textinput.Model"),
				"new":  irStr("textinput.New()"),
				"view": irStr(".View()"),
				"pkg":  irStr("charm.land/bubbles/v2/textinput"),
			})},
			{Name: "focus", Value: irStruct(map[string]ir.Expr{"enabled": irBool(true)})},
			{Name: "binds", Value: &ir.ListLit{Elems: []ir.Expr{
				irStruct(map[string]ir.Expr{
					"prop": irStr("value"),
					"get":  irStr(".Value()"),
				}),
			}}},
			{Name: "events", Value: &ir.ListLit{Elems: []ir.Expr{
				irStruct(map[string]ir.Expr{
					"on":  irStr("submit"),
					"key": irStr("enter"),
				}),
			}}},
		},
	}
	bp := extractBlueprint(n)
	if bp.Kind != bpWidget {
		t.Fatalf("kind=%v, want bpWidget", bp.Kind)
	}
	if bp.Model.Type != "textinput.Model" {
		t.Fatalf("type=%q", bp.Model.Type)
	}
	if bp.Model.New != "textinput.New()" || bp.Model.View != ".View()" {
		t.Fatalf("model=%+v", bp.Model)
	}
	if bp.Model.Pkg != "charm.land/bubbles/v2/textinput" {
		t.Fatalf("pkg=%q", bp.Model.Pkg)
	}
	if !bp.Focus {
		t.Fatalf("expected focusable")
	}
	if len(bp.Binds) != 1 || bp.Binds[0].Prop != "value" || bp.Binds[0].Get != ".Value()" {
		t.Fatalf("binds=%+v", bp.Binds)
	}
	if len(bp.Events) != 1 || bp.Events[0].On != "submit" || bp.Events[0].Key != "enter" {
		t.Fatalf("events=%+v", bp.Events)
	}
}
