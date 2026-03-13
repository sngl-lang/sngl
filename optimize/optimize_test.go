package optimize

import (
	"testing"

	"git.duckfam.us/jonathan/sngl/ast"
	"github.com/google/cel-go/cel"
)

// celExpr creates a checked CEL Expr using a minimal env with PLATFORM and LANGUAGE.
func celExpr(t *testing.T, src string) ast.Expr {
	t.Helper()
	env, err := cel.NewEnv(
		cel.Variable("PLATFORM", cel.StringType),
		cel.Variable("LANGUAGE", cel.StringType),
	)
	if err != nil {
		t.Fatalf("cel env: %v", err)
	}
	parsed, iss := env.Parse(src)
	if iss != nil && iss.Err() != nil {
		t.Fatalf("cel parse %q: %v", src, iss.Err())
	}
	checked, iss := env.Check(parsed)
	if iss != nil && iss.Err() != nil {
		t.Fatalf("cel check %q: %v", src, iss.Err())
	}
	return ast.Expr{CEL: src, AST: checked}
}

// celExprWithVars creates a checked CEL Expr using an env with extra variables.
func celExprWithVars(t *testing.T, src string, extras ...cel.EnvOption) ast.Expr {
	t.Helper()
	opts := []cel.EnvOption{
		cel.Variable("PLATFORM", cel.StringType),
		cel.Variable("LANGUAGE", cel.StringType),
	}
	opts = append(opts, extras...)
	env, err := cel.NewEnv(opts...)
	if err != nil {
		t.Fatalf("cel env: %v", err)
	}
	parsed, iss := env.Parse(src)
	if iss != nil && iss.Err() != nil {
		t.Fatalf("cel parse %q: %v", src, iss.Err())
	}
	checked, iss := env.Check(parsed)
	if iss != nil && iss.Err() != nil {
		t.Fatalf("cel check %q: %v", src, iss.Err())
	}
	return ast.Expr{CEL: src, AST: checked}
}

func TestIfTrue_Kept(t *testing.T) {
	ifExpr := celExpr(t, "PLATFORM == 'html'")
	doc := &ast.Document{
		App: &ast.App{
			Children: []*ast.VisualNode{{
				Component: "div",
				If:        &ifExpr,
			}},
		},
	}
	Optimize(doc, Config{Platform: "html", Language: "js"})
	if len(doc.App.Children) != 1 {
		t.Fatalf("expected 1 child, got %d", len(doc.App.Children))
	}
	if doc.App.Children[0].If != nil {
		t.Errorf("expected If to be nil (guard removed), got %+v", doc.App.Children[0].If)
	}
}

func TestIfFalse_Removed(t *testing.T) {
	ifExpr := celExpr(t, "PLATFORM == 'html'")
	doc := &ast.Document{
		App: &ast.App{
			Children: []*ast.VisualNode{{
				Component: "div",
				If:        &ifExpr,
			}},
		},
	}
	Optimize(doc, Config{Platform: "bubbletea", Language: "go"})
	if len(doc.App.Children) != 0 {
		t.Fatalf("expected 0 children (dead branch), got %d", len(doc.App.Children))
	}
}

func TestNonConstIf_Unchanged(t *testing.T) {
	ifExpr := celExprWithVars(t, "PLATFORM == 'html' && count > 0",
		cel.Variable("count", cel.IntType))
	origCEL := ifExpr.CEL
	origAST := ifExpr.AST
	doc := &ast.Document{
		App: &ast.App{
			Children: []*ast.VisualNode{{
				Component: "div",
				If:        &ifExpr,
			}},
		},
	}
	Optimize(doc, Config{Platform: "html", Language: "js"})
	if len(doc.App.Children) != 1 {
		t.Fatalf("expected 1 child, got %d", len(doc.App.Children))
	}
	node := doc.App.Children[0]
	if node.If == nil {
		t.Fatal("expected If to remain, got nil")
	}
	if node.If.CEL != origCEL {
		t.Errorf("expected CEL %q, got %q", origCEL, node.If.CEL)
	}
	if node.If.AST != origAST {
		t.Error("expected AST pointer unchanged")
	}
}

func TestPropFolded(t *testing.T) {
	doc := &ast.Document{
		App: &ast.App{
			Children: []*ast.VisualNode{{
				Component: "div",
				Props: map[string]ast.Expr{
					"platform": celExpr(t, "PLATFORM"),
				},
			}},
		},
	}
	Optimize(doc, Config{Platform: "html", Language: "js"})
	e := doc.App.Children[0].Props["platform"]
	if e.CEL != "" {
		t.Errorf("expected CEL cleared, got %q", e.CEL)
	}
	if e.Literal != "html" {
		t.Errorf("expected literal 'html', got %v", e.Literal)
	}
}

func TestComputedFolded(t *testing.T) {
	expr := celExpr(t, "PLATFORM + '/' + LANGUAGE")
	doc := &ast.Document{
		Computeds: []*ast.Computed{{
			Name: "target",
			Expr: expr,
		}},
	}
	Optimize(doc, Config{Platform: "html", Language: "js"})
	e := doc.Computeds[0].Expr
	if e.CEL != "" {
		t.Errorf("expected CEL cleared, got %q", e.CEL)
	}
	if e.Literal != "html/js" {
		t.Errorf("expected literal 'html/js', got %v", e.Literal)
	}
}

func TestNestedChildrenRemoved(t *testing.T) {
	ifExpr := celExpr(t, "PLATFORM == 'html'")
	doc := &ast.Document{
		App: &ast.App{
			Children: []*ast.VisualNode{{
				Component: "div",
				If:        &ifExpr,
				Children: []*ast.VisualNode{
					{Component: "span"},
					{Component: "p"},
				},
			}},
		},
	}
	Optimize(doc, Config{Platform: "bubbletea", Language: "go"})
	if len(doc.App.Children) != 0 {
		t.Fatalf("expected 0 children, got %d", len(doc.App.Children))
	}
}

func TestCloneIsolation(t *testing.T) {
	ifExpr := celExpr(t, "PLATFORM == 'html'")
	doc := &ast.Document{
		App: &ast.App{
			Children: []*ast.VisualNode{{
				Component: "div",
				If:        &ifExpr,
			}},
		},
	}
	cloned := doc.Clone()
	Optimize(cloned, Config{Platform: "bubbletea", Language: "go"})

	// Original should be untouched.
	if len(doc.App.Children) != 1 {
		t.Fatalf("original mutated: expected 1 child, got %d", len(doc.App.Children))
	}
	if doc.App.Children[0].If == nil {
		t.Error("original If was cleared")
	}
	if doc.App.Children[0].If.CEL == "" {
		t.Error("original CEL was cleared")
	}

	// Clone should have the branch eliminated.
	if len(cloned.App.Children) != 0 {
		t.Fatalf("clone: expected 0 children, got %d", len(cloned.App.Children))
	}
}
