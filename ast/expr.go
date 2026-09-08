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

// --- Type expressions ---

// NamedType is a type reference: int, pkg.Type, List<int>.
type NamedType struct {
	Pos      Pos
	Name     string     // type name
	Package  string     // qualifier in pkg.Type (empty if unqualified)
	TypeArgs []TypeExpr // generic arguments: List<int>, Map<string, int> (nil if not generic)
}

// FuncTypeParam is one parameter in a func-type expression.
// Name is empty when the parameter has no declared name (positional-only when called).
type FuncTypeParam struct {
	Name string
	Type TypeExpr
}

// FuncType is a function type: func(int, string) -> bool.
type FuncType struct {
	Pos    Pos
	Params []FuncTypeParam // parameter types (Name="" means anonymous/positional-only)
	Return TypeExpr        // nil for void
}

// StructDef, EnumDef, and UnitDef also implement TypeExpr for anonymous type forms.

// --- Expressions ---

// LiteralExpr is a literal value: int, float, string, bool, nil, color, unit.
type LiteralExpr struct {
	Pos  Pos
	Kind LiteralKind
	// Raw is the source spelling between the delimiters — a string's escapes
	// unprocessed. StringValue decodes it; ir.Literal.Value is that decode.
	Raw string
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

// SelectExpr is member access: operand.field.
type SelectExpr struct {
	Pos     Pos
	Operand Expr
	Field   string
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
	// ID is the element-reference name from `Func #id(args)` declaration
	// syntax (e.g. `button #inc(...)`, `context #locale(...)`). Empty for
	// ordinary calls. Node declarations carry it onto VisualNode.ID.
	ID string
}

// StructFieldLit is a field in a struct literal.
// NamePos records the source position of the Name identifier (zero
// when synthesized or when Spread is true).
type StructFieldLit struct {
	Name    string
	NamePos Pos
	Value   Expr
	Spread  bool // if true, Value is the spread operand
}

// IsShorthand reports whether the field was written `{a}` rather than
// `{a = a}`. The parser puts the shorthand's ident at the *name's* position,
// since `{a}` is one token and `{a = a}` is two. A field with no position is
// one the compiler synthesized, and those are written out in full.
func (f StructFieldLit) IsShorthand() bool {
	if f.Spread || !f.NamePos.IsSet() {
		return false
	}
	ident, ok := f.Value.(*IdentExpr)
	return ok && ident.Name == f.Name && ident.Pos == f.NamePos
}

// NativeRef names a foreign declaration the way an encoded value writes it:
// the import path it was read through, scheme and all, and its name there.
// Path is the whole path a program's own import line would carry, so nothing
// has to reassemble one to compare the two.
type NativeRef struct{ Path, Name string }

// StructExpr is a struct literal: Name{field = value, ...expr} or pkg.Name{...}.
type StructExpr struct {
	Pos     Pos
	Package string // qualifier in pkg.Type (empty if unqualified)
	Name    string
	// Native is the foreign declaration the value names. Only
	// parser.ParseNativeValue can produce one; it names the declaration
	// outright where Name would leave the reader matching on a bare name.
	Native *NativeRef `json:",omitempty"`
	// Anon is the inline type of a `struct { … }{ … }` value: the literal
	// spells its own type rather than naming one, so the declaration travels
	// with the expression instead of sitting at the top of the file.
	Anon      *StructDef `json:",omitempty"`
	Fields    []StructFieldLit
	Multiline bool
}

// ListExpr is a list literal: [a, b, ...c].
type ListExpr struct {
	Pos         Pos
	Elements    []Expr
	IsMultiline bool
}

// MapLit is a map literal: {<keyExpr>: <valueExpr>, ...}.
// The colon separator distinguishes from struct literals (which use =).
type MapLit struct {
	Pos       Pos
	Entries   []MapEntry
	Multiline bool
}

// MapEntry is one key-value pair in a MapLit.
type MapEntry struct {
	Pos   Pos
	Key   Expr
	Value Expr
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

// StmtBlock is a braced list of statements: { stmt; stmt }.
type StmtBlock struct {
	Pos         Pos
	IsMultiline bool
	Stmts       []Stmt
	// EndPos is the closing brace. A block the parser synthesized around a
	// braceless body has none, and it is the zero Pos there. Knowing where a
	// block ends is what lets the formatter place a comment inside it and
	// space declarations the way the source did, rather than guess from the
	// statement count.
	EndPos Pos
}

// ArgList is an ordered list of arguments (positional, named, binding, event).
type ArgList struct {
	Pos         Pos
	IsMultiline bool
	Args        []ArgOrEventHandler
}

// Arg is a single argument: positional (Name empty) or named (Name set).
// NamePos records the source position of the Name identifier for named
// args (zero Pos otherwise). Used by LSP semantic tokens and prop hover.
type Arg struct {
	Name    string
	NamePos Pos
	Value   Expr
	// Type is the type written after a positional argument, which only a slot
	// population may do — `slot cell(row Row)`. In a call the builder refuses
	// it, so nothing downstream sees one.
	Type TypeExpr `json:",omitempty"`
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

// BreakStmt and ContinueStmt are the loop escapes. Neither carries a label:
// they act on the innermost enclosing loop, which is what the checker verifies
// there is one of.
type BreakStmt struct {
	Pos Pos
}

type ContinueStmt struct {
	Pos Pos
}

// CallStmt wraps a CallExpr used as a statement.
type CallStmt struct {
	Pos  Pos
	Call *CallExpr
}

// I18nInterpExpr is a translatable string: $"text {placeholder} more".
// Parts alternate between *LiteralExpr (string segment) and *I18nPlaceholderExpr.
type I18nInterpExpr struct {
	Pos   Pos
	Parts []Expr
	Style StringStyle
}

// I18nPlaceholderExpr is a single placeholder inside an I18nInterpExpr:
// {expr} or {expr, type} or {expr, type, case1{...} case2{...}} or {expr, type, style}.
type I18nPlaceholderExpr struct {
	Pos   Pos
	Value Expr
	Type  string     // e.g. "plural", "select", "number", "date" — empty for simple {expr}
	Style string     // bare style ident e.g. "short", "medium", "currency" — mutually exclusive with Cases
	Cases []I18nCase // non-nil only when Type is set and cases are present
	// Multiline records that the cases were written one per line. A message
	// with four branches is unreadable on one, and the author's choice is the
	// only thing that says which it is.
	Multiline bool
}

// I18nCase is one branch of a plural/select formatter: selector{body}.
type I18nCase struct {
	Pos      Pos
	Selector string // "one", "other", "=0", etc.
	Body     []Expr // literal segments and nested *I18nPlaceholderExpr
	// Leading holds the comments written above the case. A placeholder is an
	// expression, so its comments have no statement list to live in.
	Leading []*Comment `json:",omitempty"`
}

// --- ExprPos implementations ---

func (x *LiteralExpr) ExprPos() *Pos         { return &x.Pos }
func (x *IdentExpr) ExprPos() *Pos           { return &x.Pos }
func (x *EventRefExpr) ExprPos() *Pos        { return &x.Pos }
func (x *BinaryExpr) ExprPos() *Pos          { return &x.Pos }
func (x *UnaryExpr) ExprPos() *Pos           { return &x.Pos }
func (x *TernaryExpr) ExprPos() *Pos         { return &x.Pos }
func (x *SelectExpr) ExprPos() *Pos          { return &x.Pos }
func (x *IndexExpr) ExprPos() *Pos           { return &x.Pos }
func (x *CallExpr) ExprPos() *Pos            { return &x.Pos }
func (x *StructExpr) ExprPos() *Pos          { return &x.Pos }
func (x *ListExpr) ExprPos() *Pos            { return &x.Pos }
func (x *MapLit) ExprPos() *Pos              { return &x.Pos }
func (x *SpreadExpr) ExprPos() *Pos          { return &x.Pos }
func (x *InterpolationExpr) ExprPos() *Pos   { return &x.Pos }
func (x *LambdaExpr) ExprPos() *Pos          { return &x.Pos }
func (x *ParenExpr) ExprPos() *Pos           { return &x.Pos }
func (x *ConstExpr) ExprPos() *Pos           { return &x.Pos }
func (x *I18nInterpExpr) ExprPos() *Pos      { return &x.Pos }
func (x *I18nPlaceholderExpr) ExprPos() *Pos { return &x.Pos }
func (x *NamedType) ExprPos() *Pos           { return &x.Pos }
func (x *FuncType) ExprPos() *Pos            { return &x.Pos }
func (x *StructDef) ExprPos() *Pos           { return &x.Pos }
func (x *EnumDef) ExprPos() *Pos             { return &x.Pos }
func (x *UnitDef) ExprPos() *Pos             { return &x.Pos }
func (x *StmtBlock) ExprPos() *Pos           { return &x.Pos }

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

func (x *AssignStmt) StmtPos() *Pos   { return &x.Pos }
func (x *ToggleStmt) StmtPos() *Pos   { return &x.Pos }
func (x *IncDecStmt) StmtPos() *Pos   { return &x.Pos }
func (x *VarStmt) StmtPos() *Pos      { return &x.Pos }
func (x *ReturnStmt) StmtPos() *Pos   { return &x.Pos }
func (x *BreakStmt) StmtPos() *Pos    { return &x.Pos }
func (x *ContinueStmt) StmtPos() *Pos { return &x.Pos }
func (x *CallStmt) StmtPos() *Pos     { return &x.Pos }
