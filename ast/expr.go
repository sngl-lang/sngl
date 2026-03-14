package ast

import "github.com/google/cel-go/cel"

// Expr is either a literal value or a CEL expression.
type Expr struct {
	Literal  any      // non-nil for static values
	CEL      string   // non-empty for (cel)"..." expressions
	AST      *cel.Ast // parsed CEL AST (nil for literals)
	SNGL     Node     // native SNGL expression tree (populated by both parsers)
	TypeHint string   // from KDL type annotation: "int", "bool", "User", etc.
}

// ForClause is the parsed form of for="item in items" or for="item, index in items".
type ForClause struct {
	Variable string // element variable, e.g. "item"
	IndexVar string // optional index variable, e.g. "index"
	Iterable Expr
}
