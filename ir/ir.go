package ir

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
	Symbols    *SymbolTable
}

func (p *Package) IsMain() bool {
	return slices.ContainsFunc(p.Components, func(c *Component) bool { return c.Name == "main" })
}

// Import records a resolved import.
type Import struct {
	AST    *ast.Import
	Path   string        // import URI (e.g., "widgets", "go://net/http")
	Alias  string        // effective namespace name; only allowed in main file declarations
	Pkg    *Package      // resolved SNGL package (nil for native)
	Native *NativeImport // non-nil for scheme imports
}

func (i *Import) SymName() string { return i.Alias }
func (i *Import) SymType() *Type  { return nil }

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
	Block      []Stmt // type-checked statements (expression bodies become a single Return)
	Purity     Purity
	IsTest     bool
	Reads      []*Var // vars read (directly or via called functions)
	Writes     []*Var // vars mutated (directly or via called functions)
	Intrinsic string // non-empty = intrinsic ID (e.g. "StrIndexOf"); codegen must provide native impl
}

func (f *Func) SymName() string { return f.Name }
func (f *Func) SymType() *Type  { return &Type{Kind: TypeFunc, Sig: f.FuncSig()} }

// FuncSig builds the FuncSig for this function.
func (f *Func) FuncSig() *FuncSig {
	return &FuncSig{
		Params:     f.Params,
		Return:     f.Return,
		TypeParams: f.TypeParams,
		Purity:     f.Purity,
	}
}

// Var represents a constant or variable declaration.
type Var struct {
	AST      ast.Stmt // original ConstDecl or VarDecl
	Name     string
	Type     *Type
	Init     Expr // checked initializer (nil if none)
	IsConst  bool
	Handlers []*EventHandler
}

func (v *Var) SymName() string { return v.Name }
func (v *Var) SymType() *Type  { return v.Type }

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
	Body         []Stmt // type-checked body statements
}

func (c *Component) SymName() string { return c.Name }
func (c *Component) SymType() *Type  { return &Type{Kind: TypeComponent, Decl: c} }

// Prop is a resolved component property.
type Prop struct {
	Name          string
	Type          *Type
	Default       Expr // nil if no default
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
	Body    []Stmt // type-checked body statements
	Checked bool   // true if body was already checked in context (e.g., inside a for-loop)
}

func (w *Window) SymName() string { return w.Name }
func (w *Window) SymType() *Type  { return nil }

// Timer represents a timer declaration at the component or package level.
// The timer body is a Func so codegen can reuse function transform logic.
type Timer struct {
	AST      *ast.VisualNode
	Interval Expr // checked interval expression (e.g., 500ms)
	Handler  *Func
}

// Output represents a resolved output directive. Only permitted in the main file.
type Output struct {
	AST      *ast.VisualNode
	Lang     string
	Platform string
	Options  map[string]string
}

// Param is a resolved function or component parameter.
type Param struct {
	Name    string
	Type    *Type
	Default Expr // nil if no default
}

func (p *Param) SymName() string { return p.Name }
func (p *Param) SymType() *Type  { return p.Type }

// StructDef is a resolved struct type declaration.
type StructDef struct {
	AST    *ast.StructDef
	Name   string
	Fields []*StructField
}

func (s *StructDef) SymName() string { return s.Name }
func (s *StructDef) SymType() *Type  { return &Type{Kind: TypeStruct, Decl: s} }

// StructField is a resolved field in a struct.
type StructField struct {
	Name    string
	Type    *Type
	Default Expr // nil if no default
}

// EnumDef is a resolved enum type declaration.
type EnumDef struct {
	AST     *ast.EnumDef
	Name    string
	Members []*EnumMember
}

func (e *EnumDef) SymName() string { return e.Name }
func (e *EnumDef) SymType() *Type  { return &Type{Kind: TypeEnum, Decl: e} }

// EnumMember is a single value in an enum.
type EnumMember struct {
	Name  string
	Value Expr // nil for bare members
}

// UnitDef is a resolved unit type declaration with conversion table.
type UnitDef struct {
	AST      *ast.UnitDef
	Name     string
	Suffixes []*UnitSuffix
}

func (u *UnitDef) SymName() string { return u.Name }
func (u *UnitDef) SymType() *Type  { return &Type{Kind: TypeUnit, Decl: u} }

// UnitSuffix is a resolved suffix within a unit declaration.
type UnitSuffix struct {
	Name   string
	Factor float64 // multiplier to base (1.0 for base suffix)
	IsBase bool
}

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

//go:generate go tool stringer -type=Severity

// Severity classifies a diagnostic.
type Severity int

const (
	Error Severity = iota
	Warning
)

// Target provides type information for a registered language or platform.
type Target interface {
	Identifier() string
	Package() []*ast.Document         // parsed .sngl API docs (includes Options struct)
	Resolve(identifier string) Symbol // dynamic identifiers (e.g., html.div); nil if unknown
}

// Language is a Target for a registered language translator.
type Language interface{ Target }

// Platform is a Target for a registered platform generator.
type Platform interface {
	Target
	IsLanguageSupported(Language) bool
}

// StaticTarget identifies the compile target by name.
type StaticTarget struct {
	Platform string
	Language string
}
