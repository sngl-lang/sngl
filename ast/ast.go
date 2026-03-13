package ast

import "fmt"

// Pos records the source position of an AST node.
type Pos struct {
	Line   int // 1-based line number
	Column int // 1-based column number
}

func (p Pos) String() string {
	if p.Line == 0 {
		return ""
	}
	return fmt.Sprintf("%d:%d", p.Line, p.Column)
}

// IsValid reports whether the position has been set.
func (p Pos) IsValid() bool { return p.Line > 0 }

type Output struct {
	Pos      Pos
	Lang     string            // "go"
	Platform string            // "bubbletea"
	Options  map[string]string // {"package": "main"}
}

type Document struct {
	Outputs    []*Output
	Structs    []*StructDef
	Imports    []*Import
	Binds      []*Bind
	Computeds  []*Computed
	Components []*Component
	Styles     []*StyleDecl
	App        *App
}

type StructDef struct {
	Pos    Pos
	Name   string
	Fields []*StructField
}

type StructField struct {
	Pos     Pos
	Name    string
	Type    string // type hint: "string", "bool", "int", etc.
	Default Expr
}

type Import struct {
	Pos  Pos
	Path string
}

type Bind struct {
	Pos  Pos
	Name string
	Init Expr
}

type Computed struct {
	Pos  Pos
	Name string
	Expr Expr
}

type StyleDecl struct {
	Pos   Pos
	Name  string
	Props map[string]Expr
}

type Component struct {
	Pos    Pos
	Name   string
	Params []*Param
	Body   []*VisualNode
}

type Param struct {
	Pos      Pos
	Name     string
	Default  Expr
	Required bool
}

type App struct {
	Pos      Pos
	Children []*VisualNode
}

type VisualNode struct {
	Pos        Pos
	Component  string
	ID         *Expr
	Key        *Expr
	Class      *Expr
	If         *Expr
	For        *ForClause
	Ref        *Expr
	Props      map[string]Expr
	Events     map[string]Expr
	StyleAttrs map[string]Expr
	StyleBlock map[string]Expr
	AttrNodes  map[string]*AttrNode
	Children   []*VisualNode
}

type AttrNode struct {
	Pos   Pos
	Name  string
	Props map[string]Expr
}
