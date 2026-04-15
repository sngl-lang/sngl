package codegen

import (
	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/ir"
)

// ExprTranslator translates typed SNGL expressions into target language source.
// Each method receives an ExprCtx that provides type info and symbol resolution
// from the checker's Package.
type ExprTranslator interface {
	Lang() string
	ir.Language

	// Expr translates an expression to target language source.
	Expr(e ast.Expr, ctx *ExprCtx) string

	// Mutation translates a mutation statement (assign, toggle, call, emit)
	// into one or more target-language statements.
	Mutation(s ast.Stmt, ctx *ExprCtx) []string

	// Literal translates a compile-time constant expression.
	Literal(e ast.Expr) string

	// TypeName maps a checker Type to the target language's type syntax.
	TypeName(t *ir.Type) string

	// ExportName applies the target language's export convention to a name.
	ExportName(name string) string
}
