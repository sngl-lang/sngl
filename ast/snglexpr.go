package ast

// Node is the interface for all SNGL expression and statement AST nodes.
// The unexported method prevents external implementations.
type Node interface {
	snglNode()
}

// --- Enums ---

// LiteralKind identifies the type of a literal value.
type LiteralKind int

const (
	LiteralInt LiteralKind = iota
	LiteralFloat
	LiteralString
	LiteralBool
	LiteralNull
	LiteralColor
	LiteralDuration
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

// LiteralExpr is a literal value: int, float, string, bool, nil, color, duration.
type LiteralExpr struct {
	Value any
	Kind  LiteralKind
}

// IdentExpr is an identifier reference.
type IdentExpr struct {
	Name string
}

// BinaryExpr is a binary operation.
type BinaryExpr struct {
	Op    BinaryOp
	Left  Node
	Right Node
}

// UnaryExpr is a unary operation.
type UnaryExpr struct {
	Op      UnaryOp
	Operand Node
}

// TernaryExpr is a ternary conditional: cond ? then : else.
type TernaryExpr struct {
	Cond Node
	Then Node
	Else Node
}

// SelectExpr is field access: operand.field.
type SelectExpr struct {
	Operand Node
	Field   string
}

// IndexExpr is index access: operand[index].
type IndexExpr struct {
	Operand Node
	Index   Node
}

// CallExpr is a function call: func(args...).
type CallExpr struct {
	Func string
	Args []Node
}

// MethodExpr is a method call: receiver.method(args...).
type MethodExpr struct {
	Receiver Node
	Method   string
	Args     []Node
}

// StructFieldLit is a field in a struct literal.
type StructFieldLit struct {
	Name  string
	Value Node
}

// StructExpr is a struct literal: Name{field: value, ...}.
type StructExpr struct {
	Name   string
	Fields []StructFieldLit
}

// ListExpr is a list literal: [a, b, c].
type ListExpr struct {
	Elements []Node
}

// InterpolationExpr is a string with interpolated expressions.
// Parts alternate between *LiteralExpr (string) and expression nodes.
type InterpolationExpr struct {
	Parts []Node
}

// --- Statements ---

// AssignStmt is an assignment: target op= value.
type AssignStmt struct {
	Target Node
	Op     AssignOp
	Value  Node
}

// ToggleStmt is a boolean toggle: target!!.
type ToggleStmt struct {
	Target Node
}

// EmitStmt is an event emission: @name(args...).
type EmitStmt struct {
	Name string
	Args []Node
}

// StmtBlock is a list of statements: { stmt; stmt }.
type StmtBlock struct {
	Stmts []Node
}

// --- Node interface implementations ---

func (*LiteralExpr) snglNode()       {}
func (*IdentExpr) snglNode()         {}
func (*BinaryExpr) snglNode()        {}
func (*UnaryExpr) snglNode()         {}
func (*TernaryExpr) snglNode()       {}
func (*SelectExpr) snglNode()        {}
func (*IndexExpr) snglNode()         {}
func (*CallExpr) snglNode()          {}
func (*MethodExpr) snglNode()        {}
func (*StructExpr) snglNode()        {}
func (*ListExpr) snglNode()          {}
func (*InterpolationExpr) snglNode() {}
func (*AssignStmt) snglNode()        {}
func (*ToggleStmt) snglNode()        {}
func (*EmitStmt) snglNode()          {}
func (*StmtBlock) snglNode()         {}
