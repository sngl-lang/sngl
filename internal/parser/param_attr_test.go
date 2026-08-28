package parser

import (
	"testing"

	"git.duckfam.us/jonathan/sngl/ast"
)

// findFunc returns the named top-level function.
func findFunc(t *testing.T, doc *ast.Document, name string) *ast.FuncDef {
	t.Helper()
	for _, s := range doc.Stmts {
		if fn, ok := s.(*ast.FuncDef); ok && fn.Name == name {
			return fn
		}
	}
	t.Fatalf("func %q not found", name)
	return nil
}

func findComponent(t *testing.T, doc *ast.Document, name string) *ast.ComponentDecl {
	t.Helper()
	for _, s := range doc.Stmts {
		if c, ok := s.(*ast.ComponentDecl); ok && c.Name == name {
			return c
		}
	}
	t.Fatalf("component %q not found", name)
	return nil
}

func attrNames(attrs []ast.MacroAttr) []string {
	var out []string
	for _, a := range attrs {
		n := a.Name
		if a.Alias != "" {
			n = a.Alias + "." + n
		}
		out = append(out, n)
	}
	return out
}

func TestParseParamAttrs(t *testing.T) {
	src := `func f(#[some.mark("x")] a string, b int) { }

component element(
    tag string,
    #[some.mark("^data-")] attributes map<string, string>,
) {}
`
	doc, err := Parse("test.sngl", []byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	fn := findFunc(t, doc, "f")
	if len(fn.Params.Params) != 2 {
		t.Fatalf("params = %d, want 2", len(fn.Params.Params))
	}
	a := fn.Params.Params[0]
	if got := attrNames(a.Attrs); len(got) != 1 || got[0] != "some.mark" {
		t.Errorf("param a attrs = %v, want [some.mark]", got)
	}
	if len(a.Attrs) == 1 {
		lit, ok := a.Attrs[0].Args[0].(*ast.LiteralExpr)
		if !ok || lit.Raw != "x" {
			t.Errorf("param a attr arg = %#v, want string \"x\"", a.Attrs[0].Args[0])
		}
	}
	if b := fn.Params.Params[1]; len(b.Attrs) != 0 {
		t.Errorf("param b attrs = %v, want none", attrNames(b.Attrs))
	}

	c := findComponent(t, doc, "element")
	if len(c.Props.Props) != 2 {
		t.Fatalf("props = %d, want 2", len(c.Props.Props))
	}
	if p := c.Props.Props[0].(ast.Param); len(p.Attrs) != 0 {
		t.Errorf("prop tag attrs = %v, want none", attrNames(p.Attrs))
	}
	p := c.Props.Props[1].(ast.Param)
	if p.Name != "attributes" {
		t.Fatalf("prop name = %q", p.Name)
	}
	if got := attrNames(p.Attrs); len(got) != 1 || got[0] != "some.mark" {
		t.Errorf("prop attributes attrs = %v, want [some.mark]", got)
	}
}

// A mark is legal on every CompParam form the grammar hoists it over, the
// event form included: #[wildcard] says which event names a component answers
// to as well as which prop names.
func TestParseParamAttrForms(t *testing.T) {
	tests := []struct {
		name    string
		src     string
		wantErr bool
	}{
		{"bare", `component c(#[m] a) {}`, false},
		{"typed", `component c(#[m] a int) {}`, false},
		{"default", `component c(#[m] a = 1) {}`, false},
		{"binding", `component c(#[m] :a int) {}`, false},
		{"multiple", `component c(#[m] #[n("x")] a int) {}`, false},
		{"lambda", `func f() => func(#[m] a int) => a`, false},
		{"event", `component c(#[m] @click) {}`, false},
		{"event typed", `component c(#[m] @click E) {}`, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse("test.sngl", []byte(tt.src))
			if tt.wantErr {
				if err == nil {
					t.Fatalf("parse %q: want error", tt.src)
				}
				return
			}
			if err != nil {
				t.Fatalf("parse %q: %v", tt.src, err)
			}
		})
	}
}

func TestFormatParamAttrs(t *testing.T) {
	// A param list written across lines still formats on one line: nothing
	// sets ParamList.IsMultiline from source, so the attr goes inline.
	assertFormat(t,
		`func f(#[some.mark("x")] a string, b int) { }`,
		`func f(#[some.mark("x")] a string, b int) {}`)

	assertFormat(t,
		"component element(\n    tag string,\n    #[some.mark(\"^data-\")] attributes map<string, string>,\n) {}",
		`component element(tag string, #[some.mark("^data-")] attributes map<string, string>) {}`)

	assertFormat(t,
		`component c(#[m] #[n(1)] :a int = 2) {}`,
		`component c(#[m] #[n(1)] :a int = 2) {}`)

	assertFormat(t,
		`func f() => func(#[m] a int) => a`,
		`func f() => func(#[m] a int) => a`)
}

// The multiline branch is unreachable from source today, so drive it from a
// hand-built AST: an attr belongs on its own line above its param there.
func TestFormatParamAttrsMultiline(t *testing.T) {
	mark := ast.MacroAttr{Name: "mark", Args: []ast.Expr{
		&ast.LiteralExpr{Kind: ast.LiteralStringQuoted, Raw: "^data-"},
	}}
	fn := &ast.FuncDef{
		Name: "f",
		Params: ast.ParamList{
			IsMultiline: true,
			Params: []ast.Param{
				{Name: "tag", Type: &ast.NamedType{Name: "string"}},
				{Name: "attributes", Type: &ast.NamedType{Name: "string"}, Attrs: []ast.MacroAttr{mark}},
			},
		},
	}
	doc := &ast.Document{Stmts: []ast.Stmt{fn}}
	const want = "func f(\n    tag string,\n    #[mark(\"^data-\")]\n    attributes string,\n) {}\n"
	if got := Format(doc); got != want {
		t.Errorf("format:\ngot:  %q\nwant: %q", got, want)
	}
}
