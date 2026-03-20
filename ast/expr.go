package ast

// Expr is either a literal value or a SNGL expression.
type Expr struct {
	Literal  any    // non-nil for static values
	SNGL     Node   // native SNGL expression tree
	TypeHint string // type annotation: "int", "bool", "User", etc.
}

// ForClause is the parsed form of for="item in items" or for="item, index in items".
type ForClause struct {
	Variable string // element variable, e.g. "item"
	IndexVar string // optional index variable, e.g. "index"
	Iterable Expr
}
