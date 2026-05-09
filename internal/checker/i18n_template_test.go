package checker

import (
	"testing"

	"git.duckfam.us/jonathan/sngl/ast"
)

func TestTemplateBuilderPlain(t *testing.T) {
	b := &templateBuilder{}
	b.writeParts([]ast.Expr{
		&ast.LiteralExpr{Raw: "Login", Kind: ast.LiteralStringQuoted},
	})
	if got := string(b.sb); got != "Login" {
		t.Errorf("template = %q, want Login", got)
	}
	if len(b.args) != 0 {
		t.Errorf("args = %v, want empty", b.args)
	}
}

func TestTemplateBuilderSimplePlaceholder(t *testing.T) {
	b := &templateBuilder{}
	b.writeParts([]ast.Expr{
		&ast.LiteralExpr{Raw: "Hello, ", Kind: ast.LiteralStringQuoted},
		&ast.I18nPlaceholderExpr{
			Value: &ast.IdentExpr{Name: "name"},
		},
		&ast.LiteralExpr{Raw: "!", Kind: ast.LiteralStringQuoted},
	})
	if got := string(b.sb); got != "Hello, {name}!" {
		t.Errorf("template = %q, want %q", got, "Hello, {name}!")
	}
	if len(b.args) != 1 || b.args[0].name != "name" {
		t.Errorf("args = %v, want [{name name}]", b.args)
	}
}

func TestTemplateBuilderComplexExprPlaceholder(t *testing.T) {
	// {user.name} — non-ident value gets synthetic arg0.
	b := &templateBuilder{}
	b.writeParts([]ast.Expr{
		&ast.I18nPlaceholderExpr{
			Value: &ast.SelectExpr{
				Operand: &ast.IdentExpr{Name: "user"},
				Field:   "name",
			},
		},
	})
	if got := string(b.sb); got != "{arg0}" {
		t.Errorf("template = %q, want {arg0}", got)
	}
	if len(b.args) != 1 || b.args[0].name != "arg0" {
		t.Errorf("args = %v, want [{arg0 ...}]", b.args)
	}
}

func TestTemplateBuilderPlural(t *testing.T) {
	b := &templateBuilder{}
	b.writeParts([]ast.Expr{
		&ast.LiteralExpr{Raw: "You have ", Kind: ast.LiteralStringQuoted},
		&ast.I18nPlaceholderExpr{
			Value: &ast.IdentExpr{Name: "count"},
			Type:  "plural",
			Cases: []ast.I18nCase{
				{Selector: "one", Body: []ast.Expr{
					&ast.LiteralExpr{Raw: "message", Kind: ast.LiteralStringQuoted},
				}},
				{Selector: "other", Body: []ast.Expr{
					&ast.LiteralExpr{Raw: "messages", Kind: ast.LiteralStringQuoted},
				}},
			},
		},
	})
	want := "You have {count, plural, one{message} other{messages}}"
	if got := string(b.sb); got != want {
		t.Errorf("template = %q\nwant   %q", got, want)
	}
	if len(b.args) != 1 || b.args[0].name != "count" {
		t.Errorf("args = %v, want [{count ...}]", b.args)
	}
}
