package expand

import (
	"testing"

	"git.duckfam.us/jonathan/sngl/ast"
)

func str(s string) ast.Expr  { return &ast.LiteralExpr{Kind: ast.LiteralStringQuoted, Raw: s} }
func num(s string) ast.Expr  { return &ast.LiteralExpr{Kind: ast.LiteralInt, Raw: s} }
func name(s string) ast.Expr { return &ast.IdentExpr{Name: s} }

func TestEvalArgsHappyPath(t *testing.T) {
	params := []Param{
		{Name: "kind", Kind: ArgString},
		{Name: "count", Kind: ArgInt},
		{Name: "target", Kind: ArgIdent},
		{Name: "extra", Kind: ArgString, Optional: true},
	}
	args, err := evalArgs(params, []ast.Expr{str("color"), num("3"), name("box")})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := args.String("kind"); got != "color" {
		t.Errorf(`String("kind") = %q, want "color"`, got)
	}
	if got := args.Int("count"); got != 3 {
		t.Errorf(`Int("count") = %d, want 3`, got)
	}
	if got := args.Ident("target"); got != "box" {
		t.Errorf(`Ident("target") = %q, want "box"`, got)
	}
	if args.Has("extra") {
		t.Errorf(`Has("extra") = true, want false (omitted optional)`)
	}
}

func TestEvalArgsArity(t *testing.T) {
	params := []Param{{Name: "a", Kind: ArgString}, {Name: "b", Kind: ArgString, Optional: true}}
	// too few (0 < 1 required)
	if _, err := evalArgs(params, nil); err == nil {
		t.Error("expected arity error for too few arguments")
	}
	// too many (3 > 2 total)
	if _, err := evalArgs(params, []ast.Expr{str("x"), str("y"), str("z")}); err == nil {
		t.Error("expected arity error for too many arguments")
	}
	// exactly required is fine
	if _, err := evalArgs(params, []ast.Expr{str("x")}); err != nil {
		t.Errorf("unexpected error at min arity: %v", err)
	}
}

func TestEvalArgsKindMismatch(t *testing.T) {
	params := []Param{{Name: "kind", Kind: ArgString}}
	// a bare ident where a constant string is required
	if _, err := evalArgs(params, []ast.Expr{name("color")}); err == nil {
		t.Error("expected kind error: ident where ArgString required")
	}
	intParams := []Param{{Name: "n", Kind: ArgInt}}
	if _, err := evalArgs(intParams, []ast.Expr{str("5")}); err == nil {
		t.Error("expected kind error: string where ArgInt required")
	}
}
