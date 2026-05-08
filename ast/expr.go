package ast

// Expr represents any SNGL expression.
type Expr interface {
	ExprPos() *Pos
}

// TypeExpr is a type expression: named, qualified, generic, function,
// or anonymous declaration (StructDef, EnumDef, UnitDef).
type TypeExpr interface {
	Expr
	typeExpr()
}

// TargetExpr is an expression for an addressable target value.
type TargetExpr interface {
	Expr
	targetExpr()
}

// --- Enums ---

//go:generate go tool stringer -type=LiteralKind -trimprefix Literal

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

//go:generate go tool stringer -type=StringStyle -linecomment

// StringStyle identifies the quoting style of a string literal.
type StringStyle int

const (
	StyleDouble StringStyle = iota // "..."
	StyleTriple                    // """..."""
	StyleRaw                       // `...`
)

//go:generate go tool stringer -type=BinaryOp -linecomment

// BinaryOp identifies a binary operator.
type BinaryOp int

const (
	BinAdd BinaryOp = iota // +
	BinSub                 // -
	BinMul                 // *
	BinDiv                 // /
	BinMod                 // %
	BinEq                  // ==
	BinNeq                 // !=
	BinLt                  // <
	BinLte                 // <=
	BinGt                  // >
	BinGte                 // >=
	BinAnd                 // &&
	BinOr                  // ||
)

//go:generate go tool stringer -type=UnaryOp -linecomment

// UnaryOp identifies a unary operator.
type UnaryOp int

const (
	UnaryNot   UnaryOp = iota // !
	UnaryNeg                  // -
	UnaryAddr                 // &
	UnaryDeref                // *
)

//go:generate go tool stringer -type=AssignOp -linecomment

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

//go:generate go tool stringer -type=SelectKind -linecomment

// SelectKind identifies the kind of member access in a SelectExpr.
type SelectKind int

const (
	SelectField   SelectKind = iota // field
	SelectEvent                     // @event
	SelectElemRef                   // #ref
)

// --- Type expressions ---

// NamedType is a type reference: int, pkg.Type, List<int>.
type NamedType struct {
	Pos      Pos
	Name     string     // type name
	Package  string     // qualifier in pkg.Type (empty if unqualified)
	TypeArgs []TypeExpr // generic arguments: List<int>, Map<string, int> (nil if not generic)
}

// FuncType is a function type: func(int, string) -> bool.
type FuncType struct {
	Pos    Pos
	Params []TypeExpr // parameter types
	Return TypeExpr   // nil for void
}

// StructDef, EnumDef, and UnitDef also implement TypeExpr for anonymous type forms.

// --- Expressions ---

// LiteralExpr is a literal value: int, float, string, bool, nil, color, unit.
type LiteralExpr struct {
	Pos  Pos
	Kind LiteralKind
	Raw  string // original source text
}

// UnitLiteral is the value stored in a LiteralExpr with Kind == LiteralUnit.
type UnitLiteral struct {
	Pos Pos
	LiteralExpr
	Suffix string // "px", "ms", "em"
}

// IdentExpr is an identifier reference.
type IdentExpr struct {
	Pos  Pos
	Name string
}

// EventRefExpr references an event by name: @click, @change.
type EventRefExpr struct {
	Pos  Pos
	Name string
}

// BinaryExpr is a binary operation.
type BinaryExpr struct {
	Pos   Pos
	Op    BinaryOp
	Left  Expr
	Right Expr
}

// UnaryExpr is a unary operation.
type UnaryExpr struct {
	Pos     Pos
	Op      UnaryOp
	Operand Expr
}

// TernaryExpr is a ternary conditional: cond ? then : else.
type TernaryExpr struct {
	Pos  Pos
	Cond Expr
	Then Expr
	Else Expr
}

// SelectExpr is member access: .field, .@event, .#ref, #ref.
type SelectExpr struct {
	Pos     Pos
	Operand Expr
	Field   string
	Kind    SelectKind
}

// IndexExpr is index access: operand[index].
type IndexExpr struct {
	Pos     Pos
	Operand Expr
	Index   Expr
}

// CallExpr is a call expression: callee(args...).
// Func is any expression — IdentExpr for plain calls, SelectExpr for method calls.
type CallExpr struct {
	Pos  Pos
	Func Expr
	Args ArgList
}

// StructFieldLit is a field in a struct literal.
type StructFieldLit struct {
	Name   string
	Value  Expr
	Spread bool // if true, Value is the spread operand
}

// StructExpr is a struct literal: Name{field = value, ...expr} or pkg.Name{...}.
type StructExpr struct {
	Pos       Pos
	Package   string // qualifier in pkg.Type (empty if unqualified)
	Name      string
	Fields    []StructFieldLit
	Multiline bool
}

// ListExpr is a list literal: [a, b, ...c].
type ListExpr struct {
	Pos         Pos
	Elements    []Expr
	IsMultiline bool
}

// SpreadExpr represents a spread operation: ...expr.
type SpreadExpr struct {
	Pos     Pos
	Operand Expr
}

// InterpolationExpr is a string with interpolated expressions.
// Parts alternate between *LiteralExpr (string) and expression nodes.
type InterpolationExpr struct {
	Pos   Pos
	Parts []Expr
	Style StringStyle
}

// ElementRefExpr references a visual element by its #id.
type ElementRefExpr struct {
	Pos  Pos
	Name string
}

// LambdaExpr is a function literal: func(params) => expr or func(params) [Type] { }.
// Exactly one of Body or Block is set.
type LambdaExpr struct {
	Pos        Pos
	Params     ParamList
	ReturnType TypeExpr
	Body       Expr      // expression form (=> expr)
	Block      StmtBlock // block form ({ ... })
}

// ParenExpr preserves explicit parentheses: (expr).
type ParenExpr struct {
	Pos   Pos
	Inner Expr
}

// ConstExpr asserts at check time that its operand is a constant expression.
// The wrapper has no runtime representation; the checker validates constness
// and then the optimizer folds the operand to a literal.
type ConstExpr struct {
	Pos     Pos
	Operand Expr
}

// --- Statements ---

// Stmt is any node valid inside a StmtBlock: declarations, statements, expressions.
type Stmt interface {
	StmtPos() *Pos
}

// AssignStmt is an assignment: target op= value.
type AssignStmt struct {
	Pos    Pos
	Target TargetExpr
	Op     AssignOp
	Value  Expr
}

// ToggleStmt is a boolean toggle: target!!.
type ToggleStmt struct {
	Pos    Pos
	Target TargetExpr
}

// IncDecStmt is a postfix increment or decrement: target++ / target--.
// Statement-only (not an expression). Lowered in the checker to
// `target = target + 1` or `target = target - 1`.
type IncDecStmt struct {
	Pos    Pos
	Target TargetExpr
	IsDec  bool // false: ++, true: --
}

// EmitStmt is an event emission: @name(args...).
type EmitStmt struct {
	Pos  Pos
	Name string
	Args ArgList
}

// StmtBlock is a braced list of statements: { stmt; stmt }.
type StmtBlock struct {
	Pos         Pos
	IsMultiline bool
	Stmts       []Stmt
}

// ArgList is an ordered list of arguments (positional, named, binding, event).
type ArgList struct {
	Pos         Pos
	IsMultiline bool
	Args        []ArgOrEventHandler
}

// Arg is a single argument: positional (Name empty) or named (Name set).
type Arg struct {
	Name  string
	Value Expr
}

// ArgOrEventHandler is an argument or inline event handler in an ArgList.
type ArgOrEventHandler interface {
	argOrEventHandler()
}

func (Arg) argOrEventHandler()          {}
func (EventHandler) argOrEventHandler() {}

func (x StmtBlock) IsDefined() bool { return x.Pos.IsSet() }

// VarStmt is a local variable declaration inside a function body.
type VarStmt struct {
	Pos  Pos
	Name string
	Type TypeExpr
	Init Expr
}

// ReturnStmt is a return statement in a block function.
type ReturnStmt struct {
	Pos   Pos
	Value Expr // nil for bare return
}

// CallStmt wraps a CallExpr used as a statement.
type CallStmt struct {
	Pos  Pos
	Call *CallExpr
}

// --- ExprPos implementations ---

func (x *LiteralExpr) ExprPos() *Pos       { return &x.Pos }
func (x *IdentExpr) ExprPos() *Pos         { return &x.Pos }
func (x *EventRefExpr) ExprPos() *Pos      { return &x.Pos }
func (x *BinaryExpr) ExprPos() *Pos        { return &x.Pos }
func (x *UnaryExpr) ExprPos() *Pos         { return &x.Pos }
func (x *TernaryExpr) ExprPos() *Pos       { return &x.Pos }
func (x *SelectExpr) ExprPos() *Pos        { return &x.Pos }
func (x *IndexExpr) ExprPos() *Pos         { return &x.Pos }
func (x *CallExpr) ExprPos() *Pos          { return &x.Pos }
func (x *StructExpr) ExprPos() *Pos        { return &x.Pos }
func (x *ListExpr) ExprPos() *Pos          { return &x.Pos }
func (x *SpreadExpr) ExprPos() *Pos        { return &x.Pos }
func (x *InterpolationExpr) ExprPos() *Pos { return &x.Pos }
func (x *ElementRefExpr) ExprPos() *Pos    { return &x.Pos }
func (x *LambdaExpr) ExprPos() *Pos        { return &x.Pos }
func (x *ParenExpr) ExprPos() *Pos         { return &x.Pos }
func (x *ConstExpr) ExprPos() *Pos         { return &x.Pos }
func (x *NamedType) ExprPos() *Pos         { return &x.Pos }
func (x *FuncType) ExprPos() *Pos          { return &x.Pos }
func (x *StructDef) ExprPos() *Pos         { return &x.Pos }
func (x *EnumDef) ExprPos() *Pos           { return &x.Pos }
func (x *UnitDef) ExprPos() *Pos           { return &x.Pos }
func (x *StmtBlock) ExprPos() *Pos         { return &x.Pos }

// --- typeExpr implementations ---

func (*NamedType) typeExpr() {}
func (*FuncType) typeExpr()  {}
func (*StructDef) typeExpr() {}
func (*EnumDef) typeExpr()   {}
func (*UnitDef) typeExpr()   {}

// --- targetExpr implementations ---

func (*IdentExpr) targetExpr()  {}
func (*SelectExpr) targetExpr() {}
func (*IndexExpr) targetExpr()  {}
func (*UnaryExpr) targetExpr()  {}

// --- StmtPos implementations ---

func (x *AssignStmt) StmtPos() *Pos { return &x.Pos }
func (x *ToggleStmt) StmtPos() *Pos { return &x.Pos }
func (x *IncDecStmt) StmtPos() *Pos { return &x.Pos }
func (x *EmitStmt) StmtPos() *Pos   { return &x.Pos }
func (x *VarStmt) StmtPos() *Pos    { return &x.Pos }
func (x *ReturnStmt) StmtPos() *Pos { return &x.Pos }
func (x *CallStmt) StmtPos() *Pos   { return &x.Pos }
