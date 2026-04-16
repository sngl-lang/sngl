package ir

import "git.duckfam.us/jonathan/sngl/ast"

// Expr is a type-checked expression node. Every Expr carries its resolved
// type. Concrete types have an AST field for source positions.
type Expr interface {
	exprNode()
	ExprType() *Type
}

// Literal is a constant value: int, float, string, bool, null, color, unit.
type Literal struct {
	AST    *ast.LiteralExpr
	Type   *Type
	Raw    string // source text for codegen
	Suffix string // unit suffix ("px") if unit; empty otherwise
}

// Ident is a resolved identifier reference.
type Ident struct {
	AST    *ast.IdentExpr
	Type   *Type
	Sym    Symbol // resolved: *Var, *Func, *Param, *LoopVar, etc.
	Member string // non-empty for bare enum member ("active" → Status.active)
}

// Binary is a binary operation.
type Binary struct {
	AST   *ast.BinaryExpr // nil for synthetic (desugared interpolation)
	Type  *Type
	Op    ast.BinaryOp
	Left  Expr
	Right Expr
}

// Unary is a unary operation.
type Unary struct {
	AST     *ast.UnaryExpr
	Type    *Type
	Op      ast.UnaryOp
	Operand Expr
}

// Ternary is a conditional expression: cond ? then : else.
type Ternary struct {
	AST  *ast.TernaryExpr
	Type *Type
	Cond Expr
	Then Expr
	Else Expr
}

// Call is a resolved function or method call.
//   - Plain function: Func set, Receiver nil
//   - Instance method: Func set, Receiver set (expr.method(args))
//   - Namespace call: Func set, resolved through namespace
type Call struct {
	AST      *ast.CallExpr
	Type     *Type     // return type
	Func     *Func     // resolved function (nil for unresolved/dynamic)
	Receiver Expr      // non-nil for instance method calls
	Args     []CallArg // resolved arguments
}

// CallArg is a resolved argument in a function call.
type CallArg struct {
	Name  string // empty for positional
	Value Expr
}

// Conversion is a builtin type coercion: int(x), float(x), string(x), bool(x).
type Conversion struct {
	AST     *ast.CallExpr
	Type    *Type // target type
	Operand Expr
}

// Select is a resolved field or member access.
type Select struct {
	AST     *ast.SelectExpr
	Type    *Type
	Operand Expr
	Field   string
}

// Index is a resolved index operation: operand[index].
type Index struct {
	AST     *ast.IndexExpr
	Type    *Type
	Operand Expr
	Idx     Expr
}

// StructLit is a resolved struct literal.
type StructLit struct {
	AST    *ast.StructExpr
	Type   *Type
	Def    *StructDef // resolved struct definition (nil if anonymous/unresolved)
	Fields []FieldInit
}

// FieldInit is a field in a struct literal.
type FieldInit struct {
	Name   string // empty for spread
	Value  Expr
	Spread bool
}

// ListLit is a resolved list literal.
type ListLit struct {
	AST   *ast.ListExpr
	Type  *Type
	Elems []Expr
}

// Spread is a spread operation: ...expr.
type Spread struct {
	AST     *ast.SpreadExpr
	Type    *Type
	Operand Expr
}

// Lambda is a resolved function literal / closure.
type Lambda struct {
	AST  *ast.LambdaExpr
	Type *Type
	Func *Func // resolved params, return type, checked body
}

// --- Expr interface implementations ---

func (*Literal) exprNode()    {}
func (*Ident) exprNode()      {}
func (*Binary) exprNode()     {}
func (*Unary) exprNode()      {}
func (*Ternary) exprNode()    {}
func (*Call) exprNode()       {}
func (*Conversion) exprNode() {}
func (*Select) exprNode()     {}
func (*Index) exprNode()      {}
func (*StructLit) exprNode()  {}
func (*ListLit) exprNode()    {}
func (*Spread) exprNode()     {}
func (*Lambda) exprNode()     {}

func (x *Literal) ExprType() *Type    { return x.Type }
func (x *Ident) ExprType() *Type      { return x.Type }
func (x *Binary) ExprType() *Type     { return x.Type }
func (x *Unary) ExprType() *Type      { return x.Type }
func (x *Ternary) ExprType() *Type    { return x.Type }
func (x *Call) ExprType() *Type       { return x.Type }
func (x *Conversion) ExprType() *Type { return x.Type }
func (x *Select) ExprType() *Type     { return x.Type }
func (x *Index) ExprType() *Type      { return x.Type }
func (x *StructLit) ExprType() *Type  { return x.Type }
func (x *ListLit) ExprType() *Type    { return x.Type }
func (x *Spread) ExprType() *Type     { return x.Type }
func (x *Lambda) ExprType() *Type     { return x.Type }
