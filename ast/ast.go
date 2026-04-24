package ast

import (
	"fmt"
	"strings"
)

// FileAsset records a file that needs to be copied to the output directory.
type FileAsset struct {
	SrcPath string // absolute source path
	OutPath string // output path relative to output dir
	URL     string // URL path for use in HTML
}

// Pos records the source position of an AST node.
type Pos struct {
	File   string // source filename (empty when unknown)
	Line   int    // 1-based line number
	Column int    // 1-based column number
}

func (p Pos) IsSet() bool   { return p != Pos{} }
func (p Pos) IsValid() bool { return p.Line > 0 }

func (p Pos) String() string {
	if p.Line == 0 {
		return ""
	}
	if p.File != "" {
		return fmt.Sprintf("%s:%d:%d", p.File, p.Line, p.Column)
	}
	return fmt.Sprintf("%d:%d", p.Line, p.Column)
}

// Comment is a source comment preserved for formatting.
type Comment struct {
	Pos    Pos
	Text   string // includes delimiters (// or /* */)
	Block  bool   // true for /* */ comments
	Inline bool   // true for trailing same-line comments
}

// DisabledDecl wraps any statement prefixed with slashdash (/-).
type DisabledDecl struct {
	Pos   Pos
	Inner Stmt
}

// Document is the top-level container for a .sngl file.
type Document struct {
	Stmts []Stmt
}

// --- Type declarations ---

// EnumMember is a single value in an enum declaration.
type EnumMember struct {
	Pos   Pos
	Name  string
	Value Expr // nil for bare members
}

// EnumDef declares an enum type. Name is empty for anonymous enum types.
type EnumDef struct {
	Pos         Pos
	Name        string
	Members     []EnumMember
	IsMultiline bool
}

// StructDef declares a struct type. Name is empty for anonymous struct types.
type StructDef struct {
	Pos         Pos
	Name        string
	Fields      []*StructField
	IsMultiline bool
}

// StructField is a field in a struct declaration.
// Names groups comma-separated fields sharing a type (e.g. `a, b int`);
// a default applies to every name.
type StructField struct {
	Pos     Pos
	Names   []string
	Type    TypeExpr
	Default Expr
}

// UnitDef declares a unit type with named suffixes.
// Name is empty for anonymous unit types.
type UnitDef struct {
	Pos         Pos
	Name        string
	Suffixes    []*UnitSuffix
	IsMultiline bool
}

// UnitSuffix defines a single suffix within a unit declaration.
type UnitSuffix struct {
	Pos    Pos
	Name   string // "ms", "px"
	Factor Expr   // nil for bare suffixes
}

// --- Variables and constants ---

// VarSpec is a single binding in a const or var declaration.
type VarSpec struct {
	Names    []string
	Type     TypeExpr
	Default  Expr
	Handlers []EventHandler
}

// ConstDecl declares one or more constants.
type ConstDecl struct {
	Pos       Pos
	IsGrouped bool
	Specs     []VarSpec
}

// VarDecl declares one or more variables.
type VarDecl struct {
	Pos       Pos
	IsGrouped bool
	Specs     []VarSpec
}

// --- Imports ---

// Import declares a module import, optionally aliased and/or replaced.
type Import struct {
	Pos     Pos
	Path    string // local import path (LHS of =>, or the bare string)
	Alias   string // explicit ident alias, "" if none
	Replace string // replacement URL (RHS of =>), "" if no replace
}

// --- Parameters ---

// Param is a parameter in a function, component, or lambda definition.
type Param struct {
	Pos           Pos
	Name          string
	Type          TypeExpr
	Default       Expr
	Bidirectional bool // :name — component binding param
}

// ParamList is an ordered list of parameters.
type ParamList struct {
	Pos         Pos
	IsMultiline bool
	Params      []Param
}

// --- Functions ---

// FuncDef declares a named function.
// Exactly one of Body or Block is set.
type FuncDef struct {
	Pos        Pos
	Name       string
	TypeParams []string // generic type parameters, e.g., ["T", "U"]
	Params     ParamList
	ReturnType TypeExpr  // nil for void/action functions
	Body       Expr      // single-expression form (=> expr)
	Block      StmtBlock // block form ({ ... })
}

// IsTest returns true if this function is a test function.
func (f *FuncDef) IsTest() bool { return strings.HasPrefix(f.Name, "test") }

// TestFuncs returns all document-level test functions.
func (d *Document) TestFuncs() []*FuncDef {
	var out []*FuncDef
	for _, s := range d.Stmts {
		if fn, ok := s.(*FuncDef); ok && fn.IsTest() {
			out = append(out, fn)
		}
	}
	return out
}

// SplitMethodName splits "int.sqrt" into ("int", "sqrt", true).
func SplitMethodName(name string) (typeName, method string, ok bool) {
	if before, after, ok0 := strings.Cut(name, "."); ok0 {
		return before, after, true
	}
	return "", name, false
}

// --- Components ---

// ComponentDecl declares a component with props and a body.
type ComponentDecl struct {
	Pos          Pos
	Name         string
	Props        PropList
	ChildrenType TypeExpr
	Body         StmtBlock
}

// PropList is the parameter list of a component declaration.
type PropList struct {
	Pos         Pos
	IsMultiline bool
	Props       []ParamOrEventDecl
}

// ParamOrEventDecl is a component parameter or event declaration.
type ParamOrEventDecl interface {
	paramOrEventDecl()
}

func (Param) paramOrEventDecl()     {}
func (EventDecl) paramOrEventDecl() {}

// EventDecl declares an event on a component: @click, @change Type.
type EventDecl struct {
	Pos  Pos
	Name string
	Type TypeExpr // optional type annotation
}

// EventHandler is an event handler: @name[(params)] { body }.
type EventHandler struct {
	Pos    Pos
	Name   string
	Params ParamList
	Body   StmtBlock
}

// --- Visual nodes ---

// VisualNode is a visual element instantiation with optional args and body.
type VisualNode struct {
	Pos    Pos
	Target TargetExpr
	Args   ArgList
	ID     string // element ID from #id syntax
	Block  StmtBlock
}

// --- Control flow ---

// IfStmt: if cond { body } [else { alt }].
type IfStmt struct {
	Pos  Pos
	Cond Expr
	Body StmtBlock
	Else StmtBlock // zero value if no else
}

// ForStmt: for key [, value] = iter { body } [else { alt }].
type ForStmt struct {
	Pos   Pos
	Key   string // iterator variable
	Value string // optional second variable (empty if single-var form)
	Iter  Expr
	Body  StmtBlock
	Else  StmtBlock // zero value if no else
}

// PlatformStmt: platform ident { body }.
// Conditional on the target platform; also injects the platform's package as a fallback scope.
type PlatformStmt struct {
	Pos      Pos
	Platform string // "html", "bubbletea", etc.
	Body     StmtBlock
}

// --- StmtPos implementations ---

func (s *StructDef) StmtPos() *Pos     { return &s.Pos }
func (e *EnumDef) StmtPos() *Pos       { return &e.Pos }
func (u *UnitDef) StmtPos() *Pos       { return &u.Pos }
func (c *ConstDecl) StmtPos() *Pos     { return &c.Pos }
func (c *VarDecl) StmtPos() *Pos       { return &c.Pos }
func (f *FuncDef) StmtPos() *Pos       { return &f.Pos }
func (i *Import) StmtPos() *Pos        { return &i.Pos }
func (c *ComponentDecl) StmtPos() *Pos { return &c.Pos }
func (vn *VisualNode) StmtPos() *Pos   { return &vn.Pos }
func (s *IfStmt) StmtPos() *Pos        { return &s.Pos }
func (s *ForStmt) StmtPos() *Pos       { return &s.Pos }
func (s *PlatformStmt) StmtPos() *Pos  { return &s.Pos }
func (c *Comment) StmtPos() *Pos       { return &c.Pos }
func (d *DisabledDecl) StmtPos() *Pos  { return &d.Pos }
