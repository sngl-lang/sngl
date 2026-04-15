package checker

import (
	"slices"

	"git.duckfam.us/jonathan/sngl/ast"
)

// Package is the top-level IR for a checked .sngl package.
type Package struct {
	Imports    []*Import
	Structs    []*StructDef
	Enums      []*EnumDef
	Units      []*UnitDef
	Consts     []*Var
	Vars       []*Var
	Funcs      []*Func
	Components []*Component
	Windows    []*Window
	Timers     []*Timer
	Outputs    []*Output
	TypeMap    map[ast.Expr]*Type // every expression → its resolved type
	Symbols    *SymbolTable
	Stdlib     *Package // stdlib package (nil on the stdlib package itself)
}

func (p *Package) IsMain() bool {
	return slices.ContainsFunc(p.Components, func(c *Component) bool { return c.Name == "main" })
}

// Import records a resolved import.
type Import struct {
	AST    *ast.Import
	Alias  string        // effective namespace name; only allowed in main file declarations
	Pkg    *Package      // resolved SNGL package (nil for native)
	Native *NativeImport // non-nil for scheme imports
	Pos    ast.Pos
}

func (i *Import) SymName() string { return i.Alias }
func (i *Import) SymType() *Type  { return nil }
func (i *Import) SymPos() ast.Pos { return i.Pos }

// NativeImport holds declarations from a scheme import (go://, ts://, etc.).
type NativeImport struct {
	ImportPath string
	Structs    []*StructDef
	Enums      []*EnumDef
	Funcs      []*Func
	Vars       []*Var
}

// Func represents any function: top-level, type-attached method, or lambda.
type Func struct {
	AST        *ast.FuncDef // nil for lambdas and event handlers
	Name       string       // empty for lambdas and event handlers
	Receiver   string       // "int" for int.abs (empty for plain funcs)
	TypeParams []string
	Params     []*Param
	Return     *Type
	Body       ast.Expr       // expression form (=> expr)
	ASTBlock   *ast.StmtBlock // block form ({ ... })
	Block      []Stmt         // type-checked block statements (nil for expression form)
	Purity     Purity
	IsTest     bool
	Reads      []*Var // vars read (directly or via called functions)
	Writes     []*Var // vars mutated (directly or via called functions)
	Pos        ast.Pos
}

func (f *Func) SymName() string { return f.Name }
func (f *Func) SymType() *Type  { return &Type{Kind: TypeFunc, Sig: f.FuncSig()} }
func (f *Func) SymPos() ast.Pos { return f.Pos }

// FuncSig builds the FuncSig for this function.
func (f *Func) FuncSig() *FuncSig {
	return &FuncSig{
		Params:     f.Params,
		Return:     f.Return,
		TypeParams: f.TypeParams,
		Purity:     f.Purity,
	}
}

// VarAccess records a single read or write to a variable.
type VarAccess struct {
	Pos   ast.Pos
	Write bool // true for mutations
}

// Var represents a constant or variable declaration.
type Var struct {
	AST      ast.Stmt // original ConstDecl or VarDecl
	Name     string
	Type     *Type
	IsConst  bool
	Handlers []*EventHandler
	Refs     []VarAccess // all access sites
	Pos      ast.Pos
}

func (v *Var) SymName() string { return v.Name }
func (v *Var) SymType() *Type  { return v.Type }
func (v *Var) SymPos() ast.Pos { return v.Pos }

// Component represents a resolved component declaration.
type Component struct {
	AST          *ast.ComponentDecl
	Name         string
	Props        []*Prop
	Events       []*EventDecl
	ChildrenType *Type
	Vars         []*Var
	Funcs        []*Func
	Timers       []*Timer
	Body         *ast.StmtBlock // raw AST (kept for formatter and legacy codegen)
	IRBody       []Stmt         // type-checked body statements
	Pos          ast.Pos
}

func (c *Component) SymName() string { return c.Name }
func (c *Component) SymType() *Type  { return &Type{Kind: TypeComponent, Decl: c} }
func (c *Component) SymPos() ast.Pos { return c.Pos }

// Prop is a resolved component property.
type Prop struct {
	Name          string
	Type          *Type
	Default       ast.Expr
	Bidirectional bool
}

// EventDecl is a resolved event declaration on a component.
type EventDecl struct {
	Name string
	Type *Type // payload type; nil for void events
}

// EventHandler is a resolved event handler. The handler body is represented
// as a Func so codegen can reuse its function transform logic.
type EventHandler struct {
	AST  *ast.EventHandler
	Name string
	Func *Func
}

// Window represents a window declaration at the root or component level.
type Window struct {
	AST     *ast.VisualNode
	Name    string
	Vars    []*Var
	Funcs   []*Func
	Body    *ast.StmtBlock // raw AST (kept for formatter and legacy codegen)
	IRBody  []Stmt         // type-checked body statements
	Pos     ast.Pos
	Checked bool // true if body was already checked in context (e.g., inside a for-loop)
}

func (w *Window) SymName() string { return w.Name }
func (w *Window) SymType() *Type  { return nil }
func (w *Window) SymPos() ast.Pos { return w.Pos }

// Timer represents a timer declaration at the component or package level.
// The timer body is a Func so codegen can reuse function transform logic.
type Timer struct {
	AST     *ast.VisualNode
	Handler *Func
	Pos     ast.Pos
}

// Output represents a resolved output directive. Only permitted in the main file.
type Output struct {
	AST      *ast.VisualNode
	Lang     string
	Platform string
	Options  map[string]string
	Pos      ast.Pos
}

// Param is a resolved function or component parameter.
type Param struct {
	Name       string
	Type       *Type
	HasDefault bool
	Pos        ast.Pos
}

func (p *Param) SymName() string { return p.Name }
func (p *Param) SymType() *Type  { return p.Type }
func (p *Param) SymPos() ast.Pos { return p.Pos }

// StructDef is a resolved struct type declaration.
type StructDef struct {
	AST    *ast.StructDef
	Name   string
	Fields []*StructField
	Pos    ast.Pos
}

func (s *StructDef) SymName() string { return s.Name }
func (s *StructDef) SymType() *Type  { return &Type{Kind: TypeStruct, Decl: s} }
func (s *StructDef) SymPos() ast.Pos { return s.Pos }

// StructField is a resolved field in a struct.
type StructField struct {
	Name    string
	Type    *Type
	Default ast.Expr
}

// EnumDef is a resolved enum type declaration.
type EnumDef struct {
	AST     *ast.EnumDef
	Name    string
	Members []*EnumMember
	Pos     ast.Pos
}

func (e *EnumDef) SymName() string { return e.Name }
func (e *EnumDef) SymType() *Type  { return &Type{Kind: TypeEnum, Decl: e} }
func (e *EnumDef) SymPos() ast.Pos { return e.Pos }

// EnumMember is a single value in an enum.
type EnumMember struct {
	Name  string
	Value ast.Expr // nil for bare members
}

// UnitDef is a resolved unit type declaration with conversion table.
type UnitDef struct {
	AST      *ast.UnitDef
	Name     string
	Suffixes []*UnitSuffix
	Pos      ast.Pos
}

func (u *UnitDef) SymName() string { return u.Name }
func (u *UnitDef) SymType() *Type  { return &Type{Kind: TypeUnit, Decl: u} }
func (u *UnitDef) SymPos() ast.Pos { return u.Pos }

// UnitSuffix is a resolved suffix within a unit declaration.
type UnitSuffix struct {
	Name   string
	Factor float64 // multiplier to base (1.0 for base suffix)
	IsBase bool
}

// --- IR statement types ---
//
// These capture type-checked statements so codegen never needs to
// disambiguate ast.VisualNode (component? function? element?) itself.

// Stmt is a type-checked statement node.
type Stmt interface {
	stmtNode()
	StmtPos() ast.Pos
}

// NodeInst is a resolved component or platform-element instantiation.
// Component is non-nil when instantiating a user-defined component.
type NodeInst struct {
	AST       ast.Stmt    // original *ast.VisualNode (or *ast.CallStmt for Foo() that's a component)
	Name      string      // resolved element/component name
	Component *Component  // non-nil for user component; nil for platform element
	Args      ast.ArgList // props and event handlers
	Children  []Stmt      // type-checked body
	ID        string      // #id binding
	Ref       *ast.Expr   // ref binding
	Pos       ast.Pos
}

func (*NodeInst) stmtNode()          {}
func (n *NodeInst) StmtPos() ast.Pos { return n.Pos }

// CallStmt is a void function call — definitively not a component.
type CallStmt struct {
	AST  ast.Stmt      // original *ast.CallStmt or *ast.VisualNode
	Call *ast.CallExpr // the call expression (may be nil for bare-ident visual nodes)
	Func *Func         // resolved function, if known
	Pos  ast.Pos
}

func (*CallStmt) stmtNode()          {}
func (n *CallStmt) StmtPos() ast.Pos { return n.Pos }

// SlotInst is the slot pseudo-element.
type SlotInst struct {
	AST      *ast.VisualNode
	Children []Stmt
	Pos      ast.Pos
}

func (*SlotInst) stmtNode()          {}
func (n *SlotInst) StmtPos() ast.Pos { return n.Pos }

// Assign wraps a type-checked assignment statement.
type Assign struct {
	AST *ast.AssignStmt
	Pos ast.Pos
}

func (*Assign) stmtNode()          {}
func (n *Assign) StmtPos() ast.Pos { return n.Pos }

// Toggle wraps a type-checked toggle statement.
type Toggle struct {
	AST *ast.ToggleStmt
	Pos ast.Pos
}

func (*Toggle) stmtNode()          {}
func (n *Toggle) StmtPos() ast.Pos { return n.Pos }

// Emit wraps a type-checked event emission.
type Emit struct {
	AST *ast.EmitStmt
	Pos ast.Pos
}

func (*Emit) stmtNode()          {}
func (n *Emit) StmtPos() ast.Pos { return n.Pos }

// LocalVar wraps a local variable declaration with its resolved type.
type LocalVar struct {
	AST  *ast.VarStmt
	Type *Type
	Pos  ast.Pos
}

func (*LocalVar) stmtNode()          {}
func (n *LocalVar) StmtPos() ast.Pos { return n.Pos }

// Return wraps a type-checked return statement.
type Return struct {
	AST *ast.ReturnStmt
	Pos ast.Pos
}

func (*Return) stmtNode()          {}
func (n *Return) StmtPos() ast.Pos { return n.Pos }

// If is a type-checked if statement with IR bodies.
type If struct {
	AST  *ast.IfStmt
	Body []Stmt
	Else []Stmt
	Pos  ast.Pos
}

func (*If) stmtNode()          {}
func (n *If) StmtPos() ast.Pos { return n.Pos }

// For is a type-checked for statement with IR bodies and resolved element type.
type For struct {
	AST      *ast.ForStmt
	ElemType *Type
	Body     []Stmt
	Else     []Stmt
	Pos      ast.Pos
}

func (*For) stmtNode()          {}
func (n *For) StmtPos() ast.Pos { return n.Pos }

// PlatformFilter is a type-checked platform statement with IR body.
type PlatformFilter struct {
	AST  *ast.PlatformStmt
	Body []Stmt
	Pos  ast.Pos
}

func (*PlatformFilter) stmtNode()          {}
func (n *PlatformFilter) StmtPos() ast.Pos { return n.Pos }

// Diagnostic is a structured error or warning with position.
type Diagnostic struct {
	Pos      ast.Pos
	Msg      string
	Severity Severity
}

func (d Diagnostic) Error() string {
	if d.Pos.IsValid() {
		return d.Pos.String() + ": " + d.Msg
	}
	return d.Msg
}

// Severity classifies a diagnostic.
type Severity int

const (
	Error Severity = iota
	Warning
)
