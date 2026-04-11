package ast

// Expr represents any SNGL expression
// The unexported method prevents external implementations.
type Expr interface {
	ExprPos() *Pos
}

// TypeExpr is an expression for a type, named or anonymous.
type TypeExpr interface {
	Expr
}

// TargetExpr is an expression for an addressable target value.
type TargetExpr interface {
	Expr
}

// --- Enums ---

// LiteralKind identifies the type of a literal value.
type LiteralKind int

const (
	LiteralInt LiteralKind = iota
	LiteralFloat
	LiteralStringQuoted
	LiteralStringBackticked
	LiteralStringTrippleQuoted
	LiteralBool
	LiteralNull
	LiteralColor
	LiteralUnit
)

// BinaryOp identifies a binary operator.
type BinaryOp int

const (
	BinAdd BinaryOp = iota
	BinSub
	BinMul
	BinDiv
	BinMod
	BinEq
	BinNeq
	BinLt
	BinLte
	BinGt
	BinGte
	BinAnd
	BinOr
)

// UnaryOp identifies a unary operator.
type UnaryOp int

const (
	UnaryNot UnaryOp = iota
	UnaryNeg
)

// AssignOp identifies an assignment operator.
type AssignOp int

const (
	AssignSet AssignOp = iota // =
	AssignAdd                 // +=
	AssignSub                 // -=
	AssignMul                 // *=
	AssignDiv                 // /=
	AssignMod                 // %=
)

// --- Expressions ---

// LiteralExpr is a literal value: int, float, string, bool, nil, color, unit.
type LiteralExpr struct {
	Pos
	Kind LiteralKind
	Raw  string // original source text
}

// UnitLiteral is the value stored in a LiteralExpr with Kind == LiteralUnit.
type UnitLiteral struct {
	Pos
	LiteralExpr
	Suffix string // "px", "ms", "em"
}

// IdentExpr is an identifier reference.
type IdentExpr struct {
	Pos
	Name string
}

// BinaryExpr is a binary operation.
type BinaryExpr struct {
	Pos
	Op    BinaryOp
	Left  Expr
	Right Expr
}

// UnaryExpr is a unary operation.
type UnaryExpr struct {
	Pos
	Op      UnaryOp
	Operand Expr
}

// TernaryExpr is a ternary conditional: cond ? then : else.
type TernaryExpr struct {
	Pos
	Cond Expr
	Then Expr
	Else Expr
}

// SelectExpr is field access: operand.field.
type SelectExpr struct {
	Pos
	Operand      Expr
	Field        string
	ResolvedType string // type hint populated by checker, e.g. "string"
}

// IndexExpr is index access: operand[index].
type IndexExpr struct {
	Pos
	Operand      Expr
	Index        Expr
	ResolvedType string // element type populated by checker, e.g. "docs.Component"
}

// CallExpr is a function call: func(args...).
type CallExpr struct {
	Pos
	Func string
	Args []Expr
}

// MethodExpr is a method call: receiver.method(args...).
type MethodExpr struct {
	Pos
	Receiver Expr
	Method   string
	Args     []Expr
	Resolved string // qualified name set by checker, e.g. "regex.matches"
}

// StructFieldLit is a field in a struct literal.
type StructFieldLit struct {
	Name   string
	Value  Expr
	Spread bool // if true, Value is the spread operand, Name is empty
}

// StructExpr is a struct literal: Name{field: value, ...expr}.
type StructExpr struct {
	Pos
	Name      string
	Fields    []StructFieldLit
	Multiline bool // true when fields span multiple lines in source
}

// ListExpr is a list literal: [a, b, ...c].
type ListExpr struct {
	Pos
	Elements []Expr
}

// SpreadExpr represents a spread operation: ...expr.
type SpreadExpr struct {
	Pos
	Operand Expr
}

// InterpolationExpr is a string with interpolated expressions.
// Parts alternate between *LiteralExpr (string) and expression nodes.
type InterpolationExpr struct {
	Pos
	Parts []Expr
	Style StringStyle // quoting style (StyleDouble or StyleTriple)
}

// ElementRefExpr references a visual element by its #id.
type ElementRefExpr struct {
	Pos
	Name string
}

// LambdaExpr is an inline function: (t) => t.done, (a, b) => a + b,
// or a block-body anonymous function: func(t, c) { stmts }.
// Parameter types are optional — inferred from context when omitted.
// Exactly one of Body or Block is set.
type LambdaExpr struct {
	Pos
	Params     []string   // parameter names
	ParamTypes []string   // optional type hints (empty string = inferred)
	Body       Expr       // expression body (arrow form)
	Block      *FuncBlock // block body (func(params) { ... } form)
}

// ParenExpr preserves explicit parentheses in the source: (expr).
type ParenExpr struct {
	Pos
	Inner Expr
}

// --- Statements ---

// AssignStmt is an assignment: target op= value.
type AssignStmt struct {
	Pos
	Target Expr
	Op     AssignOp
	Value  Expr
}

// ToggleStmt is a boolean toggle: target!!.
type ToggleStmt struct {
	Pos
	Target Expr
}

// EmitStmt is an event emission: @name(args...).
type EmitStmt struct {
	Pos
	Name string
	Args []Expr
}

// StmtBlock is a list of statements: { stmt; stmt }.
type StmtBlock struct {
	Pos
	IsMultiline bool
	Stmts       []Expr
}

func (x StmtBlock) IsDefined() bool { return x.Pos.IsSet() }

// VarStmt is a local variable declaration inside a function body.
type VarStmt struct {
	Pos
	Name string
	Type string // optional type hint
	Init Expr   // initializer expression
}

// ReturnStmt is a return statement in a block function.
type ReturnStmt struct {
	Pos
	Value Expr // nil for bare return
}

// CallStmt wraps a CallExpr used as a statement (for void function calls).
type CallStmt struct {
	Pos
	Call *CallExpr
}

// --- Expr interface implementations ---

func (x *LiteralExpr) ExprPos() *Pos       { return &x.Pos }
func (x *IdentExpr) ExprPos() *Pos         { return &x.Pos }
func (x *BinaryExpr) ExprPos() *Pos        { return &x.Pos }
func (x *UnaryExpr) ExprPos() *Pos         { return &x.Pos }
func (x *TernaryExpr) ExprPos() *Pos       { return &x.Pos }
func (x *SelectExpr) ExprPos() *Pos        { return &x.Pos }
func (x *IndexExpr) ExprPos() *Pos         { return &x.Pos }
func (x *CallExpr) ExprPos() *Pos          { return &x.Pos }
func (x *MethodExpr) ExprPos() *Pos        { return &x.Pos }
func (x *StructExpr) ExprPos() *Pos        { return &x.Pos }
func (x *ListExpr) ExprPos() *Pos          { return &x.Pos }
func (x *InterpolationExpr) ExprPos() *Pos { return &x.Pos }
func (x *ElementRefExpr) ExprPos() *Pos    { return &x.Pos }
func (x *LambdaExpr) ExprPos() *Pos        { return &x.Pos }
func (x *ParenExpr) ExprPos() *Pos         { return &x.Pos }
func (x *AssignStmt) StmtPos() *Pos        { return &x.Pos }
func (x *ToggleStmt) StmtPos() *Pos        { return &x.Pos }
func (x *EmitStmt) StmtPos() *Pos          { return &x.Pos }
func (x *StmtBlock) ExprPos() *Pos         { return &x.Pos }
func (x *VarStmt) StmtPos() *Pos           { return &x.Pos }
func (x *ReturnStmt) StmtPos() *Pos        { return &x.Pos }
func (x *CallStmt) StmtPos() *Pos          { return &x.Pos }
