package ast

type Document struct {
	Structs    []*StructDef
	Imports    []*Import
	Binds      []*Bind
	Computeds  []*Computed
	Components []*Component
	Styles     []*StyleDecl
	App        *App
}

type StructDef struct {
	Name   string
	Fields []*StructField
}

type StructField struct {
	Name    string
	Type    string // type hint: "string", "bool", "int", etc.
	Default Expr
}

type Import struct {
	Path string
}

type Bind struct {
	Name string
	Init Expr
}

type Computed struct {
	Name string
	Expr Expr
}

type StyleDecl struct {
	Name  string
	Props map[string]Expr
}

type Component struct {
	Name   string
	Params []*Param
	Body   []*VisualNode
}

type Param struct {
	Name     string
	Default  Expr
	Required bool
}

type App struct {
	Children []*VisualNode
}

type VisualNode struct {
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
	Name  string
	Props map[string]Expr
}
